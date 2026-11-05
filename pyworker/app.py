"""通达信 / AkShare HTTP 边车。解析只放在这里，datahub 不读 vipdoc 二进制。"""
from __future__ import annotations

import json
import logging
import os
import struct
import threading
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, urlparse

SHANGHAI = timezone(timedelta(hours=8))
TDX_DIR = Path(os.environ.get("TDX_DIR", r"D:\new_tdx"))
logger = logging.getLogger("pyworker")
ROLES = {x.strip() for x in os.environ.get("PYWORKER_ROLES", "local,file,tcp,akshare,baostock").split(",") if x.strip()}
LISTEN = os.environ.get("PYWORKER_LISTEN", "127.0.0.1:18080")

MARKETS = {"sh": "SH", "sz": "SZ", "bj": "BJ"}


def iso(dt: datetime) -> str:
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=SHANGHAI)
    return dt.isoformat()


def json_bytes(obj) -> bytes:
    return json.dumps(obj, ensure_ascii=False, default=str).encode("utf-8")


def unavailable(reason: str):
    return 503, {"error": "unavailable", "reason": reason}


def ymd_str(value) -> str:
    """Normalize any date-like value to YYYY-MM-DD for datahub Parse."""
    if value is None:
        return datetime.now(SHANGHAI).strftime("%Y-%m-%d")
    if hasattr(value, "strftime"):
        try:
            return value.strftime("%Y-%m-%d")
        except Exception:
            pass
    s = str(value).strip()
    if not s:
        return datetime.now(SHANGHAI).strftime("%Y-%m-%d")
    # "2024-10-28 00:00:00" / "2024/10/28" / "20241028"
    for sep in (" ", "T"):
        if sep in s:
            s = s.split(sep, 1)[0]
    s = s.replace("/", "-")
    if len(s) == 8 and s.isdigit():
        return f"{s[:4]}-{s[4:6]}-{s[6:8]}"
    if len(s) >= 10 and s[4] == "-" and s[7] == "-":
        return s[:10]
    return datetime.now(SHANGHAI).strftime("%Y-%m-%d")


def parse_symbol_file(name: str) -> str | None:
    stem = Path(name).stem.lower()
    for prefix, mkt in MARKETS.items():
        if stem.startswith(prefix) and stem[len(prefix) :].isdigit():
            return f"{stem[len(prefix):].zfill(6)}.{mkt}"
    return None


def code_to_file(symbol: str) -> tuple[str, str] | None:
    s = symbol.strip().upper()
    if "." not in s:
        return None
    code, mkt = s.split(".", 1)
    mkt = mkt.lower()
    if mkt not in MARKETS or len(code) != 6:
        return None
    return mkt, f"{mkt}{code}"


# --- vipdoc -----------------------------------------------------------------

DAY_STRUCT = struct.Struct("<IIIIIfII")


def read_day_file(path: Path, start: str, end: str) -> list[dict]:
    out = []
    data = path.read_bytes()
    symbol = parse_symbol_file(path.name)
    if not symbol:
        return out
    for i in range(0, len(data) - 31, 32):
        date, o, h, l, c, amount, vol, _ = DAY_STRUCT.unpack_from(data, i)
        ymd = f"{date:08d}"
        if start and ymd < start.replace("-", ""):
            continue
        if end and ymd > end.replace("-", ""):
            continue
        try:
            t = datetime.strptime(ymd, "%Y%m%d").replace(tzinfo=SHANGHAI)
        except ValueError:
            continue
        out.append(
            {
                "symbol": symbol,
                "time": iso(t),
                "freq": "1d",
                "open": o / 100.0,
                "high": h / 100.0,
                "low": l / 100.0,
                "close": c / 100.0,
                "volume": int(vol) * 100,
                "amount": float(amount),
            }
        )
    return out


LC_STRUCT = struct.Struct("<HHffffI")


def unpack_lc_date(zip_day: int, minutes: int) -> datetime | None:
    month = int((zip_day % 2048) / 100)
    year = (zip_day >> 11) + 2004
    day = (zip_day % 2048) % 100
    hour = minutes // 60
    minute = minutes % 60
    try:
        end = datetime(year, month, day, hour, minute, tzinfo=SHANGHAI)
    except ValueError:
        return None
    return end - timedelta(minutes=1)


def read_lc_file(path: Path, freq: str, start: str, end: str) -> list[dict]:
    out = []
    data = path.read_bytes()
    symbol = parse_symbol_file(path.name)
    if not symbol:
        return out
    start_d = start.replace("-", "")
    end_d = end.replace("-", "")
    rec = 32
    for i in range(0, len(data) - rec + 1, rec):
        zip_day, minutes, o, h, l, c = struct.unpack_from("<HHffff", data, i)
        vol = struct.unpack_from("<I", data, i + 24)[0]
        t = unpack_lc_date(zip_day, minutes)
        if t is None:
            continue
        ymd = t.strftime("%Y%m%d")
        if start_d and ymd < start_d:
            continue
        if end_d and ymd > end_d:
            continue
        out.append(
            {
                "symbol": symbol,
                "time": iso(t),
                "freq": freq,
                "open": float(o),
                "high": float(h),
                "low": float(l),
                "close": float(c),
                "volume": int(vol) * 100,
                "amount": 0.0,
            }
        )
    return out


