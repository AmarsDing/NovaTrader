package biz

import (
	"context"
	"time"

	"server/pkg/rules"
)

// upstream.go 是 M13-T01 四上游端口在 biz 的接线：情绪阶段与当日利空挂到
// rules.Snapshot 上的唯一入口。取不到一律显式缺失（标记/日志），不冒充有数：
// - 情绪缺失：Stage 留空 → Fuse.Coef 未知阶段按 1.0，候选/报告标 sentiment_missing；
// - 利空缺失：NegativeNews 保持 false 但该代码本轮不当「无利空」——利空出口抑制，
//   报告/日志标 negative_news_missing。空集（确无利空）返回 missing 为空。

// stageAt 经 SentimentSource 取 asOf 当日情绪阶段。端口未装配或源端缺失都返回
// ok=false；err 只表达取数故障（供日志），ok 才是「拿到了没有」。
func (uc *Usecase) stageAt(ctx context.Context, asOf time.Time) (rules.Stage, bool, error) {
	if uc.sentiment == nil {
		return "", false, nil
	}
	return uc.sentiment.Stage(ctx, asOf)
}

// attachStage 把当日情绪阶段写到快照上。取不到（!ok）时 Stage 一律留空：
// Fuse.Coef 对未知阶段回落 1.0，rep 同时记 sentiment_missing——情绪系数=1.0 的
// 降级口径与 M13 设计一致，恒 WARM 的编造值不再出现。
func (uc *Usecase) attachStage(ctx context.Context, snaps []rules.Snapshot, asOf time.Time, rep *ScanReport) {
	stage, ok, err := uc.stageAt(ctx, asOf)
	switch {
	case err != nil:
		uc.log.Warnf("sentiment fetch at %s: %v（本轮按缺失处理）", asOf.Format("2006-01-02 15:04"), err)
		ok = false
	case !ok:
		uc.log.Warnf("sentiment missing at %s（本轮融合系数按 1.0）", asOf.Format("2006-01-02 15:04"))
	}
	if !ok && rep != nil {
		rep.Missing = append(rep.Missing, MissSentiment)
	}
	for i := range snaps {
		snaps[i].Stage = stage
	}
}

// negativeNews 经 NewsSource 取当日利空。端口未装配或整体失败时全部代码进
// missing；单只失败只影响该只。set 交集外的代码是「确无利空」，missing 交集内
// 的是「没查成」，两者不得混用。
func (uc *Usecase) negativeNews(ctx context.Context, symbols []string, asOf time.Time) (set, missing map[string]bool) {
	if uc.news == nil {
		return map[string]bool{}, symbolSet(symbols)
	}
	set, missing, err := uc.news.Negative(ctx, symbols, asOf)
	if err != nil {
		uc.log.Warnf("negative news fetch at %s: %v（全部代码按缺失处理）", asOf.Format("2006-01-02 15:04"), err)
		return map[string]bool{}, symbolSet(symbols)
	}
	return set, missing
}

// attachNews 把当日利空挂到出口扫描的快照上。missing 的代码 NegativeNews 保持
// false 但记录到 rep/log：本轮利空出口对它们抑制（缺失不当「无利空」）。
func (uc *Usecase) attachNews(ctx context.Context, snaps []rules.Snapshot, symbols []string, asOf time.Time, rep *ScanReport) {
	set, missing := uc.negativeNews(ctx, symbols, asOf)
	for i := range snaps {
		if missing[snaps[i].Symbol] {
			continue
		}
		snaps[i].NegativeNews = set[snaps[i].Symbol]
	}
	if len(missing) > 0 {
		uc.log.Warnf("negative news missing for %d symbols at %s（利空出口本轮对这些代码抑制）",
			len(missing), asOf.Format("2006-01-02 15:04"))
		if rep != nil {
			rep.Missing = append(rep.Missing, MissNegativeNews)
		}
	}
}

func symbolSet(symbols []string) map[string]bool {
	out := make(map[string]bool, len(symbols))
	for _, s := range symbols {
		out[s] = true
	}
	return out
}
