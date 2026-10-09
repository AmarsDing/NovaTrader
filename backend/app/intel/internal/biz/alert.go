package biz

import (
	"math"
	"sort"
	"time"
)

const (
	AlertReasonImportance = "importance"
	AlertReasonShift      = "sentiment_shift"

	AlertWarn     = "warn"
	AlertCritical = "critical"

	criticalShift = 0.8
	// 综合情感只看近 72 小时。
	CompositeWindow = 72 * time.Hour
)

// Shift 是某只股票在本条情报入库前后的综合情感。
type Shift struct {
	Stock  string  `json:"stock"`
	Before float64 `json:"before"`
	After  float64 `json:"after"`
}

func (s Shift) Delta() float64 { return math.Abs(s.After - s.Before) }

// Alert 是 intel.alert 的载荷。
type Alert struct {
	NewsID     int      `json:"news_id"`
	ClusterID  int      `json:"cluster_id"`
	Reason     string   `json:"reason"`
	Level      string   `json:"level"`
	Title      string   `json:"title"`
	Stocks     []string `json:"stocks"`
	EventType  string   `json:"event_type"`
	Sentiment  float64  `json:"sentiment"`
	Importance int      `json:"importance"`
	Shift      *Shift   `json:"shift,omitempty"`
}

// DecideAlert 按重要度或情绪突变判定是否告警。两者都命中时报重要度，并带上突变信息。
func DecideAlert(it *Item, s Score, links []Link, shifts []Shift, importanceAt int, shiftAt float64) *Alert {
	var biggest *Shift
	for i := range shifts {
		if biggest == nil || shifts[i].Delta() > biggest.Delta() {
			biggest = &shifts[i]
		}
	}
	a := &Alert{
		NewsID: it.ID, ClusterID: it.ClusterID, Title: it.Title,
		Stocks: stockTargets(links), EventType: s.EventType, Sentiment: s.Sentiment, Importance: s.Importance,
	}
	switch {
	case s.Importance >= importanceAt:
		a.Reason, a.Level = AlertReasonImportance, AlertWarn
		if s.Importance >= 5 {
			a.Level = AlertCritical
		}
		if biggest != nil && biggest.Delta() >= shiftAt {
			sh := *biggest
			a.Shift = &sh
		}
	case biggest != nil && biggest.Delta() >= shiftAt:
		a.Reason, a.Level = AlertReasonShift, AlertWarn
		if biggest.Delta() >= criticalShift {
			a.Level = AlertCritical
		}
		sh := *biggest
		a.Shift = &sh
	default:
		return nil
	}
	return a
}

func stockTargets(links []Link) []string {
	out := []string{}
	for _, l := range links {
		if l.TargetType == TargetStock {
			out = append(out, l.Target)
		}
	}
	return out
}

// topStock 返回置信度最高的个股，写回 news_sentiment.stock_code。
func topStock(links []Link) string {
	best, conf := "", -1.0
	for _, l := range links {
		if l.TargetType == TargetStock && l.Confidence > conf {
			best, conf = l.Target, l.Confidence
		}
	}
	return best
}

// HotWord 是热词排行的一项。
type HotWord struct {
	Type      string
	Target    string
	Count     int
	PrevCount int
	Score     float64
}

// RankHotWords 得分 = 本窗口簇数 × log2(1 + (本窗口 + 1) / (上一窗口 + 1))。
func RankHotWords(now, prev map[[2]string]int, limit int) []HotWord {
	out := make([]HotWord, 0, len(now))
	for k, c := range now {
		p := prev[k]
		out = append(out, HotWord{
			Type: k[0], Target: k[1], Count: c, PrevCount: p,
			Score: round4(float64(c) * math.Log2(1+float64(c+1)/float64(p+1))),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Type+out[i].Target < out[j].Type+out[j].Target
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
