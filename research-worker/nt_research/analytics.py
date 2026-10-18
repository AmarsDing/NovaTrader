from __future__ import annotations
import html
import json
import math
import statistics
import time
from pathlib import Path
from .protocol import Invalid, Unavailable, dates, finite, require


def nav_returns(job, data):
    if data.get("kind") != "account_nav" or data.get("fee_policy") != "net_of_costs" or data.get("frequency") != "1d":
        raise Invalid("only fee-net daily account NAV is accepted; signal samples are not NAV")
    if data.get("cash_flow_policy") not in {"no_external_flows", "precomputed_unit_nav"}:
        raise Invalid("external flows need an authoritative precomputed unit NAV")
    if data.get("currency") != "CNY" or not data.get("ledger_hash") or not data.get("calendar_version"):
        raise Invalid("currency, ledger hash and calendar version are required")
    if data.get("benchmark_kind") not in {"price_index", "total_return_index"} or not data.get("benchmark_id"):
        raise Invalid("an explicit aligned benchmark is required; ticker downloads are forbidden")
    points = data.get("points", [])
    calendar = dates([p.get("date") for p in points], job)
    if data.get("expected_dates") != calendar:
        raise Invalid("NAV calendar coverage is incomplete; do not fill missing days with zero")
    nav, bench = [], []
    for row in points:
        if row.get("valuation_complete") is not True:
            raise Invalid("unreconciled or missing valuations cannot enter performance")
        flow = finite(row.get("external_flow"), "external_flow")
        if data["cash_flow_policy"] == "no_external_flows" and flow != 0:
            raise Invalid("external cash flow requires precomputed unit NAV")
        n = finite(row.get("nav"), "nav")
        b = finite(row.get("benchmark"), "benchmark")
        if n <= 0 or b <= 0:
            raise Invalid("NAV and benchmark must be positive")
        nav.append(n); bench.append(b)
    return calendar, nav, bench, [b / a - 1 for a, b in zip(nav, nav[1:])], [b / a - 1 for a, b in zip(bench, bench[1:])]


def reference_metrics(nav, returns, annual=252):
    peak = nav[0]
    drawdown = 0.0
    for value in nav:
        peak = max(peak, value)
        drawdown = max(drawdown, (peak - value) / peak)
    sigma = statistics.stdev(returns) if len(returns) > 1 else None
    return {"total_return": nav[-1] / nav[0] - 1, "annual_return": (nav[-1] / nav[0]) ** (annual / len(returns)) - 1,
            "max_drawdown": drawdown, "sharpe": statistics.mean(returns) / sigma * math.sqrt(annual) if sigma else None,
            "return_periods": len(returns), "positive_day_fraction": sum(r > 0 for r in returns) / len(returns)}


def performance(job, data, output):
    calendar, nav, benchmark, returns, benchmark_returns = nav_returns(job, data)
    options = job.get("options", {})
    if options.get("periods_per_year", 252) != 252 or options.get("rf", 0) != 0:
        raise Invalid("initial comparison fixes daily frequency, 252 periods/year and rf=0")
    pd = require("pandas"); qs = require("quantstats")
    series = pd.Series(returns, index=pd.to_datetime(calendar[1:]), name="net_account_nav")
    bseries = pd.Series(benchmark_returns, index=series.index, name=data["benchmark_id"])
    nseries = pd.Series(nav, index=pd.to_datetime(calendar), name="net_account_nav")
    reference = reference_metrics(nav, returns)
    def clean(value):
        return float(value) if math.isfinite(float(value)) else None
    quantstats = {"total_return": clean(qs.stats.comp(series)),
                 "annual_return": clean(qs.stats.cagr(series, rf=0, periods=252)),
                 "sharpe": clean(qs.stats.sharpe(series, rf=0, periods=252)) if reference["sharpe"] is not None else None,
                 "max_drawdown": abs(clean(qs.stats.max_drawdown(nseries))) }
    comparison = {key: {"go_convention_reference": reference[key], "quantstats": value,
                        "difference": None if value is None or reference[key] is None else value - reference[key]}
                  for key, value in quantstats.items()}
    # Native self-contained HTML remains usable for short or constant samples, for
    # which the vendor full tearsheet may have insufficient observations.
    result = {"metrics": reference, "quantstats": quantstats, "comparison": comparison,
              "benchmark_total_return": benchmark[-1] / benchmark[0] - 1,
              "start": calendar[0], "end": calendar[-1], "initial_nav_included": True,
              "definitions": {"annualization": "252 / return periods; initial point excluded from count", "drawdown": "includes initial NAV", "win_rate": "positive days, not trade wins", "cash_flow_policy": data["cash_flow_policy"], "benchmark_kind": data["benchmark_kind"]},
              "warnings": [], "promotion": "research_only"}
    if len(returns) < 2:
        result["warnings"].append("single return period: sample Sharpe is undefined")
    safe = html.escape(json.dumps(result, ensure_ascii=False, indent=2))
    (output / "performance.html").write_text("<!doctype html><meta charset='utf-8'><title>NovaTrader performance audit</title><style>body{background:#08111e;color:#dbeeff;font:16px monospace;padding:32px}pre{white-space:pre-wrap}</style><h1>费用后账户净值 / 绩效核验</h1><p>QuantStats metrics; 252-day reference. This is a research artifact.</p><pre>" + safe + "</pre>", encoding="utf-8")
    if options.get("vendor_html"):
        try:
            qs.reports.html(series, benchmark=bseries, rf=0, periods_per_year=252, output=str(output / "quantstats.html"), title="NovaTrader fee-net NAV", match_dates=False)
        except Exception as exc:
            result["warnings"].append("vendor HTML unavailable: " + type(exc).__name__)
    return result


