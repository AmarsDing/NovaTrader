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

// Expired 判断快照是否不能用于计算：上游标记过期，或写入时间距 now 超过 maxAge。
func (s Snapshot) Expired(now time.Time, maxAge time.Duration) bool {
	if s.Stale {
		return true
	}
	if maxAge <= 0 || s.AsOf.IsZero() {
		return false
	}
	return now.Sub(s.AsOf) > maxAge
}