def vipdoc_last_date() -> str:
    latest = ""
    for mkt in MARKETS:
        folder = TDX_DIR / "vipdoc" / mkt / "lday"
        if not folder.is_dir():
            continue
        for p in folder.glob("*.day"):
            try:
                size = p.stat().st_size
            except OSError:
                continue
            if size < 32:
                continue
            with p.open("rb") as f:
                f.seek(-32, os.SEEK_END)
                date = struct.unpack("<I", f.read(4))[0]
            ymd = f"{date:08d}"
            if ymd > latest:
                latest = ymd
            break
    if not latest:
        return ""
    return f"{latest[:4]}-{latest[4:6]}-{latest[6:8]}"


def list_day_files() -> list[Path]:
    files = []
    for mkt in MARKETS:
        folder = TDX_DIR / "vipdoc" / mkt / "lday"
        if folder.is_dir():
            files.extend(folder.glob("*.day"))
    return files


def file_bars(body: dict):
    freq = body.get("freq") or "1d"
    start = body.get("start") or ""
    end = body.get("end") or start
    expect = body.get("expect") or ""
    symbols = body.get("symbols") or []
    last = vipdoc_last_date()
    stale = bool(expect) and last < expect
    bars: list[dict] = []
    if freq == "1d":
        files = list_day_files()
        want = None
        if symbols:
            want = set()
            for s in symbols:
                pair = code_to_file(s)
                if pair:
                    want.add(pair[1])
        for p in files:
            if want is not None and p.stem.lower() not in want:
                continue
            bars.extend(read_day_file(p, start, end))
    else:
        suffix = "lc5" if freq == "5m" else "lc1"
        subdir = "fzline" if freq == "5m" else "minline"
        targets = symbols
        if not targets:
            return 400, {"error": "minute bars need symbols, or download full market first"}
        for s in targets:
            pair = code_to_file(s)
            if not pair:
                continue
            mkt, stem = pair
            path = TDX_DIR / "vipdoc" / mkt / subdir / f"{stem}.{suffix}"
            if not path.exists():
                alt = TDX_DIR / "vipdoc" / mkt / subdir / f"{stem}.{suffix[-1]}"
                path = alt if alt.exists() else path
            if path.exists():
                bars.extend(read_lc_file(path, freq, start, end))
    return 200, {"bars": bars, "last_date": last, "stale": stale}


def _read_gbk(path: Path) -> str:
    return path.read_text(encoding="gbk", errors="ignore")


def file_sectors():
    sectors = []
    names = {}
    incon = TDX_DIR / "incon.dat"
    if incon.exists():
        for line in _read_gbk(incon).splitlines():
            if "|" in line and not line.startswith("#"):
                code, name = line.split("|", 1)
                names[code.strip()] = name.strip()
    hy = TDX_DIR / "T0002" / "hq_cache" / "tdxhy.cfg"
    if hy.exists():
        groups: dict[str, list[str]] = {}
        for line in _read_gbk(hy).splitlines():
            parts = line.strip().split("|")
            if len(parts) < 3 or not parts[1].isdigit():
                continue
            mkt = {"0": "SZ", "1": "SH", "2": "BJ"}.get(parts[0], "SZ")
            code = parts[2].strip()
            if not code:
                continue
            groups.setdefault(code, []).append(f"{parts[1].zfill(6)}.{mkt}")
        for code, members in groups.items():
            sectors.append(
                {"code": code, "name": names.get(code, code), "kind": "industry", "members": members}
            )
    root = TDX_DIR / "T0002" / "blocknew"
    if root.is_dir():
        for p in root.glob("*.blk"):
            text = _read_gbk(p)
            members = []
            for line in text.splitlines():
                line = line.strip()
                if not line or line.startswith("#"):
                    continue
                if line[0] in "012" and len(line) >= 7 and line[1:7].isdigit():
                    market = {"1": "SH", "0": "SZ", "2": "BJ"}.get(line[0], "SZ")
                    members.append(f"{line[1:7]}.{market}")
            if members:
                sectors.append({"code": p.stem, "name": p.stem, "kind": "style", "members": members})
    if not sectors:
        return unavailable("没有板块文件，请在通达信里下载板块或盘后数据")
    return 200, {"sectors": sectors}


# --- 7709 -------------------------------------------------------------------

_tcp = None


def tcp_client():
    global _tcp
    if _tcp is not None:
        return _tcp
    from mootdx.quotes import Quotes

    _tcp = Quotes.factory(market="std", bestip=True, timeout=8)
    return _tcp


def to_symbol(code: str, market: int) -> str:
    code = str(code).zfill(6)
    if market == 1:
        return code + ".SH"
    if str(code).startswith("8") or str(code).startswith("4") or str(code).startswith("9"):
        return code + ".BJ"
    return code + ".SZ"


def tcp_quote_time(row, now: datetime) -> datetime | None:
    """保留源日期/时间；缺失或无法解析时保持未知，不能用请求时间冒充行情时间。"""
    value = row.get("servertime", "") or row.get("time", "")
    if isinstance(value, datetime):
        return value.replace(tzinfo=SHANGHAI) if value.tzinfo is None else value
    text = str(value or "").strip()
    if not text:
        return None
    try:
        # 完整日期必须保留，不能把前一交易日的时间移到今天。
        if len(text) >= 10 and text[4] == "-" and text[7] == "-":
            parsed = datetime.fromisoformat(text)
            return parsed.replace(tzinfo=SHANGHAI) if parsed.tzinfo is None else parsed
        # mootdx 的时间字段常只有 HH:MM:SS，沿用当日上海日期。
        return datetime.strptime(text[:8], "%H:%M:%S").replace(
            year=now.year, month=now.month, day=now.day, tzinfo=SHANGHAI
        )
    except (TypeError, ValueError):
        return None


