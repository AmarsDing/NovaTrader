"""Opt-in, pinned-client mTLS JSON service. No insecure listener is provided."""

import hashlib
from http.server import BaseHTTPRequestHandler, HTTPServer
import ipaddress
import json
import re
import ssl
import threading
from urllib.parse import unquote, urlsplit

from .protocol import PROTOCOL, ProtocolError


def reject_constants(_):
    raise ValueError("NONFINITE_JSON")


def unique_object(items):
    result = {}
    for key, value in items:
        if key in result:
            raise ValueError("DUPLICATE_JSON_KEY")
        result[key] = value
    return result


class GatewayServer(HTTPServer):
    """Bounded workers including TLS handshakes; slow peers cannot create unlimited threads."""
    def __init__(self, address, gateway, tls_context, client_fingerprints, *, max_workers=8):
        if tls_context.verify_mode != ssl.CERT_REQUIRED or not client_fingerprints:
            raise ProtocolError("MTLS_AND_PIN_ALLOWLIST_REQUIRED")
        self.gateway, self.tls_context = gateway, tls_context
        self.client_fingerprints = frozenset(client_fingerprints)
        self.slots = threading.BoundedSemaphore(max_workers)
        super().__init__(address, Handler)

    def process_request(self, request, address):
        if not self.slots.acquire(blocking=False):
            self.shutdown_request(request)
            return
        threading.Thread(target=self._worker, args=(request, address), daemon=True).start()

    def _worker(self, request, address):
        secured = None
        try:
            request.settimeout(5)
            secured = self.tls_context.wrap_socket(request, server_side=True)
            certificate = secured.getpeercert(binary_form=True)
            if hashlib.sha256(certificate).hexdigest() not in self.client_fingerprints:
                return
            self.finish_request(secured, address)
        except (OSError, ssl.SSLError):
            pass
        finally:
            self.shutdown_request(secured or request)
            self.slots.release()


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "NovaTraderBroker/isolated-v1"

    def log_message(self, *_):
        pass  # No account identifiers, order paths or certificate subjects in stdout logs.

    def _reply(self, status, value):
        body = json.dumps(value, ensure_ascii=True, allow_nan=False, separators=(",", ":")).encode("ascii")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(body)
        self.close_connection = True

    def _handle(self, method):
        gateway = self.server.gateway
        try:
            if self.headers.get("X-NT-Protocol") != PROTOCOL:
                return self._reply(409, {"error": "PROTOCOL_MISMATCH"})
            if self.headers.get("X-NT-Account") != gateway.storage.account_id:
                return self._reply(403, {"error": "ACCOUNT_NOT_ALLOWED"})
            target = urlsplit(self.path)
            if target.query or target.fragment or target.netloc:
                raise ProtocolError("INVALID_ENDPOINT")
            path = target.path
            if method == "POST":
                if self.headers.get("Transfer-Encoding") or self.headers.get("Content-Type", "").split(";", 1)[0] != "application/json":
                    raise ProtocolError("JSON_BODY_REQUIRED")
                length = int(self.headers.get("Content-Length", "0"))
                if not 0 < length <= 65536:
                    raise ProtocolError("INVALID_BODY_LENGTH")
                body = self.rfile.read(length)
                if len(body) != length:
                    raise ProtocolError("PARTIAL_JSON_BODY")
                data = json.loads(body, parse_constant=reject_constants, object_pairs_hook=unique_object)
                if not isinstance(data, dict):
                    raise ProtocolError("JSON_OBJECT_REQUIRED")
                if path == "/v1/broker/orders" and set(data) == {"order"}:
                    payload = gateway.submit(data["order"])
                elif path == "/v1/broker/cancels":
                    payload = gateway.cancel(data)
                elif path == "/v1/broker/reconcile" and set(data) == {"expected"}:
                    payload = gateway.reconcile(data["expected"])
                else:
                    return self._reply(404, {"error": "UNKNOWN_ENDPOINT_OR_FIELDS"})
            elif path == "/v1/broker/health":
                payload = gateway.health()
            elif path == "/v1/broker/snapshot":
                payload = gateway.snapshot()
            elif path.startswith("/v1/broker/orders/"):
                payload = gateway.query(unquote(path[len("/v1/broker/orders/"):]))
            else:
                return self._reply(404, {"error": "UNKNOWN_ENDPOINT"})
            self._reply(200, {"protocol": PROTOCOL, "account_id": gateway.storage.account_id, "payload": payload})
        except ProtocolError as error:
            self._reply(409 if str(error) in {"IDENTITY_CONFLICT", "CANCEL_REQUIRES_CURRENT_ORDER_EVIDENCE"} else 400, {"error": str(error)})
        except (ValueError, UnicodeError):
            self._reply(400, {"error": "INVALID_JSON_OR_LENGTH"})
        except OSError:
            self._reply(503, {"error": "TRANSPORT_UNAVAILABLE_QUERY_ONLY"})

    def do_GET(self):
        self._handle("GET")

    def do_POST(self):
        self._handle("POST")


def create_server(gateway, config):
    required = {"host", "port", "ca_file", "cert_file", "key_file", "allowed_client_sha256"}
    if not isinstance(config, dict) or set(config) != required:
        raise ProtocolError("INVALID_TLS_CONFIGURATION")
    if not isinstance(config["port"], int) or isinstance(config["port"], bool) or not 0 <= config["port"] <= 65535:
        raise ProtocolError("INVALID_LISTENER_PORT")
    try:
        address = ipaddress.ip_address(config["host"])
    except ValueError:
        raise ProtocolError("LITERAL_BIND_ADDRESS_REQUIRED") from None
    if address.version != 4:
        raise ProtocolError("IPV4_BIND_REQUIRED")
    pins = config["allowed_client_sha256"]
    if not isinstance(pins, list) or not pins or any(not isinstance(pin, str) or not re.fullmatch(r"[0-9a-f]{64}", pin) for pin in pins):
        raise ProtocolError("CLIENT_CERTIFICATE_PINS_REQUIRED")
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.minimum_version = ssl.TLSVersion.TLSv1_2
    context.verify_mode = ssl.CERT_REQUIRED
    context.load_verify_locations(cafile=config["ca_file"])
    context.load_cert_chain(config["cert_file"], config["key_file"])
    return GatewayServer((str(address), config["port"]), gateway, context, pins)


def serve(gateway, config_path):
    with open(config_path, encoding="utf-8") as handle:
        config = json.load(handle, object_pairs_hook=unique_object)
    with create_server(gateway, config) as server:
        print(json.dumps({"event": "LISTENING", "host": server.server_address[0], "port": server.server_address[1],
                          "protocol": PROTOCOL, "mode": "ISOLATED", "can_open_live": False}), flush=True)
        server.serve_forever(poll_interval=0.2)
