package rules

// FuseParams 是规则分和 AI 分的融合参数。
type FuseParams struct {
	RuleWeight  float64
	AIWeight    float64
	SelectScore float64
	HighScore   float64
	StageCoef   map[Stage]float64
}

func DefaultFuse() FuseParams {
	return FuseParams{
		RuleWeight:  0.4,
		AIWeight:    0.6,
		SelectScore: 75,
		HighScore:   85,
		// 与 M05 设计文档第 5 节的情绪系数一致。
		StageCoef: map[Stage]float64{
			StageIce:     0.80,
			StageRecover: 0.95,
			StageWarm:    1.00,
			StageHot:     1.05,
			StageFade:    0.85,
		},
	}
}

func LoadFuse(get Lookup) FuseParams {
	d := DefaultFuse()
	coef := make(map[Stage]float64, len(d.StageCoef))
	for stage, v := range d.StageCoef {
		coef[stage] = pick(get, "m06.stage_coef."+string(stage), v)
	}
	return FuseParams{
		RuleWeight:  pick(get, "m06.rule_weight", d.RuleWeight),
		AIWeight:    pick(get, "m06.ai_weight", d.AIWeight),
		SelectScore: pick(get, "m06.select_score", d.SelectScore),
		HighScore:   pick(get, "m06.high_score", d.HighScore),
		StageCoef:   coef,
	}
}

// Coef 返回情绪阶段系数。阶段未知时按 1。
func (p FuseParams) Coef(stage Stage) float64 {
	if v, ok := p.StageCoef[stage]; ok {
		return v
	}
	return 1
}

// Fuse 计算综合分。ai 为 nil 表示模型不可用，此时只用规则分，degraded 为真。
// AI 分在 M05 已乘过情绪系数，这里只给规则分乘系数。
func (p FuseParams) Fuse(rule float64, ai *float64, stage Stage) (score float64, degraded bool) {
	r := clamp(rule, 0, 100) * p.Coef(stage)
	if ai == nil {
		return clamp(r, 0, 100), true
	}
	return clamp(p.RuleWeight*r+p.AIWeight*clamp(*ai, 0, 100), 0, 100), false
}

// Selected 判断是否入选，以及是否高价值。
func (p FuseParams) Selected(score float64) (selected, high bool) {
	return score >= p.SelectScore, score >= p.HighScore
}