def tcp_snapshots(symbols: list[str]):
    client = tcp_client()
    codes = []
    if symbols:
        codes = [s.split(".")[0] for s in symbols if "." in s]
    else:
        # 全市场走证券列表再分批。
        secs = tcp_securities_items()
        codes = [x["symbol"].split(".")[0] for x in secs]
    items = []
    for i in range(0, len(codes), 80):
        df = client.quotes(symbol=codes[i : i + 80])
        if df is None or getattr(df, "empty", True):
            continue
        for _, row in df.iterrows():
            code = str(row.get("code", row.get("symbol", "")))
            market = int(row.get("market", 1 if code.startswith("6") else 0))
            last = float(row.get("price", 0) or 0)
            if last <= 0:
                continue
            t = tcp_quote_time(row, datetime.now(SHANGHAI))
            vol = int(float(row.get("vol", 0) or 0)) * 100
            items.append(
                {
                    "symbol": to_symbol(code, market),
                    "time": iso(t) if t is not None else None,
                    "stale": t is None,
                    "pre_close": float(row.get("last_close", 0) or 0),
                    "open": float(row.get("open", 0) or 0),
                    "high": float(row.get("high", 0) or 0),
                    "low": float(row.get("low", 0) or 0),
                    "last": last,
                    "volume": vol,
                    "amount": float(row.get("amount", 0) or 0),
                    "bid": [
                        [float(row.get(f"bid{j}", 0) or 0), float(row.get(f"bid_vol{j}", 0) or 0) * 100]
                        for j in range(1, 6)
                    ],
                    "ask": [
                        [float(row.get(f"ask{j}", 0) or 0), float(row.get(f"ask_vol{j}", 0) or 0) * 100]
                        for j in range(1, 6)
                    ],
                }
            )
    return 200, {"items": items}


def _tcp_bj_items(client) -> list[dict]:
    """BJ 证券枚举（gap⑤/S10）：绕 mootdx 高层包装走 pytdx 原生接口。

    已核实本机 mootdx 0.11.7：Stocks.stocks(market) 对 market=2 直接 raise
    MootdxValidationException（只放行 0/1），而 stock_count() 放行 0/1/2、
    底层 client.get_security_list(market=2, start=) 协议层支持 BJ——故按
    stock_count 真分页整取。失败分级钉死：计数失败（或整表为空）→ 返回空、
    调用侧标 skipped_markets=["BJ"]，绝不拿空表冒充「BJ 无证券」；
    中间断页 → 保留已取前缀、仅告警（部分枚举优于整体缺失，不标 skipped）。
    注：920 开头的新号段在 mootdx get_stock_market 里会被推断成 SH，
    这批代码的 quote/bars 会静默缺数（不会张冠李戴：SH 无同码证券）；
    待 mootdx 上游支持前按已知边界接受，枚举本身不受影响。
    """
    items: list[dict] = []
    try:
        count = int(client.stock_count(market=2) or 0)
    except Exception as e:
        logger.warning("tdx_tcp BJ 证券计数失败，BJ 市场本轮整体跳过（显式缺失）：%s", e)
        return items
    for start in range(0, count, 1000):
        try:
            rows = client.client.get_security_list(market=2, start=start)
        except Exception as e:
            logger.warning("tdx_tcp BJ 证券分页中断 start=%d，保留已取 %d 只：%s", start, len(items), e)
            break
        if not rows:
            # 中间页为空即停。start < count 恒成立（计数承诺仍有券），这里空页不是
            # 「正常翻到底」而是真实 pytdx 的失败形态——静默返空不抛错（W3 复核第⑧条）。
            # 不告警就和「BJ 恰好只有这么几只」无法区分，必须显式留痕；
            # 仍按部分枚举保留已取前缀，不标 skipped（部分覆盖优于整体缺失）。
            logger.warning(
                "tdx_tcp BJ 证券分页中途空页 start=%d（计数承诺 %d 只），保留已取 %d 只",
                start, count, len(items),
            )
            break
        for row in rows:
            code = str(row.get("code", "")).zfill(6)
            name = str(row.get("name", "") or row.get("code", ""))
            items.append({"symbol": f"{code}.BJ", "name": name, "market": "BJ"})
    return items


def tcp_securities_items() -> list[dict]:
    client = tcp_client()
    items = []
    # SH/SZ：已核实 mootdx 0.11.7 stocks() 内部按 stock_count 全量翻页
    # （range(0, counts, 1000) 循环 get_security_list），一次调用拿整市场；
    # 旧版这里套「while True 单页即 break」死壳，易让人误以为各地仍要手动翻页。
    for market, suffix in ((1, "SH"), (0, "SZ")):
        df = client.stocks(market=market)
        if df is None or getattr(df, "empty", True):
            continue
        for _, row in df.iterrows():
            code = str(row.get("code", "")).zfill(6)
            name = str(row.get("name", "") or row.get("code", ""))
            items.append({"symbol": f"{code}.{suffix}", "name": name, "market": suffix})
    items.extend(_tcp_bj_items(client))
    return items


def tcp_securities():
    items = tcp_securities_items()
    if len(items) < 100:
        return unavailable("7709 证券列表过短，主站可能不可用")
    bj = sum(1 for x in items if x["market"] == "BJ")
    body = {"items": items}
    if bj == 0:
        # BJ 整市场本轮未取到：显式标 skipped_markets，不让「全市场」清单静默缺一个市场。
        # Go 消费端（datahub pyworker.go）只解 items 键，新增字段向后兼容。
        logger.warning("tdx_tcp 证券枚举：BJ 市场本轮无数据（计数失败或服务器不支持），skipped_markets 已显式标注")
        body["skipped_markets"] = ["BJ"]
    return 200, body


