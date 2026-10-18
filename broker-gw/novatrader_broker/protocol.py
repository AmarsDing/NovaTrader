"""Versioned, strict subset of the official CSV protocol (not a live certification)."""

import csv
import io
import re
from decimal import Decimal, InvalidOperation


PROTOCOL = "eastmoney-file-csv-isolated-v1"
ORDER_HEADER = ("sid", "account_id", "symbol", "volume", "order_type",
                "order_business(order_biz)", "price", "comment")
CANCEL_HEADER = ("sid", "comment")
STATUS = {"1": "ACKNOWLEDGED", "2": "PARTIAL", "3": "FILLED", "5": "CANCELLED",
          "8": "REJECTED", "10": "PENDING", "12": "EXPIRED"}
EXECUTION = {"15": "FILL", "19": "CANCEL_REJECTED"}
HEADERS = {
    "cash.csv": ("account_id", "market_value(market_val)", "nav", "pnl", "fpnl", "frozen",
                 "available", "balance", "created_at", "updated_at", "recv_at", "order_frozen(ord_frozen)"),
    "position.csv": ("account_id", "symbol", "side", "volume", "volume_today(vol_today)", "vwap",
                     "vwap_diluted(vwap_dild)", "market_value(market_val)", "price", "fpnl", "cost",
                     "available_now(avl_now)", "created_at", "updated_at", "recv_at"),
    "order_status.csv": ("account_id", "sid", "scan_name", "cl_ord_id", "order_id", "symbol", "order_type",
                         "order_business(order_biz)", "status", "ord_rej_reason(rej_reason)",
                         "ord_rej_reason_detail(rej_detail)", "price", "volume", "filled_volume(filled_vol)",
                         "created_at", "updated_at", "sent_at", "recv_at", "filled_vwap(filledvwap)", "filled_amount(filled_amt)"),
    "execution_report.csv": ("account_id", "sid", "scan_name", "cl_ord_id", "order_id", "exec_id", "symbol",
                             "order_business(order_biz)", "ord_rej_reason(rej_reason)",
                             "ord_rej_reason_detail(rej_detail)", "exec_type", "price", "volume",
                             "created_at", "recv_at", "amount"),
}
HEADERS["order_status_change.csv"] = HEADERS["order_status.csv"]
ALIASES = {name: name.split("(", 1)[0] for fields in HEADERS.values() for name in fields}
# The guide presents long_name(short_name); terminal exports must be inspected at admission.
for literal, canonical in list(ALIASES.items()):
    if "(" in literal:
        ALIASES[literal.split("(", 1)[1][:-1]] = canonical
        ALIASES[canonical] = canonical


class ProtocolError(ValueError):
    """A bounded public error code, without terminal paths or secret values."""


def identifier(value, *, account=False):
    pattern = r"[A-Za-z0-9][A-Za-z0-9_-]{0,63}" if account else r"[A-Za-z0-9][A-Za-z0-9_.:#-]{0,95}"
    if not isinstance(value, str) or not re.fullmatch(pattern, value):
        raise ProtocolError("INVALID_ACCOUNT" if account else "INVALID_IDENTITY")
    if account and value.upper() in {"CON", "PRN", "AUX", "NUL", *(f"COM{i}" for i in range(1, 10)), *(f"LPT{i}" for i in range(1, 10))}:
        raise ProtocolError("INVALID_ACCOUNT")
    return value


def decimal(value, *, nonnegative=False):
    if isinstance(value, bool) or value is None:
        raise ProtocolError("INVALID_NUMBER")
    try:
        result = Decimal(str(value))
    except (InvalidOperation, ValueError):
        raise ProtocolError("INVALID_NUMBER") from None
    if not result.is_finite() or abs(result) > Decimal("1e18") or (nonnegative and result < 0):
        raise ProtocolError("INVALID_NUMBER")
    return result


def integer(value, *, positive=False):
    number = decimal(value, nonnegative=True)
    if number != number.to_integral_value() or number > 1_000_000_000 or (positive and number <= 0):
        raise ProtocolError("INVALID_QUANTITY")
    return int(number)


def symbol(value):
    if not isinstance(value, str):
        raise ProtocolError("UNVERIFIED_SYMBOL")
    match = re.fullmatch(r"([0-9]{6})\.(SH|SZ)", value)
    if match:
        return ("SHSE" if match[2] == "SH" else "SZSE") + "." + match[1]
    if re.fullmatch(r"(SHSE|SZSE)\.[0-9]{6}", value):
        return value
    raise ProtocolError("UNVERIFIED_SYMBOL")


def order(value):
    if not isinstance(value, dict) or set(value) != {"sid", "symbol", "side", "price", "volume"}:
        raise ProtocolError("INVALID_ORDER_FIELDS")
    price = decimal(value["price"], nonnegative=True)
    if price <= 0 or price > 1_000_000 or price * 100 != (price * 100).to_integral_value():
        raise ProtocolError("INVALID_PRICE_TICK")
    if not isinstance(value["side"], str) or value["side"] not in {"buy", "sell"}:
        raise ProtocolError("INVALID_SIDE")
    return {"sid": identifier(value["sid"]), "symbol": symbol(value["symbol"]), "side": value["side"],
            "price": f"{price:.2f}", "volume": integer(value["volume"], positive=True)}


def encode_rows(header, rows, encoding="utf-8"):
    target = io.StringIO(newline="")
    writer = csv.writer(target, lineterminator="\r\n")
    writer.writerow(header)
    writer.writerows(rows)
    return target.getvalue().encode(encoding)


def order_csv(account_id, value):
    return encode_rows(ORDER_HEADER, [[value["sid"], account_id, value["symbol"], value["volume"], 1,
                                      1 if value["side"] == "buy" else 2, value["price"], ""]])
