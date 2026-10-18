// Package market 是 M02 的纯计算部分：1 分钟 K 线、指标、涨停生态、情绪、板块、资金、外围、竞价。
// 不读库、不连网，实盘服务和 M07 回测共用同一份实现。口径见 doc/开发文档/M02-行情与因子。
package market

import (
	"encoding/json"
	"fmt"
	"time"
)

// SnapshotKey 是 datahub 写最新快照的 Redis Hash。field 为证券代码。
const SnapshotKey = "snap:latest"

// Snapshot 是 M01 写入的单只股票快照。Volume（股）和 Amount（元）是当日累计值。
// Bid、Ask 每档为 [价格, 股数]。
type Snapshot struct {
	Symbol   string       `json:"symbol"`
	Time     time.Time    `json:"time"`
	AsOf     time.Time    `json:"as_of"`
	Source   string       `json:"source"`
	Stale    bool         `json:"stale"`
	PreClose float64      `json:"pre_close"`
	Open     float64      `json:"open"`
	High     float64      `json:"high"`
	Low      float64      `json:"low"`
	Last     float64      `json:"last"`
	Volume   int64        `json:"volume"`
	Amount   float64      `json:"amount"`
	Bid      [][2]float64 `json:"bid"`
	Ask      [][2]float64 `json:"ask"`
}

// ParseSnapshot 解析一条快照。缺代码或时间的快照无法归入 K 线，直接拒绝。
func ParseSnapshot(b []byte) (Snapshot, error) {
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return Snapshot{}, fmt.Errorf("market: snapshot: %w", err)
	}
	if s.Symbol == "" || s.Time.IsZero() {
		return Snapshot{}, fmt.Errorf("market: snapshot missing symbol or time")
	}
	return s, nil
}

// Bid1 返回买一价和买一量。没有买盘时量为 0。
func (s Snapshot) Bid1() (float64, int64) { return level(s.Bid) }

// Ask1 返回卖一价和卖一量。没有卖盘时量为 0。
func (s Snapshot) Ask1() (float64, int64) { return level(s.Ask) }

func level(book [][2]float64) (float64, int64) {
	if len(book) == 0 {
		return 0, 0
	}
	return book[0][0], int64(book[0][1])
}

// FreshnessTime 返回行情和写入时间中较早的一个。写入新批次不能更新源行情的年龄。
// 源时间缺失时返回零值；没有写入时间的旧格式快照仍使用其源时间。
func (s Snapshot) FreshnessTime() time.Time {
	if s.Time.IsZero() {
		return time.Time{}
	}
	if !s.AsOf.IsZero() && s.AsOf.Before(s.Time) {
		return s.AsOf
	}
	return s.Time
}

// Expired 判断快照是否不能用于计算。源行情时间和写入时间都须有效，
// 未成交/停牌的旧价可供展示，但不能通过刷新 AsOf 获得新的计算或交易资格。
func (s Snapshot) Expired(now time.Time, maxAge time.Duration) bool {
	if s.Stale || s.Time.IsZero() {
		return true
	}
	if s.Time.After(now.Add(time.Minute)) || s.AsOf.After(now.Add(time.Minute)) {
		return true
	}
	if maxAge <= 0 {
		return false
	}
	return now.Sub(s.FreshnessTime()) > maxAge
}