def return_matrix(job, data):
    if data.get("kind") != "return_matrix" or data.get("currency") != "CNY" or data.get("frequency") != "1d":
        raise Invalid("an explicit daily CNY return matrix is required")
    columns = data.get("symbols", [])
    if not columns or columns != sorted(set(columns)) or len(columns) > 100:
        raise Invalid("freeze sorted unique symbols, at most 100")
    days = dates(data.get("dates"), job, 3)
    values = data.get("returns", [])
    if len(values) != len(days) or data.get("known_at_cutoff") is not True:
        raise Invalid("matrix coverage/availability is unknown")
    for row in values:
        if len(row) != len(columns) or any(finite(v, "return") <= -1 for v in row):
            raise Invalid("invalid returns; missing values cannot be filled")
    pd = require("pandas")
    return pd.DataFrame(values, index=pd.to_datetime(days), columns=columns)


def portfolio(job, data, output):
    matrix = return_matrix(job, data)
    pypfopt = require("PyPortfolioOpt", "pypfopt")
    np = require("numpy"); cp = require("cvxpy")
    opts = job.get("options", {})
    if opts.get("method", "min_volatility") != "min_volatility":
        raise Invalid("only constrained minimum volatility is enabled; HRP is not ERC")
    solver = opts.get("solver", "CLARABEL")
    if solver not in {"CLARABEL", "OSQP"} or solver not in cp.installed_solvers():
        raise Unavailable("explicit_solver_unavailable")
    cash = finite(opts.get("cash_floor", 0.1), "cash_floor")
    if not 0 <= cash < 1:
        raise Invalid("cash_floor outside [0,1)")
    budget = 1 - cash; caps = opts.get("max_weights", {})
    max_weight = finite(opts.get("max_weight", 0.3), "max_weight")
    limits = {s: finite(caps.get(s, max_weight), "max_weight") for s in matrix.columns}
    if any(x < 0 or x > 1 for x in limits.values()) or sum(limits.values()) + 1e-9 < budget:
        return {"solver_status": "INFEASIBLE", "reason": "asset caps cannot fund risk budget", "weights": {}, "cash_weight": None, "promotion": "research_only"}
    if (matrix.std() <= 1e-12).any():
        return {"solver_status": "INSUFFICIENT_VARIATION", "reason": "zero-variance asset requires reviewed covariance policy", "weights": {}, "promotion": "research_only"}
    cov = pypfopt.risk_models.CovarianceShrinkage(matrix, returns_data=True, frequency=252).ledoit_wolf()
    ef = pypfopt.EfficientFrontier(None, cov, weight_bounds=[(0, limits[s] / budget) for s in matrix.columns], solver=solver)
    sectors = opts.get("sectors", {}); sector_caps = opts.get("sector_caps", {})
    if sector_caps:
        if set(sectors) != set(matrix.columns):
            raise Invalid("sector mapping must cover every asset")
        ef.add_sector_constraints(sectors, {}, {s: finite(v, "sector_cap") / budget for s, v in sector_caps.items()})
    current = opts.get("current_weights", {})
    locked = opts.get("locked_symbols", [])
    if locked or "max_turnover" in opts:
        if set(current) != set(matrix.columns):
            raise Invalid("current weights must cover every asset for lock/turnover constraints")
        existing = np.array([finite(current[s], "current_weight") for s in matrix.columns])
        if (existing < 0).any() or existing.sum() > 1 + 1e-8:
            raise Invalid("invalid current weights")
        for symbol in locked:
            if symbol not in matrix.columns:
                raise Invalid("unknown locked asset")
            idx = matrix.columns.get_loc(symbol)
            ef.add_constraint(lambda w, i=idx: w[i] * budget == existing[i])
        if "max_turnover" in opts:
            limit = finite(opts["max_turnover"], "max_turnover")
            if limit < 0:
                raise Invalid("negative turnover limit")
            cash_change = abs(cash - (1 - existing.sum()))
            ef.add_constraint(lambda w: (cp.norm(w * budget - existing, 1) + cash_change) / 2 <= limit)
    try:
        ef.min_volatility()
    except Exception as exc:
        return {"solver_status": "INFEASIBLE_OR_SOLVER_FAILURE", "reason": type(exc).__name__, "weights": {}, "promotion": "research_only"}
    weights = {s: float(w) * budget for s, w in zip(matrix.columns, ef.weights)}
    w = np.array(list(weights.values())); covariance = cov.to_numpy(); variance = float(w @ covariance @ w)
    residual = max(abs(sum(weights.values()) + cash - 1), max(max(0, weights[s] - limits[s], -weights[s]) for s in weights))
    for sector, cap in sector_caps.items():
        residual = max(residual, sum(weights[s] for s in weights if sectors[s] == sector) - cap)
    for symbol in locked:
        residual = max(residual, abs(weights[symbol] - current[symbol]))
    if "max_turnover" in opts:
        turnover = (sum(abs(weights[s] - current[s]) for s in weights) + cash_change) / 2
        residual = max(residual, turnover - limit)
    if residual > 1e-6 or not all(math.isfinite(v) for v in weights.values()):
        return {"solver_status": "RESIDUAL_FAILED", "max_violation": residual, "weights": {}, "promotion": "research_only"}
    return {"solver_status": "OPTIMAL", "method": "constrained_minimum_volatility", "solver": solver,
            "weights": weights, "cash_weight": cash, "annual_volatility": math.sqrt(max(0, variance)),
            "risk_contribution": {s: float(v) for s, v in zip(matrix.columns, w * (covariance @ w) / variance)} if variance > 0 else None,
            "max_constraint_violation": residual, "observations": len(matrix), "promotion": "shadow_target_only",
            "not_executed": ["share_rounding", "fees_reservation", "T+1_sellability", "market_limits", "final_Go_risk_check"]}


