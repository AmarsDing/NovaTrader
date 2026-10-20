package biz

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"server/pkg/events"
)

// 同一水位之前多回看一点，防止同一时刻的条目分批到达时漏掉。M03 按 (source, source_id) 去重。
const intelOverlap = 2 * time.Minute

type seenSet struct {
	mu sync.Mutex
	m  map[string]time.Time
}

func (s *seenSet) has(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.m[key]
	return ok
}

func (s *seenSet) add(key string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]time.Time{}
	}
	s.m[key] = now
	if len(s.m) > 20000 {
		for k, t := range s.m {
			if now.Sub(t) > 24*time.Hour {
				delete(s.m, k)
			}
		}
	}
}

var intelSeen seenSet

// IntelKind 把数据域映射到 M03 的情报类别。
func IntelKind(domain string) (string, bool) {
	switch domain {
	case DomainFlash:
		return KindFlash, true
	case DomainNews:
		return KindNews, true
	case DomainAnnouncement:
		return KindAnnouncement, true
	case DomainReport:
		return KindReport, true
	}
	return "", false
}

// CollectIntel 增量拉取一类情报，逐条发 intel.raw.<kind>（JetStream）。
// 事件发出后才前移水位；中途失败时水位停在最后一条成功的时间。
func (c *Collector) CollectIntel(ctx context.Context, domain string) (CollectSummary, error) {
	kind, ok := IntelKind(domain)
	if !ok {
		return CollectSummary{}, fmt.Errorf("datahub: %s is not an intel domain", domain)
	}
	if c.bus == nil {
		return CollectSummary{Domain: domain}, fmt.Errorf("datahub: no event bus")
	}
	cursorName := "intel:" + domain
	since := c.now().Add(-24 * time.Hour)
	if v, err := c.repo.Cursor(ctx, cursorName); err == nil && v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			since = t
		}
	}
	items, src, err := Run(ctx, c.p, domain, RunOptions{Timeout: 30 * time.Second},
		func(ctx context.Context, s Source) ([]Intel, error) {
			return s.(IntelSource).Intel(ctx, kind, IntelQuery{Since: since.Add(-intelOverlap), Limit: 200})
		}, nil)
	if err != nil {
		return CollectSummary{Domain: domain}, err
	}
	// B4：report 域先把盈利预测落 report_estimate（PIT 行级 upsert），再发事件。
	// 落库失败即整轮失败：事件不发、水位不前移，下一轮重采重试（幂等无重复行）。
	if domain == DomainReport {
		if est := EstimateRowsOf(items); len(est) > 0 {
			rd := ready("report_estimate", time.Time{}, len(est), src, false)
			if _, err := c.repo.UpsertReportEstimates(ctx, est, src, rd); err != nil {
				return CollectSummary{Domain: domain}, fmt.Errorf("datahub: upsert report estimates: %w", err)
			}
		}
	}
	type timed struct {
		Intel
		at time.Time
	}
	list := make([]timed, 0, len(items))
	for _, it := range items {
		at, err := time.Parse(time.RFC3339, it.PublishTime)
		if err != nil {
			at = c.now()
			it.PublishTime = ""
		}
		list = append(list, timed{Intel: it, at: at})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].at.Before(list[j].at) })
	newest := since
	sent := 0
	var pubErr error
	for _, it := range list {
		if it.at.Before(since.Add(-intelOverlap)) || it.SourceID == "" || it.Title == "" {
			continue
		}
		key := domain + "|" + it.Source + "|" + it.SourceID
		if intelSeen.has(key) {
			continue
		}
		it.Kind = kind
		env, err := events.New(serviceName, events.SubjectIntelRaw(kind), events.TraceID(ctx), it.Intel)
		if err != nil {
			pubErr = err
			break
		}
		if err := c.bus.Publish(ctx, env); err != nil {
			pubErr = err
			break
		}
		intelSeen.add(key, c.now())
		sent++
		if it.at.After(newest) {
			newest = it.at
		}
	}
	if newest.After(since) {
		if err := c.repo.SetCursor(ctx, cursorName, newest.UTC().Format(time.RFC3339)); err != nil {
			c.log.Warnf("cursor %s: %v", cursorName, err)
		}
	}
	res := CollectSummary{Domain: domain, Source: src, Rows: sent}
	if pubErr != nil {
		return res, fmt.Errorf("datahub: publish %s: %w", kind, pubErr)
	}
	return res, nil
}
