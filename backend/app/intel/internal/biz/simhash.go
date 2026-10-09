package biz

import (
	"hash/fnv"
	"math/bits"
	"sync"
	"time"
)

// SimHash 用字符二元组算 64 位指纹。返回值和参与计算的字数。
func SimHash(s string) (uint64, int) {
	rs := textRunes(s)
	if len(rs) == 0 {
		return 0, 0
	}
	if len(rs) == 1 {
		return hashRunes(rs), 1
	}
	var v [64]int
	for i := 0; i+1 < len(rs); i++ {
		h := hashRunes(rs[i : i+2])
		for b := 0; b < 64; b++ {
			if h&(1<<uint(b)) != 0 {
				v[b]++
			} else {
				v[b]--
			}
		}
	}
	var out uint64
	for b := 0; b < 64; b++ {
		if v[b] > 0 {
			out |= 1 << uint(b)
		}
	}
	return out, len(rs)
}

func hashRunes(rs []rune) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(string(rs)))
	return h.Sum64()
}

func Hamming(a, b uint64) int {
	return bits.OnesCount64(a ^ b)
}

// 少于 shortText 字的文本特征太少，近似去重收紧到 shortTextDist。
const (
	shortText     = 30
	shortTextDist = 2
)

// Index 是近似去重的内存索引，只保留窗口内的指纹。
type Index struct {
	mu      sync.Mutex
	window  time.Duration
	maxDist int
	items   []Fingerprint
}

func NewIndex(window time.Duration, maxDist int) *Index {
	return &Index{window: window, maxDist: maxDist}
}

// Find 返回窗口内最相近的簇。两边都有源头代码且没有交集时不合并，
// 防止不同公司套同一模板的公告被并成一簇。没有足够相近的返回 0。
func (x *Index) Find(hash uint64, runeLen int, codes []string, now time.Time) int {
	x.mu.Lock()
	defer x.mu.Unlock()
	since := now.Add(-x.window)
	best, bestDist := 0, 65
	for _, fp := range x.items {
		if fp.At.Before(since) || fp.ClusterID == 0 {
			continue
		}
		limit := x.maxDist
		if runeLen < shortText || fp.RuneLen < shortText {
			limit = min(limit, shortTextDist)
		}
		d := Hamming(hash, fp.SimHash)
		if d > limit || d >= bestDist || disjoint(codes, fp.Codes) {
			continue
		}
		best, bestDist = fp.ClusterID, d
	}
	return best
}

func disjoint(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return false
			}
		}
	}
	return true
}

// Add 记下一个指纹，并丢掉窗口外的旧项。
func (x *Index) Add(fp Fingerprint, now time.Time) {
	x.mu.Lock()
	defer x.mu.Unlock()
	since := now.Add(-x.window)
	kept := x.items[:0]
	for _, old := range x.items {
		if !old.At.Before(since) {
			kept = append(kept, old)
		}
	}
	if fp.At.Before(since) {
		x.items = kept
		return
	}
	x.items = append(kept, fp)
}

func (x *Index) Len() int {
	x.mu.Lock()
	defer x.mu.Unlock()
	return len(x.items)
}
