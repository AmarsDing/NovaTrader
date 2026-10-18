from __future__ import annotations

import importlib.metadata
import io
import json
import math
import os
import re
import struct
import urllib.error
import urllib.request
import zipfile
from datetime import date, datetime, timezone

TDX_URL = "https://www.tdx.com.cn/products/data/data/g4day/{ymd}.zip"
OPENBB_VERSIONS = {"openbb-core": "2.0.0", "openbb-fred": "2.0.0"}


class SourceError(Exception):
    def __init__(self, code: str, reason: str):
        super().__init__(reason)
        self.code = code


def requested_day(value: str) -> date:
    try:
        d = date.fromisoformat(value)
    except (ValueError, TypeError) as exc:
        raise SourceError("invalid_request", "date must be YYYY-MM-DD") from exc
    if d > datetime.now(timezone.utc).date():
        raise SourceError("invalid_request", "future source dates are unavailable")
    return d


def download(url: str) -> tuple[bytes, dict]:
    # URLs are constructed by fixed adapters, never supplied by a job or remote SKILL.
    request = urllib.request.Request(url, headers={"User-Agent": "NovaTrader-source-poc/1"})
    try:
        with urllib.request.urlopen(request, timeout=20) as response:
            data = response.read(8 * 1024 * 1024 + 1)
            if len(data) > 8 * 1024 * 1024:
                raise SourceError("format_changed", "source body exceeds 8MiB bound")
            return data, {"content_type": response.headers.get("Content-Type"),
                          "last_modified_hint": response.headers.get("Last-Modified")}
    except urllib.error.HTTPError as exc:
        if exc.code == 429:
            raise SourceError("rate_limited", "provider returned HTTP 429") from exc
        if exc.code == 404:
            raise SourceError("not_published", "archive absent: not retained, holiday, or not published") from exc
        raise SourceError("unavailable", f"provider returned HTTP {exc.code}") from exc
    except (urllib.error.URLError, TimeoutError, OSError) as exc:
        raise SourceError("unavailable", "provider connection or timeout failure") from exc


def is_a_stock(code: str, market: str) -> bool:
    return ((market == "sh" and code.startswith(("60", "68"))) or
            (market == "sz" and code.startswith(("00", "30"))) or
            (market == "bj" and code.startswith(("4", "8", "92"))))


def parse_tdx(raw: bytes, trade_date: str) -> tuple[list[dict], dict]:
    """Independent implementation of the documented COD/MD1 format; does not execute upstream code."""
    d = requested_day(trade_date)
    ymd = d.strftime("%Y%m%d")
    rows, counts = [], {}
    try:
        with zipfile.ZipFile(io.BytesIO(raw)) as archive:
            names = archive.namelist()
            if len(names) != len(set(names)):
                raise SourceError("format_changed", "duplicate ZIP member names")
            if sum(i.file_size for i in archive.infolist()) > 100 * 1024 * 1024:
                raise SourceError("format_changed", "uncompressed package exceeds 100MiB")
            for market in ("sh", "sz", "bj"):
                code_name, price_name = f"{market}{ymd[2:]}.cod", f"{market}{ymd[2:]}.md1"
                if market == "bj" and ymd < "20220506" and code_name not in names and price_name not in names:
                    counts[market] = {"status": "unsupported_before_2022_05_06", "a_stock_count": None}
                    continue
                if code_name not in names or price_name not in names:
                    raise SourceError("format_changed", f"missing {market} code/price pair")
                codes, prices = archive.read(code_name), archive.read(price_name)
                if len(codes) % 150 or len(prices) % 512 or len(codes) // 150 != len(prices) // 512:
                    raise SourceError("format_changed", f"{market} truncated or unmatched blocks")
                seen_codes, seen_blocks = set(), set()
                start = len(rows)
                no_trade_rows = 0
                for offset in range(0, len(codes), 150):
                    entry = codes[offset:offset + 150]
                    try:
                        code = entry[:6].rstrip(b"\0 ").decode("ascii")
                        name = entry[40:72].split(b"\0")[0].decode("gbk").strip()
                    except UnicodeDecodeError as exc:
                        raise SourceError("format_changed", "invalid code/name encoding") from exc
                    block_id = struct.unpack_from("<H", entry, 32)[0]
                    if not re.fullmatch(r"\d{6}", code) or code in seen_codes or block_id in seen_blocks:
                        raise SourceError("format_changed", "duplicate/invalid security or price block identity")
                    seen_codes.add(code)
                    seen_blocks.add(block_id)
                    if block_id >= len(prices) // 512:
                        raise SourceError("format_changed", "price block out of bounds")
                    block = memoryview(prices)[block_id * 512:(block_id + 1) * 512]
                    pre_close = struct.unpack_from("<d", block, 4)[0]
                    opening, high, low, close = struct.unpack_from("<4d", block, 12)
                    volume = struct.unpack_from("<Q", block, 56)[0]
                    amount = struct.unpack_from("<d", block, 72)[0]
                    if not all(math.isfinite(v) for v in (pre_close, opening, high, low, close, amount)):
                        raise SourceError("format_changed", "non-finite provider number")
                    if not is_a_stock(code, market):
                        continue
                    if close <= 0:
                        if volume == 0 and min(opening, high, low) <= 0:
                            no_trade_rows += 1
                            continue
                        raise SourceError("format_changed", f"missing closing OHLC for traded equity {code}.{market.upper()}")
                    if volume == 0 and min(opening, high, low) <= 0:
                        no_trade_rows += 1
                        continue  # No-trade/suspended security: a close-only row is not an executable OHLC bar.
                    opening, high, low, close, pre_close = (round(v, 4) for v in (opening, high, low, close, pre_close))
                    if not name or min(opening, high, low) <= 0 or high < max(opening, close) or low > min(opening, close) or amount < 0:
                        raise SourceError("format_changed", f"invalid equity OHLC/name/amount for {code}.{market.upper()}")
                    rows.append({"symbol": f"{code}.{market.upper()}", "name": name,
                                 "time": f"{d.isoformat()}T00:00:00+08:00", "freq": "1d",
                                 "open": opening, "high": high, "low": low, "close": close,
                                 "pre_close": pre_close, "volume": volume, "amount": amount,
                                 "available_at": None, "availability": "unknown",
                                 "availability_evidence": "package does not prove historical publication time"})
                counts[market] = {"status": "observed", "a_stock_count": len(rows) - start,
                                  "excluded_no_trade_ohlc_rows": no_trade_rows}
                if counts[market]["a_stock_count"] == 0:
                    raise SourceError("empty_result", f"{market} archive has no priced A shares")
    except SourceError:
        raise
    except (zipfile.BadZipFile, EOFError, struct.error, RuntimeError) as exc:
        raise SourceError("format_changed", "invalid or truncated ZIP/COD/MD1") from exc
    rows.sort(key=lambda r: r["symbol"])
    return rows, {"scope": "priced_a_share_rows_only", "markets": counts,
                  "coverage_ratio": None, "coverage_reason": "no authoritative historical denominator supplied"}