def tcp_bars(body: dict):
    client = tcp_client()
    freq = body.get("freq") or "1d"
    start = body.get("start") or ""
    end = body.get("end") or start
    symbols = body.get("symbols") or []
    if not symbols:
        symbols = [x["symbol"] for x in tcp_securities_items()]
    frequency = 9 if freq == "1d" else (0 if freq == "1m" else 1)
    bars = []
    for s in symbols:
        code = s.split(".")[0]
        df = client.bars(symbol=code, frequency=frequency, offset=800)
        if df is None or getattr(df, "empty", True):
            continue
        for _, row in df.iterrows():
            dt = row.get("datetime") or row.get("date")
            if hasattr(dt, "to_pydatetime"):
                t = dt.to_pydatetime()
            else:
                t = datetime.fromisoformat(str(dt)[:19])
            if t.tzinfo is None:
                t = t.replace(tzinfo=SHANGHAI)
            if freq != "1d":
                t = t - timedelta(minutes=1)
            ymd = t.strftime("%Y-%m-%d")
            if start and ymd < start:
                continue
            if end and ymd > end:
                continue
            bars.append(
                {
                    "symbol": s,
                    "time": iso(t.replace(hour=0, minute=0, second=0) if freq == "1d" else t),
                    "freq": freq,
                    "open": float(row.get("open", 0) or 0),
                    "high": float(row.get("high", 0) or 0),
                    "low": float(row.get("low", 0) or 0),
                    "close": float(row.get("close", 0) or 0),
                    "volume": int(float(row.get("vol", 0) or 0)) * 100,
                    "amount": float(row.get("amount", 0) or 0),
                }
            )
    last = max((b["time"][:10] for b in bars), default="")
    expect = body.get("expect") or ""
    return 200, {"bars": bars, "last_date": last, "stale": bool(expect) and last < expect}


def tcp_adj(body: dict):
    client = tcp_client()
    day = body.get("date") or ""
    symbols = body.get("symbols") or []
    items = []
    for s in symbols:
        code = s.split(".")[0]
        df = client.xdxr(symbol=code)
        if df is None or getattr(df, "empty", True):
            continue
        factor = 1.0
        for _, row in df.iterrows():
            dt = str(row.get("date", row.get("datetime", "")))[:10]
            if day and dt > day:
                break
            peigu = float(row.get("peigu", 0) or 0)
            songgu = float(row.get("songzhuangu", 0) or row.get("songgu", 0) or 0)
            if peigu or songgu:
                factor *= (10 + songgu + peigu) / 10.0
        items.append({"symbol": s, "date": day, "factor": factor})
    return 200, {"items": items}


# --- 官方本地接口 -----------------------------------------------------------

def local_reason() -> str:
    plugin = TDX_DIR / "PYPlugins"
    if not plugin.is_dir():
        return "安装目录没有 PYPlugins，正式版通达信没有官方 Python 接口"
    return "未连上已打开的通达信客户端"


def local_snapshot(_body):
    return unavailable(local_reason())


def local_bars(_body):
    return unavailable(local_reason())


def local_securities():
    return unavailable(local_reason())


# --- AkShare ----------------------------------------------------------------


def pct_chg_from_closes(closes) -> float | None:
    """两根收盘实算涨跌幅 %，closes 按时间升序（最新在最后）。

    数据纪律：样本不足两根或基准收盘非正 → None（显式缺失），绝不返回 0 冒充。
    """
    vals = [float(c) for c in closes if c is not None]
    if len(vals) < 2:
        return None
    prev, cur = vals[-2], vals[-1]
    if prev <= 0:
        return None
    return (cur - prev) / prev * 100.0


def usdcny_pct_chg(ak) -> float | None:
    """美元兑人民币涨跌幅：currency_hist 最近两根收盘实算；拿不到返回 None。"""
    try:
        end = datetime.now(SHANGHAI).strftime("%Y%m%d")
        start = (datetime.now(SHANGHAI) - timedelta(days=14)).strftime("%Y%m%d")
        df = ak.currency_hist(symbol="usd-cny", start_date=start, end_date=end)
        if df is None or getattr(df, "empty", True) or "收盘" not in df.columns:
            return None
        return pct_chg_from_closes(df["收盘"].dropna().tolist())
    except Exception:
        return None


