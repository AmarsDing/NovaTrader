"""Explicit commands only; importing this package never starts a server or scanner."""

import argparse
import json
import sys

from .gateway import Gateway
from .protocol import ProtocolError
from .storage import initialize


def main():
    parser = argparse.ArgumentParser(description="NovaTrader isolated file-order gateway")
    parser.add_argument("command", choices=["init", "submit", "query", "cancel", "snapshot", "health", "reconcile", "demo", "serve"])
    parser.add_argument("--root", required=True)
    parser.add_argument("--account", default="TEST_ACCOUNT")
    parser.add_argument("--sid")
    parser.add_argument("--tls-config", help="Explicit mTLS configuration file, required by serve")
    args = parser.parse_args()
    try:
        if args.command == "init":
            result = initialize(args.root)
        else:
            gateway = Gateway(args.root, args.account)
            if args.command == "serve":
                if not args.tls_config:
                    raise ProtocolError("TLS_CONFIGURATION_REQUIRED")
                from .server import serve
                serve(gateway, args.tls_config)
                return 0
            if args.command == "demo":
                from .simulator import Simulator
                simulator = Simulator(args.root, args.account)
                receipt = gateway.submit({"sid": "isolated-demo-1", "symbol": "600519.SH", "side": "buy", "price": "10.50", "volume": 200})
                before = gateway.query("isolated-demo-1")
                simulator.scan()
                simulator.fill("isolated-demo-1", 100, "demo-fill-1")
                partial = gateway.query("isolated-demo-1")
                cancellation = gateway.cancel({"request_id": "isolated-cancel-1", "sid": "isolated-demo-1"})
                simulator.scan()
                result = {"published": receipt, "before_terminal": before, "partial": partial, "cancel_transport": cancellation,
                          "terminal": gateway.query("isolated-demo-1"), "reconciliation": gateway.reconcile(simulator.expected())}
            elif args.command in {"submit", "cancel", "reconcile"}:
                raw = sys.stdin.read(65537)
                if len(raw) > 65536:
                    raise ProtocolError("INPUT_TOO_LARGE")
                result = getattr(gateway, args.command)(json.loads(raw))
            elif args.command == "query":
                result = gateway.query(args.sid)
            else:
                result = getattr(gateway, args.command)()
        print(json.dumps(result, ensure_ascii=True, sort_keys=True))
        return 0
    except (ProtocolError, OSError, ValueError) as error:
        code = str(error) if isinstance(error, ProtocolError) else "INPUT_OR_IO_ERROR"
        print(json.dumps({"error": code, "can_open_live": False}), file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