def tdx_history(trade_date: str, fetch=download) -> tuple[list[dict], dict, bytes]:
    d = requested_day(trade_date)
    url = TDX_URL.format(ymd=d.strftime("%Y%m%d"))
    raw, headers = fetch(url)
    rows, coverage = parse_tdx(raw, trade_date)
    metadata = {"provider": "tdx.com.cn", "failure_domain": "tdx_official_http",
                "implementation": "a_stock_data_documented_format/3.10.2-novatrader-1",
                "source_url": url, "dataset": "unadjusted_a_share_daily_bar",
                "units": {"price": "CNY", "volume": "shares", "amount": "CNY"},
                "delay_kind": "after_close_archive", "available_at": None,
                "training_ready": False, "coverage": coverage, "response_headers": headers,
                "raw_kind": "provider_http_zip_bytes"}
    return rows, metadata, raw


def normalize_fred(result: dict, series: str) -> tuple[list[dict], dict]:
    if not isinstance(result, dict):
        raise SourceError("format_changed", "OpenBB result must be an object")
    data = result.get("results")
    if not isinstance(data, list):
        raise SourceError("format_changed", "OpenBB results must be a list")
    metadata = result.get("extra", {}).get("metadata", {}) if isinstance(result.get("extra"), dict) else {}
    series_meta = metadata.get(series, {}) if isinstance(metadata, dict) else {}
    unit = series_meta.get("units") if isinstance(series_meta, dict) else None
    rows, seen = [], set()
    for r in data:
        if not isinstance(r, dict) or "date" not in r or series not in r:
            raise SourceError("format_changed", "OpenBB FRED date/series field changed")
        d = requested_day(r["date"])
        if d in seen:
            raise SourceError("format_changed", "duplicate macro observation")
        seen.add(d)
        value = r[series]
        if value is None or value == ".":
            continue
        if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value):
            raise SourceError("format_changed", "non-numeric macro observation")
        rows.append({"code": series, "date": d.isoformat(), "value": value, "unit": unit,
                     "available_at": None, "availability": "unknown",
                     "availability_evidence": "series result does not retain ALFRED publication vintages"})
    if not rows:
        raise SourceError("empty_result", "no non-missing macro observations")
    rows.sort(key=lambda r: r["date"])
    return rows, {"provider": "fred.stlouisfed.org", "failure_domain": "st_louis_fed_fred",
                  "implementation": "openbb-fred/2.0.0", "dataset": "fred_series_current_vintage",
                  "series": series, "unit": unit, "unit_status": "known" if unit else "unknown",
                  "delay_kind": "provider_release_schedule", "available_at": None,
                  "training_ready": False, "raw_kind": "sdk_normalized_result_json"}


def openbb_macro(series: str, start: str, end: str) -> tuple[list[dict], dict, bytes]:
    if series not in {"CPIAUCSL", "FEDFUNDS", "UNRATE"}:
        raise SourceError("invalid_request", "series outside reviewed POC whitelist")
    lo, hi = requested_day(start), requested_day(end)
    if hi < lo:
        raise SourceError("invalid_request", "macro range reversed")
    if not os.environ.get("FRED_API_KEY"):
        raise SourceError("unavailable", "server-side FRED_API_KEY is not configured")
    for package, expected in OPENBB_VERSIONS.items():
        try:
            actual = importlib.metadata.version(package)
        except importlib.metadata.PackageNotFoundError as exc:
            raise SourceError("unavailable", f"isolated optional dependency {package} is not installed") from exc
        if actual != expected:
            raise SourceError("unavailable", f"{package} version differs from reviewed lock")
    try:
        from openbb import obb
        obb.user.credentials.fred_api_key = os.environ["FRED_API_KEY"]
        result = obb.fred.economy.fred_series(symbol=series, start_date=lo.isoformat(),
                                              end_date=hi.isoformat(), use_cache=False)
        raw_result = result.model_dump(mode="json")
    except Exception as exc:
        # Exception text may contain a provider URL/key; only its type is returned.
        raise SourceError("unavailable", f"OpenBB provider call failed ({type(exc).__name__})") from exc
    rows, metadata = normalize_fred(raw_result, series)
    if any(not lo.isoformat() <= r["date"] <= hi.isoformat() for r in rows):
        raise SourceError("format_changed", "provider returned observation outside requested range")
    raw = json.dumps(raw_result, ensure_ascii=False, sort_keys=True, allow_nan=False).encode()
    return rows, metadata, raw