def ak_global():
    """外围行情：东财全球指数 hist（中文名）+ 汇率/SOX。

    44 实测：spot/美股/外盘期货易 EOF；index_global_hist_em('道琼斯') 等可用，
    但需限速；取满 3 条即停（对齐 datahub CollectOverseas 门槛）。
    """
    import time
    import akshare as ak

    items = []

    def add(code: str, name: str, trade_date, close, pct) -> None:
        if any(x["code"] == code for x in items):
            return
        try:
            c = float(close or 0)
        except (TypeError, ValueError):
            return
        if c == 0:
            return
        item = {"code": code, "name": name, "trade_date": ymd_str(trade_date), "close": c}
        if pct not in (None, ""):
            try:
                v = float(pct)
                if v == v:  # not NaN
                    item["pct_chg"] = v
            except (TypeError, ValueError):
                pass
        items.append(item)

    def pct_from_closes(closes: list) -> float | None:
        return pct_chg_from_closes(closes)

    # 1) 东财全球指数历史末行（中文 symbol；限速 + 重试）
    hist_symbols = (
        ("美元指数", "UDI"),
        ("道琼斯", "DJI"),
        ("标普500", "SPX"),
        ("纳斯达克", "IXIC"),
        ("恒生指数", "HSI"),
    )
    for sym_name, code in hist_symbols:
        if len(items) >= 3:
            break
        ok = False
        for attempt in range(3):
            try:
                if attempt or items:
                    time.sleep(1.5 + attempt)
                df = ak.index_global_hist_em(symbol=sym_name)
                if df is None or getattr(df, "empty", True):
                    break
                row = df.iloc[-1]
                closes = []
                if "最新价" in df.columns:
                    closes = [float(x) for x in df["最新价"].dropna().tolist()[-3:]]
                pct = pct_from_closes(closes) if len(closes) >= 2 else None
                add(code, str(row.get("名称", sym_name)), row.get("日期", datetime.now(SHANGHAI).date()), row.get("最新价"), pct)
                ok = True
                break
            except Exception as e:
                logger.warning("ak_global hist %s attempt=%d: %s", sym_name, attempt + 1, e)
                time.sleep(3.0 * (attempt + 1))
        if not ok:
            logger.warning("ak_global hist %s gave up", sym_name)

    # 2) 快照兜底（偶发可用，失败忽略）
    if len(items) < 3:
        try:
            df = ak.index_global_spot_em()
            if df is not None and not df.empty:
                want = {"道琼斯": "DJI", "标普500": "SPX", "纳斯达克": "IXIC", "恒生": "HSI", "日经": "N225", "美元指数": "UDI"}
                for _, row in df.iterrows():
                    name = str(row.get("名称", ""))
                    for key, code in want.items():
                        if key in name:
                            add(code, name, row.get("最新行情时间", datetime.now(SHANGHAI)), row.get("最新价"), row.get("涨跌幅"))
                    if len(items) >= 5:
                        break
        except Exception as e:
            logger.warning("ak_global index_global_spot_em: %s", e)

    # 3) 美元兑人民币（仅不足 3 条时补；中行序列若过旧则丢弃）
    if len(items) < 3 and not any(x["code"] == "USDCNH" for x in items):
        try:
            try:
                df = ak.currency_boc_sina(symbol="美元")
            except TypeError:
                df = ak.currency_boc_sina()
            if df is not None and not df.empty:
                row = df.iloc[-1]
                mid = row.get("央行中间价", row.get("中行折算价"))
                try:
                    mid_f = float(mid)
                    close = mid_f / 100.0 if mid_f > 20 else mid_f
                except (TypeError, ValueError):
                    close = None
                day = ymd_str(row.get("日期", datetime.now(SHANGHAI).date()))
                # 丢弃明显陈旧的中间价（44 上无参序列曾停在 2023）
                if day >= (datetime.now(SHANGHAI) - timedelta(days=14)).strftime("%Y-%m-%d"):
                    pct = usdcny_pct_chg(ak)
                    if pct is None:
                        logger.warning("ak_global: USDCNH pct_chg 上一收盘不可得，载荷缺键（T12c 后落 NULL，不再冒充 0）")
                    add("USDCNH", "美元兑人民币", day, close, pct)
                else:
                    logger.warning("ak_global USDCNH stale date=%s skipped", day)
        except Exception as e:
            logger.warning("ak_global USDCNH: %s", e)

    # 4) SOX 补齐（重；仅不足 3 条时）
    if len(items) < 3:
        try:
            df = ak.macro_global_sox_index()
            if df is not None and not df.empty:
                row = df.iloc[-1]
                add("SOX", "费城半导体指数", row.get("日期", datetime.now(SHANGHAI).date()), row.get("最新值"), row.get("涨跌幅"))
        except Exception as e:
            logger.warning("ak_global SOX: %s", e)

    if len(items) < 1:
        return unavailable("AkShare 外围接口不可用")
    return 200, {"items": items}


def ak_macro():
    import akshare as ak

    items = []
    fetchers = {
        "CPI_YOY": lambda: ak.macro_china_cpi_yearly(),
        "PPI_YOY": lambda: ak.macro_china_ppi_yearly(),
        "PMI_MFG": lambda: ak.macro_china_pmi(),
    }
    names = {"CPI_YOY": "CPI同比", "PPI_YOY": "PPI同比", "PMI_MFG": "制造业PMI"}
    units = {"CPI_YOY": "%", "PPI_YOY": "%", "PMI_MFG": ""}
    for code, fn in fetchers.items():
        try:
            df = fn()
        except Exception as e:
            logger.warning("ak_macro %s: %s", code, e)
            continue
        if df is None or getattr(df, "empty", True):
            logger.warning("ak_macro %s: empty", code)
            continue
        row = df.iloc[-1]
        date_col = df.columns[0]
        val_col = df.columns[-1]
        try:
            value = float(row[val_col] or 0)
        except (TypeError, ValueError):
            logger.warning("ak_macro %s: bad value %r", code, row[val_col])
            continue
        items.append(
            {
                "code": code,
                "name": names[code],
                "date": ymd_str(row[date_col]),
                "value": value,
                "unit": units[code],
            }
        )
    if not items:
        return unavailable("AkShare 宏观接口不可用")
    return 200, {"items": items}