def vector_screen(job, data, output):
    opts = job.get("options", {})
    if opts.get("license_acknowledgement") != "Apache-2.0-with-Commons-Clause-reviewed":
        raise Unavailable("vectorbt_license_opt_in_required")
    vbt = require("vectorbt"); import importlib.metadata
    if opts.get("package_version") != importlib.metadata.version("vectorbt"):
        raise Invalid("vectorbt package version must match the approved pinned version")
    matrix = return_matrix(job, data)
    train_end = opts.get("train_end"); test_start = opts.get("test_start")
    if not train_end or not test_start or train_end >= test_start:
        raise Invalid("freeze disjoint development/test dates before screening")
    training = matrix.loc[:train_end]
    if len(training) < 10 or matrix.loc[test_start:].empty:
        raise Invalid("insufficient separated development/test samples")
    windows = opts.get("windows", [5, 10, 20])
    if len(windows) > 50 or len(set(windows)) != len(windows) or any(type(w) is not int or w < 2 or w >= len(training) for w in windows):
        raise Invalid("invalid or excessive parameter search")
    closes = (1 + training).cumprod()
    start = time.perf_counter(); indicators = vbt.MA.run(closes, window=windows); cold = time.perf_counter() - start
    start = time.perf_counter(); vbt.MA.run(closes, window=windows); warm = time.perf_counter() - start
    trials = []
    for window in windows:
        mean = indicators.ma[window]
        for symbol in training.columns:
            # Lag the decision before applying a subsequent day's observed return.
            active = closes[symbol].gt(mean[symbol]).shift(1).fillna(False)
            selected = training[symbol][active]
            trials.append({"window": window, "symbol": symbol, "sample_count": len(selected), "mean_next_day_return": float(selected.mean()) if len(selected) else None})
    return {"trials": trials, "trial_count": len(trials), "cold_seconds": cold, "warm_seconds": warm,
            "train_end": train_end, "sealed_test_start": test_start, "test_scored": False,
            "promotion": "requires_Go_backtest_and_three_fill_reconciliation", "note": "Indicator screening only; no execution model or shared cash simulation."}
