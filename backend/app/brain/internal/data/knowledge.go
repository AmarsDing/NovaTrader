// knowledge.go：M13-T09 brain → intel 的 M04 知识库只读检索客户端（HTTP，Search/SimilarCases）。
// 端点走配置注入（brain.intel_addr），空地址返回 nil 客户端，biz 按 kb_missing 显式降级。
package data

import (
	"context"
	"time"

	intv1 "server/api/intel/v1"
	"server/app/brain/internal/biz"
	"server/conf"

	"github.com/go-kratos/kratos/v2/log"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

type knowledgeClient struct {
	client intv1.KnowledgeHTTPClient
}

// NewKnowledgeClient 连接 M04 所在 intel 服务。intel_addr 为空时返回 nil，biz 标 kb_missing 降级。
func NewKnowledgeClient(c *conf.Brain, logger log.Logger) (biz.KnowledgeClient, func(), error) {
	addr := c.GetIntelAddr()
	if addr == "" {
		log.NewHelper(logger).Warn("brain: intel_addr is empty, kb case evidence will be kb_missing")
		return nil, func() {}, nil
	}
	conn, err := khttp.NewClient(context.Background(), khttp.WithEndpoint(addr), khttp.WithTimeout(5*time.Second))
	if err != nil {
		return nil, nil, err
	}
	return &knowledgeClient{client: intv1.NewKnowledgeHTTPClient(conn)}, func() { _ = conn.Close() }, nil
}

func (k *knowledgeClient) SimilarCases(ctx context.Context, q biz.KbQuery) (*biz.KbCases, error) {
	req := &intv1.KbSimilarCasesRequest{
		Situation:    q.Situation,
		Pattern:      q.Pattern,
		EmotionPhase: q.EmotionPhase,
		StockCode:    q.StockCode,
		Strategy:     q.Strategy,
		Book:         q.Book,
		TopK:         int32(q.TopK),
	}
	if !q.AsOf.IsZero() {
		req.AsOf = q.AsOf.Format(time.RFC3339)
	}
	reply, err := k.client.SimilarCases(ctx, req)
	if err != nil {
		return nil, err
	}
	out := &biz.KbCases{Degraded: reply.GetDegraded()}
	for _, hit := range reply.GetCases() {
		c := hit.GetCase()
		if c == nil {
			continue
		}
		kc := biz.KbCase{
			SignalID:     c.GetSignalId(),
			Book:         c.GetBook(),
			StockCode:    c.GetStockCode(),
			StockName:    c.GetStockName(),
			Strategy:     c.GetStrategy(),
			Pattern:      c.GetPattern(),
			EmotionPhase: c.GetEmotionPhase(),
			Outcome:      c.GetOutcome(),
			Attribution:  c.GetAttribution(),
		}
		// 「没取到≠0」：pnl_pct 是 optional，缺省保持 nil，不落成 0。
		if c.PnlPct != nil {
			v := c.GetPnlPct()
			kc.PnLPct = &v
		}
		out.Cases = append(out.Cases, kc)
	}
	return out, nil
}