def ak_limit_pool(qs: dict):
    import akshare as ak

    date = (qs.get("date") or [datetime.now(SHANGHAI).strftime("%Y-%m-%d")])[0].replace("-", "")
    pool = (qs.get("pool") or ["up"])[0]
    fn = {
        "up": ak.stock_zt_pool_em,
        "down": ak.stock_zt_pool_dtgc_em,
        "broken": ak.stock_zt_pool_zbgc_em,
    }.get(pool)
    if fn is None:
        return 400, {"error": "unknown pool"}
    try:
        df = fn(date=date)
    except TypeError:
        df = fn()
    except Exception as e:
        return unavailable(str(e))
    items = []
    if df is None or getattr(df, "empty", True):
        return 200, {"items": []}
    for _, row in df.iterrows():
        code = str(row.get("代码", "")).zfill(6)
        if not code.isdigit():
            continue
        mkt = "SH" if code.startswith("6") else ("BJ" if code[0] in "489" else "SZ")
        items.append(
            {
                "symbol": f"{code}.{mkt}",
                "name": str(row.get("名称", "")),
                "close": _f(row.get("最新价")),
                "pct_chg": _f(row.get("涨跌幅")),
                "amount": _f(row.get("成交额")),
                "open_count": int(float(row.get("炸板次数", 0) or 0)),
                "consecutive": int(float(row.get("连板数", row.get("连续涨停天数", 0)) or 0)),
                "reason": str(row.get("涨停原因类别", row.get("所属行业", "")) or ""),
            }
        )
    return 200, {"items": items}


def ak_minute(qs: dict):
    import akshare as ak

    symbol = (qs.get("symbol") or [""])[0]
    freq = (qs.get("freq") or ["1m"])[0]
    start = (qs.get("start") or [""])[0]
    end = (qs.get("end") or [""])[0]
    code = symbol.split(".")[0]
    period = "1" if freq == "1m" else "5"
    try:
        df = ak.stock_zh_a_hist_min_em(symbol=code, period=period, start_date=start, end_date=end)
    except Exception as e:
        return unavailable(str(e))
    bars = []
    if df is None or getattr(df, "empty", True):
        return 200, {"bars": []}
    for _, row in df.iterrows():
        t = datetime.fromisoformat(str(row.iloc[0])[:19]).replace(tzinfo=SHANGHAI)
        bars.append(
            {
                "symbol": symbol,
                "time": iso(t),
                "freq": freq,
                "open": float(row.get("开盘", 0) or 0),
                "high": float(row.get("最高", 0) or 0),
                "low": float(row.get("最低", 0) or 0),
                "close": float(row.get("收盘", 0) or 0),
                "volume": int(float(row.get("成交量", 0) or 0)) * 100,
                "amount": float(row.get("成交额", 0) or 0),
            }
        )
    return 200, {"bars": bars}


def _f(v):
    try:
        if v is None:
            return None
        return float(v)
    except (TypeError, ValueError):
        return None


# --- Baostock -----------------------------------------------------------------
# 角色定位：盘后回补冗余（链末兜底）+ 财务三表。生命周期决策＝进程级单登录+全局互斥锁：
# baostock SDK 是模块级共享 socket，非线程安全，而 ThreadingHTTPServer 每请求新线程，
# 锁把整个 login-check+query+翻页串行化；否决「线程局部队列」（无关闭钩子、失败显式化
# 成本高）与「每请求重登」（握手 200-500ms × 全市场回补不可行）。串行吞吐对晚间批量足够。
# 断线码（-10001/-10002）锁内重登并重试一次；仍失败返回 UnavailableError 契约（503）。

_bs_lock = threading.Lock()
_bs = {"mod": None, "logged_in": False}
# baostock 断线/未登录错误码：服务方夜间维护踢线时出现。
_BS_DISCONNECT_CODES = ("-10001", "-10002")

_BS_FREQS = {"1d": "d", "1w": "w", "1M": "m", "5m": "5", "15m": "15", "30m": "30", "60m": "60"}


def bs_module():
    """惰性 import；未安装时返回 None，由处理器转 503 unavailable（不落 500 兜底）。"""
    if _bs["mod"] is None:
        try:
            import baostock as bs
        except ImportError:
            return None
        _bs["mod"] = bs
    return _bs["mod"]


def _bs_symbol(code: str) -> str | None:
    """600519.SH → sh.600519（baostock 代码形）。

    BJ 市场不支持：返回 None，调用点显式进 skipped（静默丢与拿错市场数据都禁止）。
    BJ 回补改走 tdx_tcp（枚举/取数已支持 BJ，见 _tcp_bj_items）或东财链，
    不要在 baostock 链硬凑。S10 明示口径。
    """
    s = code.strip().upper()
    if "." not in s:
        return None
    num, mkt = s.split(".", 1)
    if mkt == "BJ":
        return None
    if mkt in ("SH", "SZ") and num.isdigit():
        return f"{mkt.lower()}.{num}"
    return None


def _bs_rows(rs) -> list[dict]:
    """把 ResultData 翻页成 list[dict]（fields 顺序即 get_row_data 顺序）。"""
    rows = []
    while rs.error_code == "0" and rs.next():
        rows.append(dict(zip(rs.fields, rs.get_row_data())))
    return rows


def bs_query_locked(fn):
    """锁内确保登录后执行 fn(bs)；断线码触发一次锁内重登+重试。
    返回 (result, None)；失败返回 (None, reason)。"""
    with _bs_lock:
        bs = bs_module()
        if bs is None:
            return None, "未安装 baostock，见 pyworker/requirements.txt"
        if not _bs["logged_in"]:
            lg = bs.login()
            if getattr(lg, "error_code", "-1") != "0":
                return None, f"baostock 登录失败：{getattr(lg, 'error_msg', '')}"
            _bs["logged_in"] = True
        rs = fn(bs)
        if getattr(rs, "error_code", "-1") in _BS_DISCONNECT_CODES:
            # 断线：锁内重登并重试一次；仍失败交上层显式化。
            _bs["logged_in"] = False
            lg = bs.login()
            if getattr(lg, "error_code", "-1") != "0":
                return None, f"baostock 重登失败：{getattr(lg, 'error_msg', '')}"
            _bs["logged_in"] = True
            rs = fn(bs)
        if getattr(rs, "error_code", "-1") != "0":
            return None, f"baostock 查询失败：{getattr(rs, 'error_code', '')} {getattr(rs, 'error_msg', '')}"
        return rs, None


