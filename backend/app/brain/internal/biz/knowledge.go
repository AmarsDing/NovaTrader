// knowledge.go：M13-T09 脑内研判决接 M04 知识库（RAG）。事实包组好后按情形检索相似历史案例，
// 命中 top_k=3 注入 `C:[kb_case] pattern=… outcome=…` 证据行。
// 数据纪律：检索不可用（客户端未配置 / 调用失败 / 对方 degraded 降级）绝不静默当「无案例」——
// 跳过 kb 段并在 FactPack.Meta 显式标 kb_missing；真取到但零命中是合法空结果，不标缺失。
package biz

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// kbTopK 注入事实包的案例条数上限（M13 设计 T09 定死 top_k=3）。
const kbTopK = 3

// KbQuery 是一次相似案例检索的入参，字段与 intel knowledge.proto 的 KbSimilarCasesRequest 对齐。
type KbQuery struct {
	Situation    string
	Pattern      string
	EmotionPhase string
	StockCode    string
	Strategy     string
	Book         string
	AsOf         time.Time
	TopK         int
}

// KbCase 是一条历史案例。PnLPct 为 nil 表示收益没取到（「没取到≠0」），消费方不得当 0 用。
type KbCase struct {
	SignalID     int64
	Book         string
	StockCode    string
	StockName    string
	Strategy     string
	Pattern      string
	EmotionPhase string
	Outcome      string
	Attribution  string
	PnLPct       *float64
}

// KbCases 是检索结果。Degraded=true 表示对方只走了关键词路（embed 未就绪），按缺失处理。
type KbCases struct {
	Cases    []KbCase
	Degraded bool
}

// KnowledgeClient 是 M04 知识库的只读检索端口（data 层用 HTTP 接 intel 实现）。
type KnowledgeClient interface {
	SimilarCases(ctx context.Context, q KbQuery) (*KbCases, error)
}

// attachKBCases 给事实包补相似案例证据行。开关关闭时直通（运营显式关掉，不算缺失）；
// 检索不可用时 Meta["kb_missing"]=true 显式表达，绝不静默当无案例。
func (uc *Usecase) attachKBCases(ctx context.Context, pack FactPack) FactPack {
	if !uc.settings().KBEnabled {
		return pack
	}
	missing := func() FactPack {
		if pack.Meta == nil {
			pack.Meta = map[string]any{}
		}
		pack.Meta["kb_missing"] = true
		return pack
	}
	if uc.knowledge == nil {
		uc.log.Warnf("knowledge client not configured (brain.intel_addr empty), kb evidence skipped")
		return missing()
	}
	reply, err := uc.knowledge.SimilarCases(ctx, KbQuery{
		Situation:    kbSituation(pack),
		EmotionPhase: pack.Phase,
		StockCode:    pack.Symbol,
		AsOf:         pack.AsOf,
		TopK:         kbTopK,
	})
	if err != nil {
		uc.log.Warnf("kb similar cases for %s: %v", pack.Symbol, err)
		return missing()
	}
	if reply == nil || reply.Degraded {
		return missing()
	}
	for i, c := range reply.Cases {
		if i >= kbTopK {
			break
		}
		pack.Facts = append(pack.Facts, Fact{
			ID:    fmt.Sprintf("C:[kb_case]%d", i+1),
			Label: "相似历史案例",
			Text:  kbCaseText(c),
			Dim:   External,
		})
	}
	return pack
}

// kbSituation 组一句情形描述：标的 + 名称 + 情绪阶段，供相似检索对齐场面。
func kbSituation(pack FactPack) string {
	parts := []string{pack.Symbol}
	if pack.Name != "" {
		parts = append(parts, pack.Name)
	}
	s := strings.Join(parts, " ")
	if pack.Phase != "" {
		s += " 情绪阶段" + pack.Phase
	}
	return strings.TrimSpace(s)
}

// kbCaseText 渲染一条案例证据：pattern/outcome 必有；attribution/pnl 取不到就整段省略，不落 0。
func kbCaseText(c KbCase) string {
	var b strings.Builder
	b.WriteString("pattern=" + orNA(c.Pattern) + " outcome=" + orNA(c.Outcome))
	if c.Attribution != "" {
		b.WriteString(" attribution=" + c.Attribution)
	}
	if c.PnLPct != nil {
		fmt.Fprintf(&b, " pnl=%+.2f%%", *c.PnLPct)
	}
	target := c.StockCode
	if c.StockName != "" {
		target = c.StockCode + " " + c.StockName
	}
	if target != "" {
		b.WriteString(" 标的=" + target)
	}
	return b.String()
}

func orNA(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return s
}
