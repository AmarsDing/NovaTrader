package biz

import (
	"math"
	"time"
)

// Effective 是衰减到 now 的情感：情感 × exp(-ln2 × 经过时间 / 半衰期)。
func Effective(sentiment float64, halfLifeMinutes int, published, now time.Time) float64 {
	return sentiment * decay(halfLifeMinutes, published, now)
}

func decay(halfLifeMinutes int, published, now time.Time) float64 {
	if halfLifeMinutes <= 0 {
		return 1
	}
	elapsed := now.Sub(published).Minutes()
	if elapsed <= 0 {
		return 1
	}
	return math.Exp(-math.Ln2 * elapsed / float64(halfLifeMinutes))
}

// Composite 是个股综合情感：tanh(Σ 情感 × 重要度/5 × 衰减 × 关联置信度)，落在 [-1, 1]。
func Composite(inputs []SentimentInput, now time.Time) float64 {
	sum := 0.0
	for _, in := range inputs {
		sum += in.Sentiment * float64(in.Importance) / 5 * decay(in.HalfLifeMinutes, in.PublishTime, now) * in.Confidence
	}
	return round4(math.Tanh(sum))
}