def bs_bars(body: dict):
    freq_in = body.get("freq") or "1d"
    freq = _BS_FREQS.get(freq_in)
    if freq is None:
        # 不支持的频率（如 1m/3m）显式 400：显式缺失，不冒充能取。
        return 400, {"error": "baostock 不支持该频率", "freq": freq_in}
    start = body.get("start") or ""
    end = body.get("end") or start
    expect = body.get("expect") or ""
    adjustflag = str(body.get("adjustflag") or "3")
    symbols = body.get("symbols") or []
    bs = bs_module()
    if bs is None:
        return unavailable("未安装 baostock，见 pyworker/requirements.txt")
    if not symbols:
        # 空名单补全：对 end 日 query_all_stock 拿当日在市代码。
        rs, reason = bs_query_locked(lambda m: m.query_all_stock(day=end))
        if rs is None:
            return unavailable(reason)
        symbols = []
        for row in _bs_rows(rs):
            code = str(row.get("code", ""))
            if code.startswith(("sh.", "sz.")):
                num = code.split(".", 1)[1]
                symbols.append(f"{num}.{code[:2].upper()}")
    fields = "date,code,open,high,low,close,preclose,volume,amount"
    if freq in ("5", "15", "30", "60"):
        fields = "date,time,code,open,high,low,close,volume,amount"
    bars: list[dict] = []
    skipped: list[str] = []
    for sym in symbols:
        code = _bs_symbol(sym)
        if code is None:
            # BJ/无法转换的代码显式列 skipped——不静默丢、不用烂数据冒充。
            skipped.append(sym)
            continue

        def fetch(m, c=code, f=freq):
            return m.query_history_k_data_plus(
                c, fields, start_date=start, end_date=end, frequency=f, adjustflag=adjustflag
            )

        rs, reason = bs_query_locked(fetch)
        if rs is None:
            return unavailable(reason)
        for row in _bs_rows(rs):
            date_s = str(row.get("date", ""))[:10]
            if not date_s:
                continue
            if freq in ("5", "15", "30", "60"):
                # baostock 分钟 time 形如 20261016093500000（毫秒尾），取 bar 起始。
                t_raw = str(row.get("time", ""))
                try:
                    t = datetime.strptime(t_raw[:14], "%Y%m%d%H%M%S").replace(tzinfo=SHANGHAI)
                except ValueError:
                    continue
            else:
                try:
                    t = datetime.strptime(date_s, "%Y-%m-%d").replace(tzinfo=SHANGHAI)
                except ValueError:
                    continue
            bar = {
                "symbol": sym,
                "time": iso(t),
                "freq": freq_in,
                "open": _f(row.get("open")),
                "high": _f(row.get("high")),
                "low": _f(row.get("low")),
                "close": _f(row.get("close")),
                # volume 单位即股，不乘100（与 TDX 文件/TCP 口径不同，注意区分）；
                # 数值空串/坏值 → null（pre_close 可空），绝不用 0 冒充。
                "pre_close": _f(row.get("preclose")),
                "volume": (lambda v: int(v) if v is not None else None)(_f(row.get("volume"))),
                "amount": _f(row.get("amount")),
            }
            bars.append(bar)
    last = max((b["time"][:10] for b in bars), default="")
    return 200, {"bars": bars, "last_date": last, "stale": bool(expect) and last < expect, "skipped": skipped}


def _bs_quarters(since: str) -> list[tuple[int, int]]:
    """由 since→今天推出 (year, quarter) 列表（从旧到新）。"""
    try:
        d = datetime.strptime(since[:10], "%Y-%m-%d")
    except (TypeError, ValueError):
        d = datetime.now(SHANGHAI) - timedelta(days=180)
    now = datetime.now(SHANGHAI)
    y, q = d.year, (d.month - 1) // 3 + 1
    y2, q2 = now.year, (now.month - 1) // 3 + 1
    out = []
    while (y, q) <= (y2, q2):
        out.append((y, q))
        q += 1
        if q > 4:
            y, q = y + 1, 1
    return out


