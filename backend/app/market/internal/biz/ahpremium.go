package biz

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

/* ---------- AH 溢价（目录册增补 B5，查询时直抓） ----------
 *
 * 设计锚：M02 设计文档 B5 小节。本片为「查询时直抓」原型——发现源由 market 服务
 * 在被调用时即时外抓配对（东财 F10）与三腿报价（腾讯 A/H/HKDCNY 同批）合成溢价，
 * 不落库、不进调度、不预聚合（本批硬约束：零新域零表、cron 零增、无回放体）。
 * 数据纪律：任一脚缺失只进 Missing 逐项（按纪律六 why 颗粒度），衍生列整体缺省，
 * 禁止以 0/1.0/均值冒充。
 */

// AHStatus 五态。no_pair 独立成态落实 empty≠source_down（数据纪律先例），
// 与 GetChipDistributionReply 「status!=ok 衍生列全缺」同型。
const (
	AHStatusOK          = "ok"           // 三腿齐备，衍生列有值
	AHStatusNoPair      = "no_pair"      // F10 证实无 H 配对（空 data / STR_CODEH null / 非 .HK 三态合一）
	AHStatusMissing     = "missing"      // 有配对但报价/汇率腿缺失（衍生列整体缺省）
	AHStatusPairError   = "pair_error"   // F10 配对刷新失败（含 TTL 过期且刷新失败）= 配对未知，不产溢价数
	AHStatusSourceError = "source_error" // 腾讯报价请求级失败（配对事实列仍回波）
)

// AHLegWhy：缺腿原因小枚举（对面板「按 why 分流告警」提供颗粒度——纪律六）。
const (
	AHLegNoData  = "no_data"  // 响应行缺失或价格字段空串——空≠0（没取到≠0）
	AHLegNAValue = "na_value" // 价格字段显式 "0"=无报价哨兵
	AHLegParse   = "parse"    // 字段在场但行/数值不可解析（畸形防御）
)

// AHPairing F10 配对事实。Pair=false 表示东财证实无 H 配对（此时 HCode/HName 为空，
// AName 照常回波——纯 A 行在场只是 STR_CODEH 为空是合法业务态）。
type AHPairing struct {
	Pair    bool
	ACode   string // "601939"
	AMarket string // "SH" / "SZ" / "BJ"
	AName   string
	HCode   string // "00939"（无 .HK 后缀，查询串构造在 data 层保持原样不转写）
	HName   string
}

// AHLeg 单腿报价。Price==nil=缺失（Why 填原因，绝不以 0 冒充）。
// Time 是**源内 tick 时刻**（停牌腿=源最后成交 tick 的时刻），零值=源未给时刻；
// 停牌腿的“现价”是源的最后成交价，消费侧必须结合 Time 解读新鲜度。
type AHLeg struct {
	Price *float64
	Time  time.Time
	Why   string
}

// AHTriple 腾讯同批三腿。ATraded=false=A 股停牌/无成交（价格腿照常回波）。
type AHTriple struct {
	A, H, FX AHLeg
	ATraded  bool
}

// AHMissing 缺腿逐项（Leg=a_price/h_price/hkdcny，Why 见 AHLeg* 小枚举）。
type AHMissing struct {
	Leg string
	Why string
}

// AHPremiumReport 编排产物。status≠ok 时 Ratio/Premium 恒 nil（衍生列整体缺省），
// 配对事实列与已在场腿照常回波（能查到什么给什么）。
type AHPremiumReport struct {
	Status   string
	Reason   string
	Pairing  AHPairing
	A, H, FX AHLeg
	Ratio    *float64
	Premium  *float64
	Missing  []AHMissing
	ATraded  bool
	AsOf     time.Time
}

// AHSource 是 AH 外部报价链路端口（market data 侧自含薄 client 实现；httpc 非导出，
// 不可跨包复用 datahub 包）。两个方法都必须按需不轮询、显式超时、白名单三点。
type AHSource interface {
	// AHPair 查 F10 配对（TTL 缓存：正 24h / 负 30min）。Pair=false+nil 错误 = 证实无配对；
	// error = 配对未知（调用方映射 pair_error，绝不复用陈旧缓存谎报 no_pair）。
	AHPair(ctx context.Context, symbol string) (AHPairing, error)
	// AHTriple 一次请求取 A 现价、H 现价、HKDCNY。腿级缺失走 AHLeg.Why 显式；
	// error = 请求级失败（非 2xx/超时/整体畸形，调用方映射 source_error）。
	AHTriple(ctx context.Context, aCode, aMarket, hCode string) (AHTriple, error)
}

// ahCompute 纯函数：ah_ratio=round(a/(h×rate),4)，premium_pct=round((ratio−1)×100,2)
// （对 vibe-astock tencent.py 口径；正=A 贵、负合法不钳 0——折价防恒正盲区）。
func ahCompute(a, h, rate float64) (ratio, premium float64) {
	ratio = math.Round(a/(h*rate)*10_000) / 10_000
	premium = math.Round((ratio-1)*100*100) / 100
	return ratio, premium
}

// AHPremium 编排：parse 已在 service 完成（symbol=Tongdaxin 形态 "601939.SH"）。
// 顺序：配对（TTL 内负缓存短路）→ no_pair 直接返回（此时 AHTriple 调用计数恒 0，
// 测试钉）→ 三腿同批 → 缺腿逐项 → 全齐求衍生。
func (u *Usecase) AHPremium(ctx context.Context, symbol string) (*AHPremiumReport, error) {
	ah := u.cfg.ah
	if ah == nil {
		return nil, fmt.Errorf("biz: AH 报价链路未装配（data.NewAHQuote 未注入）")
	}
	rep := &AHPremiumReport{AsOf: time.Now()}
	code, mkt, _ := strings.Cut(symbol, ".")
	rep.Pairing.ACode, rep.Pairing.AMarket = code, mkt

	pair, err := ah.AHPair(ctx, symbol)
	if err != nil {
		// 过期且刷新失败同源路径——配对未知，不产溢价数（对等立档口径）。
		rep.Status = AHStatusPairError
		rep.Reason = err.Error()
		return rep, nil
	}
	rep.Pairing.AName, rep.Pairing.HCode, rep.Pairing.HName = pair.AName, pair.HCode, pair.HName
	if !pair.Pair {
		rep.Status = AHStatusNoPair
		rep.Reason = "F10 证实无 H 配对（STR_CODEH 空或非 .HK）"
		return rep, nil
	}

	triple, err := ah.AHTriple(ctx, code, mkt, pair.HCode)
	if err != nil {
		rep.Status = AHStatusSourceError
		rep.Reason = err.Error()
		return rep, nil
	}
	rep.A, rep.H, rep.FX = triple.A, triple.H, triple.FX
	rep.ATraded = triple.ATraded

	for _, x := range []struct {
		name string
		leg  AHLeg
	}{
		{"a_price", triple.A},
		{"h_price", triple.H},
		{"hkdcny", triple.FX},
	} {
		if x.leg.Price == nil {
			why := x.leg.Why
			if why == "" {
				why = AHLegNoData
			}
			rep.Missing = append(rep.Missing, AHMissing{Leg: x.name, Why: why})
		}
	}
	if len(rep.Missing) > 0 {
		rep.Status = AHStatusMissing
		rep.Reason = fmt.Sprintf("缺腿 %d 项，衍生列整体缺省", len(rep.Missing))
		return rep, nil
	}

	ratio, premium := ahCompute(*triple.A.Price, *triple.H.Price, *triple.FX.Price)
	rep.Status = AHStatusOK
	rep.Ratio, rep.Premium = &ratio, &premium
	return rep, nil
}
