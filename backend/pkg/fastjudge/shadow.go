package fastjudge

import (
	"context"
	"server/pkg/metrics"
	"sync"
	"sync/atomic"
	"time"
)

var submitted = metrics.Counter("novatrader_fastjudge_submitted_total", "shadow jobs admitted")
var dropped = metrics.Counter("novatrader_fastjudge_dropped_total", "shadow jobs rejected at capacity")
var abstained = metrics.Counter("novatrader_fastjudge_abstained_total", "shadow judgments abstained")
var auditFailed = metrics.Counter("novatrader_fastjudge_audit_failures_total", "shadow audit writes failed")
var latency = metrics.Gauge("novatrader_fastjudge_last_duration_ms", "last completed shadow duration")

// Shadow has bounded concurrency and no waiting queue. A slow provider cannot
// back-pressure the scoring/alert transaction. Only the audit sink receives output.
type Shadow struct {
	judge         Evaluator
	sink          AuditSink
	slots         chan struct{}
	wg            sync.WaitGroup
	Submitted     atomic.Uint64
	Dropped       atomic.Uint64
	AuditFailures atomic.Uint64
}

func NewShadow(judge Evaluator, sink AuditSink, concurrency int) *Shadow {
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > 4 {
		concurrency = 4
	}
	return &Shadow{judge: judge, sink: sink, slots: make(chan struct{}, concurrency)}
}
func (s *Shadow) Submit(req Request) bool {
	if s == nil || s.judge == nil || s.sink == nil {
		return false
	}
	select {
	case s.slots <- struct{}{}:
	default:
		s.Dropped.Add(1)
		dropped.Inc()
		return false
	}
	s.Submitted.Add(1)
	submitted.Inc()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() { <-s.slots }()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result := s.judge.Judge(ctx, req)
		latency.Set(float64(result.DurationMS))
		if result.AbstainReason != "" {
			abstained.Inc()
		}
		actx, acancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer acancel()
		if err := s.sink.Record(actx, Audit{TraceID: req.TraceID, Result: result}); err != nil {
			s.AuditFailures.Add(1)
			auditFailed.Inc()
		}
	}()
	return true
}
func (s *Shadow) Wait() {
	if s != nil {
		s.wg.Wait()
	}
}