def bs_finance(body: dict):
    symbols = body.get("symbols") or []
    if not symbols:
        # finance 必填名单：全市场逐股×多季度×4 查询在串行锁下不可控，必须显式给。
        return 400, {"error": "bs_finance 需要 symbols"}
    since = body.get("since") or ""
    if not since:
        return 400, {"error": "bs_finance 需要 since（YYYY-MM-DD）"}
    periods = int(body.get("periods") or 5)
    if periods > 10:
        periods = 10
    quarters = _bs_quarters(since)[-periods:]
    bs = bs_module()
    if bs is None:
        return unavailable("未安装 baostock，见 pyworker/requirements.txt")
    items: list[dict] = []
    for sym in symbols:
        code = _bs_symbol(sym)
        if code is None:
            continue
        for year, quarter in quarters:
            merged = {"profit": None, "balance": None, "cashflow": None, "growth": None}

            def fetch_profit(m, c=code, y=year, q=quarter):
                return m.query_profit_data(code=c, year=y, quarter=q)

            rs, reason = bs_query_locked(fetch_profit)
            if rs is None:
                return unavailable(reason)
            profit = _bs_rows(rs)
            if not profit:
                continue
            merged["profit"] = profit[0]
            for name, query in (
                ("balance", "query_balance_data"),
                ("cashflow", "query_cash_flow_data"),
                ("growth", "query_growth_data"),
            ):
                fn = getattr(bs, query)

                def fetch(m, f=fn, c=code, y=year, q=quarter):
                    return f(code=c, year=y, quarter=q)

                rs, reason = bs_query_locked(fetch)
                if rs is None:
                    return unavailable(reason)
                rows = _bs_rows(rs)
                if rows:
                    merged[name] = rows[0]
            p = merged["profit"]
            stat_date = str(p.get("statDate", ""))[:10]
            # pub_date 优先 profit.pubDate，退化 growth 等行的 pubDate；缺失或早于 since 的行
            # skip——AnnDate 缺失不可入库（显式缺失纪律）。
            pub_date = str(p.get("pubDate", ""))[:10]
            if not pub_date:
                for name in ("balance", "cashflow", "growth"):
                    row = merged[name]
                    if row and str(row.get("pubDate", ""))[:10]:
                        pub_date = str(row.get("pubDate", ""))[:10]
                        break
            if not pub_date or (since and pub_date < since):
                continue
            g = merged["growth"] or {}
            items.append(
                {
                    "symbol": sym,
                    "end_date": stat_date,
                    "pub_date": pub_date,
                    "kind": "indicator",
                    # 金额单位元（baostock 原始口径），坏值 → null。
                    "net_profit": _f(p.get("netProfit")),
                    "net_profit_yoy": _f(g.get("YOYPNI")),
                    "roe": _f(p.get("roeAvg")),
                    "eps": _f(p.get("epsTTM")),
                    "data": {k: (v or {}) for k, v in merged.items()},
                }
            )
    return 200, {"items": items}


# --- HTTP -------------------------------------------------------------------

def read_json(handler: BaseHTTPRequestHandler) -> dict:
    n = int(handler.headers.get("Content-Length") or 0)
    if n <= 0:
        return {}
    raw = handler.rfile.read(n)
    if not raw:
        return {}
    return json.loads(raw.decode("utf-8"))


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        return

    def send(self, status: int, obj):
        body = json_bytes(obj)
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def need(self, role: str) -> bool:
        if role not in ROLES:
            self.send(404, {"error": "role disabled", "role": role})
            return False
        return True

    def do_GET(self):
        try:
            self.handle_get()
        except Exception:
            # 不外泄异常文本与 traceback（对齐 source-worker/http_worker.py 纪律：
            # 堆栈里含内部路径与依赖细节）。响应体只给通用文案，全文进服务端日志。
            logger.error("pyworker GET %s 未捕获异常", self.path, exc_info=True)
            self.send(500, {"error": "internal error"})

    def do_POST(self):
        try:
            self.handle_post()
        except Exception:
            logger.error("pyworker POST %s 未捕获异常", self.path, exc_info=True)
            self.send(500, {"error": "internal error"})

    def handle_get(self):
        u = urlparse(self.path)
        qs = parse_qs(u.query)
        if u.path == "/healthz":
            self.send(200, {"status": "ok", "roles": sorted(ROLES), "tdx_dir": str(TDX_DIR)})
            return
        routes = {
            "/file/sectors": ("file", file_sectors),
            "/tcp/securities": ("tcp", tcp_securities),
            "/ak/global": ("akshare", ak_global),
            "/ak/macro": ("akshare", ak_macro),
            "/api/securities": ("local", local_securities),
        }
        if u.path in routes:
            role, fn = routes[u.path]
            if not self.need(role):
                return
            status, obj = fn()
            self.send(status, obj)
            return
        if u.path == "/ak/limit_pool":
            if not self.need("akshare"):
                return
            self.send(*ak_limit_pool(qs))
            return
        if u.path == "/ak/minute":
            if not self.need("akshare"):
                return
            self.send(*ak_minute(qs))
            return
        self.send(404, {"error": "not found"})

    def handle_post(self):
        u = urlparse(self.path)
        body = read_json(self)
        routes = {
            "/api/snapshot": ("local", lambda: local_snapshot(body)),
            "/api/bars": ("local", lambda: local_bars(body)),
            "/file/bars": ("file", lambda: file_bars(body)),
            "/tcp/snapshot": ("tcp", lambda: tcp_snapshots(body.get("symbols") or [])),
            "/tcp/bars": ("tcp", lambda: tcp_bars(body)),
            "/tcp/adj_factor": ("tcp", lambda: tcp_adj(body)),
            # Baostock 角色（盘后回补冗余 + 财务三表）：未装包走 503 unavailable，
            # 未启用角色走 need() 的 404 契约（role disabled）。
            "/bs/bars": ("baostock", lambda: bs_bars(body)),
            "/bs/finance": ("baostock", lambda: bs_finance(body)),
        }
        if u.path in routes:
            role, fn = routes[u.path]
            if not self.need(role):
                return
            self.send(*fn())
            return
        self.send(404, {"error": "not found"})


def main():
    host, port = LISTEN.split(":")
    httpd = ThreadingHTTPServer((host, int(port)), Handler)
    print(f"pyworker {LISTEN} roles={sorted(ROLES)} tdx={TDX_DIR}", flush=True)
    httpd.serve_forever()


if __name__ == "__main__":
    main()
