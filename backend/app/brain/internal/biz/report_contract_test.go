package biz

import "testing"

// 版号一经发布不得静默改动：升级必须同步 proto 注释、前端守卫与本测试。
func TestReportSchemaVersionsPinned(t *testing.T) {
	versions := map[string]int{
		"Analysis":        SchemaVersionAnalysis,
		"Briefing":        SchemaVersionBriefing,
		"ProgramReview":   SchemaVersionProgramReview,
		"PredictionCheck": SchemaVersionPredictionCheck,
	}
	for name, got := range versions {
		if got != 1 {
			t.Errorf("%s schema_version = %d；现行契约应为 1，升版需同步 proto 注释/前端守卫/键集", name, got)
		}
	}
}

// kind 字面量与既有写入方对齐（reviewAIKind 在 review_ai.go）。
func TestReviewKindsAlignedWithProducers(t *testing.T) {
	if ReviewKindAI != reviewAIKind {
		t.Errorf("ReviewKindAI=%q 与 reviewAIKind=%q 漂移", ReviewKindAI, reviewAIKind)
	}
	if ReviewKindProgram != "program" || ReviewKindAIWeek != "ai_week" {
		t.Errorf("kind 字面量非契约值：program=%q ai_week=%q", ReviewKindProgram, ReviewKindAIWeek)
	}
}

// program v1 键集必须覆盖八格读数与必出的 missing。
func TestProgramStatsKeysCoversEightCells(t *testing.T) {
	set := map[string]bool{}
	for _, k := range ProgramStatsKeysV1 {
		set[k] = true
	}
	must := []string{
		"advance", "decline", "amount", // 格 1
		"amount_avg20_ratio", "amount_avg60_ratio", // 格 2
		"up", "down", "broken", "seal_rate", "max_height", // 格 3
		"tier_avg", "tier_median", // 格 4
		"promo_12", // 格 5
		"margin_total", "margin_chg_5d", // 格 6
		"index_above_20", "index_above_60", "index_above_250", // 格 7
		"themes_top5", // 格 8
		"missing",
	}
	for _, k := range must {
		if !set[k] {
			t.Errorf("program v1 键集缺 %s", k)
		}
	}
}

// AI 分段键序必须与产出方 aiSegmentOrder 一致，契约与实现不两套。
func TestAISegmentIDsMatchProducer(t *testing.T) {
	if len(AIReviewSegmentIDsV1) != len(aiSegmentOrder) {
		t.Fatalf("段数漂移：契约 %d 产出 %d", len(AIReviewSegmentIDsV1), len(aiSegmentOrder))
	}
	for i, seg := range aiSegmentOrder {
		if string(seg) != AIReviewSegmentIDsV1[i] {
			t.Errorf("段序漂移：[%d] 契约 %s 产出 %s", i, AIReviewSegmentIDsV1[i], seg)
		}
	}
}

// ai/ai_week 顶层键集含双向关键键（防改名静默破契约）。
func TestAIStatsKeysContainCriticalKeys(t *testing.T) {
	aiSet := map[string]bool{}
	for _, k := range AIReviewStatsKeysV1 {
		aiSet[k] = true
	}
	for _, k := range []string{"segments", "evidence", "omitted_count", "partial", "degraded_segments", "missing"} {
		if !aiSet[k] {
			t.Errorf("ai v1 键集缺 %s", k)
		}
	}
	weekSet := map[string]bool{}
	for _, k := range AIWeekStatsKeysV1 {
		weekSet[k] = true
	}
	for _, k := range []string{"window_start", "window_end", "day_count", "usable_days", "days", "missing"} {
		if !weekSet[k] {
			t.Errorf("ai_week v1 键集缺 %s", k)
		}
	}
}
