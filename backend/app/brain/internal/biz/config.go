package biz

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"server/conf"
	"server/pkg/llm"
)

const (
	defaultAnalyzeTimeout = 30 * time.Second
	defaultMaxBatch       = 30
	defaultBriefingCron   = "30 8 * * *"
)

var defaultRoutes = map[string]llm.Tier{
	"technical": llm.Small,
	"capital":   llm.Small,
	"news":      llm.Large,
	"external":  llm.Large,
	"synthesis": llm.Large,
	"briefing":  llm.Large,
	"explain":   llm.Small,
	"ask":       llm.Large,
}

var defaultWeights = map[Dim]float64{Technical: 0.30, News: 0.25, Capital: 0.30, External: 0.15}

var defaultPhaseCoef = map[string]float64{"ICE": 0.80, "RECOVER": 0.95, "WARM": 1.00, "HOT": 1.05, "FADE": 0.85}

type Settings struct {
	Routes         map[string]llm.Tier
	Weights        map[Dim]float64
	PhaseCoef      map[string]float64
	AnalyzeTimeout time.Duration
	MaxBatch       int
	BriefingCron   string
}

// NewSettings 用配置覆盖默认值。权重和不为 1 时按比例归一。
func NewSettings(c *conf.Brain) Settings {
	s := Settings{
		Routes:         map[string]llm.Tier{},
		Weights:        map[Dim]float64{},
		PhaseCoef:      map[string]float64{},
		AnalyzeTimeout: defaultAnalyzeTimeout,
		MaxBatch:       defaultMaxBatch,
		BriefingCron:   defaultBriefingCron,
	}
	for k, v := range defaultRoutes {
		s.Routes[k] = v
	}
	for k, v := range defaultWeights {
		s.Weights[k] = v
	}
	for k, v := range defaultPhaseCoef {
		s.PhaseCoef[k] = v
	}
	if c != nil {
		for k, v := range c.GetRoutes() {
			s.Routes[k] = llm.Tier(strings.ToLower(v))
		}
		for k, v := range c.GetWeights() {
			if v >= 0 {
				s.Weights[Dim(strings.ToLower(k))] = v
			}
		}
		for k, v := range c.GetPhaseCoef() {
			if v > 0 {
				s.PhaseCoef[strings.ToUpper(k)] = v
			}
		}
		if d := c.GetAnalyzeTimeout(); d != nil && d.AsDuration() > 0 {
			s.AnalyzeTimeout = d.AsDuration()
		}
		if c.GetMaxBatch() > 0 {
			s.MaxBatch = int(c.GetMaxBatch())
		}
		if c.GetBriefingCron() != "" {
			s.BriefingCron = c.GetBriefingCron()
		}
	}
	return normalizeWeights(s)
}

// ApplyApp 用配置页的智能体档位和权重盖过启动配置。权重有改动时重新归一。
func ApplyApp(s Settings, rows map[string]string) Settings {
	if len(rows) == 0 {
		return s
	}
	s.Routes = cloneRoutes(s.Routes)
	s.Weights = cloneWeights(s.Weights)
	tasks := []string{"technical", "capital", "news", "external", "synthesis", "briefing", "explain", "ask"}
	for _, task := range tasks {
		switch strings.ToLower(strings.TrimSpace(rows["app.agent."+task])) {
		case "small":
			s.Routes[task] = llm.Small
		case "large":
			s.Routes[task] = llm.Large
		}
	}
	changed := false
	for _, d := range Dims {
		raw := strings.TrimSpace(rows["app.agent.w."+string(d)])
		if raw == "" {
			continue
		}
		v, err := parseWeight(raw)
		if err != nil {
			continue
		}
		s.Weights[d] = v
		changed = true
	}
	if changed {
		s = normalizeWeights(s)
	}
	return s
}

func cloneRoutes(in map[string]llm.Tier) map[string]llm.Tier {
	out := make(map[string]llm.Tier, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneWeights(in map[Dim]float64) map[Dim]float64 {
	out := make(map[Dim]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func parseWeight(raw string) (float64, error) {
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("weight")
	}
	return v, nil
}

func normalizeWeights(s Settings) Settings {
	var sum float64
	for _, d := range Dims {
		sum += s.Weights[d]
	}
	if sum <= 0 {
		for k, v := range defaultWeights {
			s.Weights[k] = v
		}
		sum = 1
	}
	for _, d := range Dims {
		s.Weights[d] /= sum
	}
	return s
}

func (s Settings) tier(task string) llm.Tier {
	if t, ok := s.Routes[task]; ok {
		return t
	}
	return llm.Large
}

// coef 返回情绪阶段系数，未知阶段为 1。
func (s Settings) coef(phase string) float64 {
	if v, ok := s.PhaseCoef[strings.ToUpper(phase)]; ok {
		return v
	}
	return 1
}
