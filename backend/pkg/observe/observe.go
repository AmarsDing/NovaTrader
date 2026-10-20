// Package observe records bounded process-local operations, not broker or business acknowledgements.
// Labels are compiled into the registry; event IDs, securities and payloads never enter it.
package observe

import (
	"context"
	"errors"
	"os"
	"runtime"
	"sort"
	"sync"
	"time"

	v1 "server/api/platform/v1"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/google/uuid"
)

const WindowSeconds = 60

type definition struct{ key, kind, input, completion string }

var definitions = map[string][]definition{
	"datahub": {
		{"collect_request", "business", "run_domain_rpc_or_scheduler_or_backfill", "run_domain_returned_nil_not_all_quality_or_publish_results"},
		{"snapshot_collect", "business", "snapshot_timer_or_run_domain", "validated_snapshot_written_to_redis_not_all_symbols_fresh_or_nats_publish_confirmed"},
		{"scheduled_collect", "business", "enabled_intraday_or_intel_schedule", "scheduled_collector_returned_nil_not_all_quality_or_publish_results"},
		{"health_persist", "infrastructure", "health_timer", "source_health_persistence_returned_success_not_source_healthy"},
		{"outbox", "infrastructure", "dispatch_timer", "outbox_dispatch_returned_success_not_downstream_ack"},
	},
	"trade": {
		{"minute_notice", "notification", "market.bar.1m", "validated_market_bar_envelope_only_not_ledger_or_broker_ack"},
		{"minute_bar", "business", "validated_source_minute", "source_minute_cursor_and_ledger_committed_not_guaranteed_fill"},
		{"risk_exit", "business", "risk.exit", "local_exit_order_record_committed_not_broker_ack"},
		{"plan_tick", "infrastructure", "plan_timer", "conditional_plan_tick_returned_nil_internal_waits_are_not_execution"},
		{"plan_execute", "business", "conditional_timer_or_manual", "conditional_execution_transaction_outcome_not_guaranteed_fill"},
		{"snapshot", "business", "startup_or_snapshot_timer", "complete_sim_snapshot_transaction_committed"},
		{"intent_recovery", "infrastructure", "startup_or_snapshot_timer", "original_intent_reconciliation_pass_returned_success_not_resubmission"},
		{"outbox", "infrastructure", "dispatch_timer", "outbox_dispatch_returned_success_not_downstream_ack"},
		{"confirm_expiry", "infrastructure", "dispatch_timer", "confirmation_expiry_pass_returned_success"},
		{"watch_sweep", "infrastructure", "dispatch_timer", "watch_order_sweep_pass_returned_success_not_guaranteed_exit"},
		{"paper_evidence", "business", "evidence_timer", "collection_and_store_returned_success_not_day_qualification_or_unique_insert"},
		{"market_lease", "notification", "lease_timer", "local_nats_publish_returned_success_not_lease_consumer_ack"},
	},
	"backtest": {
		{"task_claim", "infrastructure", "worker_poll", "task_claim_returned_ownership_not_task_result"},
		{"run", "business", "claimed_backtest_task", "task_execution_outcome_not_profit_or_admission"},
		{"recovery", "infrastructure", "startup", "running_task_recovery_pass_returned_success_not_task_success"},
		{"research_outbox", "infrastructure", "research_poll", "research_outbox_dispatch_returned_success_not_downstream_ack"},
		{"research_recovery", "infrastructure", "research_poll", "research_lease_recovery_pass_returned_success_not_research_success"},
		{"research_claim", "infrastructure", "research_poll", "research_claim_returned_ownership_not_worker_result"},
		{"research_execute", "business", "claimed_research_job", "worker_result_integrity_and_execution_status_not_promotion_or_profit"},
		{"research_run", "business", "claimed_research_job", "authoritative_terminal_job_outcome_unknown_skipped_not_profit_or_promotion"},
		{"research_finish", "infrastructure", "worker_return", "finish_callback_returned_nil_not_state_change_or_research_success"},
	},
	"market": {
		{"snapshot_notice", "notification", "market.snapshot", "local_refresh_notification_only"},
		{"refresh", "business", "snapshot_notice_or_refresh_timer", "market_refresh_returned_nil_not_all_persistence_or_publish_results"},
		{"warmup", "business", "startup_or_scheduler", "market_warmup_returned_success"},
	},
	"intel": {
		{"raw", "business", "intel.raw.>", "intel_process_returned_success_not_broker_ack"},
		{"outbox", "infrastructure", "dispatch_timer", "outbox_dispatch_returned_success"},
		{"dictionary", "infrastructure", "dictionary_timer", "dictionary_reload_returned_success"},
	},
	"risk": {
		{"account_restore", "business", "startup", "authoritative_account_restore_returned_success_not_broker_connectivity"},
		{"account_consume", "business", "trade.account", "account_snapshot_consume_returned_success_not_broker_ack"},
		{"config_reload", "infrastructure", "poll_timer", "risk_config_reload_returned_success_not_order_approval"},
		{"outbox", "infrastructure", "dispatch_timer", "outbox_dispatch_returned_success"},
	},
	"brain": {
		{"analyze", "business", "analyze_rpc_or_batch", "analysis_persisted_fallback_skipped_discard_rejected_not_prediction_accuracy"},
		{"briefing", "business", "briefing_rpc_or_scheduler", "briefing_persisted_fallback_skipped_not_model_availability_or_prediction_accuracy"},
		{"explain", "business", "explain_rpc", "explanation_persisted_fallback_skipped_discard_rejected_not_prediction_accuracy"},
		{"ask", "business", "ask_rpc", "answer_persisted_fallback_skipped_discard_rejected_not_prediction_accuracy"},
		{"outbox", "infrastructure", "dispatch_timer", "outbox_dispatch_returned_success"},
	},
	"notify": {
		{"recover", "business", "startup", "notification_held_state_restore_returned_success"},
		{"message_handle", "business", "registered_notification_subjects", "notification_pipeline_returned_success_not_channel_delivery_or_user_read"},
		{"held_flush", "business", "flush_timer_or_session_changed", "notification_flush_returned_success_not_channel_delivery_or_user_read"},
	},
	"strategy": {
		{"market_alert_notice", "notification", "market.alert", "local_symbol_queue_only_not_scan"},
		{"kill_switch", "notification", "sys.killswitch", "local_kill_switch_state_applied_not_order_result"},
		{"scan", "business", "scheduler_or_scan_timer_or_market_alert", "strategy_scan_returned_success_not_trade"},
		{"exit_scan", "business", "scan_timer", "exit_scan_returned_success_not_trade"},
		{"outbox", "infrastructure", "dispatch_timer", "outbox_dispatch_returned_success"},
		{"expire", "business", "expiry_timer", "signal_expiry_returned_success"},
		{"outcomes", "business", "scheduler", "outcome_backfill_returned_success"},
		{"execution_projection", "business", "trade.order_or_trade.fill_or_recovery_timer", "authoritative_order_or_bounded_recovery_page_projection_returned_success_not_broker_ack"},
		{"closure_projection", "business", "closure_recovery_timer", "ledger_window_attribution_closed_signals_or_skipped_not_manual_closed_writes"},
	},
}

// RegisteredService reports the compiled closed registry, not a running process.
func RegisteredService(service string) bool { _, ok := definitions[service]; return ok }

var processes = struct {
	sync.Mutex
	items map[string]*Registry
}{items: make(map[string]*Registry)}

// ForService shares one instance between the service's worker and HTTP endpoint.
// An unregistered service is a programmer error, never an unbounded new label.
func ForService(service string) *Registry {
	processes.Lock()
	defer processes.Unlock()
	if r := processes.items[service]; r != nil {
		return r
	}
	defs, ok := definitions[service]
	if !ok {
		panic("observe: unregistered service")
	}
	r := newRegistry(service, defs, time.Now)
	processes.items[service] = r
	return r
}

type Outcome uint8

const (
	Succeeded Outcome = iota
	Rejected
	Failed
	Cancelled
	Skipped
)

// Fixed histogram boundaries give a P95 upper bound, not a fabricated exact quantile.
var bounds = [...]float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000, 60000}

type counts struct {
	received, queued, coalesced, attempted, succeeded, rejected, failed, cancelled, skipped uint64
	durationCount                                                                           uint64
	durationTotal, durationMax                                                              float64
	hist                                                                                    [len(bounds) + 1]uint64
}
type bucket struct {
	second int64
	counts counts
}
type Operation struct {
	registry                                 *Registry
	def                                      definition
	total                                    counts
	buckets                                  [WindowSeconds]bucket
	inflight                                 uint64
	lastReceived, lastStarted, lastCompleted time.Time
}
type Registry struct {
	mu                      sync.Mutex
	service, instance, host string
	started                 time.Time
	now                     func() time.Time
	ops                     map[string]*Operation
}

func newRegistry(service string, defs []definition, now func() time.Time) *Registry {
	host, _ := os.Hostname()
	r := &Registry{service: service, instance: uuid.NewString(), host: host, started: now(), now: now, ops: map[string]*Operation{}}
	for _, def := range defs {
		r.ops[def.key] = &Operation{registry: r, def: def}
	}
	return r
}
func (r *Registry) Op(key string) *Operation {
	if o := r.ops[key]; o != nil {
		return o
	}
	panic("observe: unregistered operation")
}
func (o *Operation) bucket(at time.Time) *counts {
	i := at.Unix() % WindowSeconds
	if i < 0 {
		i += WindowSeconds
	}
	b := &o.buckets[i]
	if b.second != at.Unix() {
		*b = bucket{second: at.Unix()}
	}
	return &b.counts
}
func (o *Operation) Received() {
	r := o.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	o.total.received++
	o.bucket(now).received++
	o.lastReceived = now
}
func (o *Operation) Queued() {
	r := o.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	o.total.queued++
	o.bucket(r.now()).queued++
}
func (o *Operation) Coalesced() {
	r := o.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	o.total.coalesced++
	o.bucket(r.now()).coalesced++
}

// Begin starts actual execution. Finish is idempotent even across concurrent defers.
func (o *Operation) Begin() func(Outcome) {
	r := o.registry
	r.mu.Lock()
	started := r.now()
	o.inflight++
	o.total.attempted++
	o.bucket(started).attempted++
	o.lastStarted = started
	r.mu.Unlock()
	var once sync.Once
	return func(outcome Outcome) {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			now := r.now()
			elapsed := now.Sub(started).Seconds() * 1000
			if elapsed < 0 {
				elapsed = 0
			}
			o.inflight--
			o.lastCompleted = now
			for _, c := range []*counts{&o.total, o.bucket(now)} {
				switch outcome {
				case Succeeded:
					c.succeeded++
				case Rejected:
					c.rejected++
				case Cancelled:
					c.cancelled++
				case Skipped:
					c.skipped++
				default:
					c.failed++
				}
				c.durationCount++
				c.durationTotal += elapsed
				if elapsed > c.durationMax {
					c.durationMax = elapsed
				}
				i := sort.SearchFloat64s(bounds[:], elapsed)
				c.hist[i]++
			}
		})
	}
}
func FinishError(finish func(Outcome), err error) {
	switch {
	case err == nil:
		finish(Succeeded)
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		finish(Cancelled)
	default:
		finish(Failed)
	}
}
func stamp(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(time.RFC3339Nano)
}
func add(dst *counts, src counts) {
	dst.received += src.received
	dst.queued += src.queued
	dst.coalesced += src.coalesced
	dst.attempted += src.attempted
	dst.succeeded += src.succeeded
	dst.rejected += src.rejected
	dst.failed += src.failed
	dst.cancelled += src.cancelled
	dst.skipped += src.skipped
	dst.durationCount += src.durationCount
	dst.durationTotal += src.durationTotal
	if src.durationMax > dst.durationMax {
		dst.durationMax = src.durationMax
	}
	for i, n := range src.hist {
		dst.hist[i] += n
	}
}
func project(c counts) *v1.ConsumerCounters {
	out := &v1.ConsumerCounters{Received: c.received, Queued: c.queued, Coalesced: c.coalesced, Attempted: c.attempted, Succeeded: c.succeeded, Rejected: c.rejected, Failed: c.failed, Cancelled: c.cancelled, Skipped: c.skipped, DurationCount: c.durationCount, DurationTotalMs: c.durationTotal, DurationMaxMs: c.durationMax}
	if c.durationCount > 0 {
		target := (c.durationCount*95 + 99) / 100
		var seen uint64
		for i, n := range c.hist {
			seen += n
			if seen >= target {
				if i < len(bounds) {
					v := bounds[i]
					out.DurationP95UpperMs = &v
				}
				break
			}
		}
	}
	return out
}
func (r *Registry) Snapshot() *v1.ConsumerObservation {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	uptime := now.Sub(r.started).Seconds()
	if uptime < 0 {
		uptime = 0
	}
	covered := uptime
	if covered > WindowSeconds {
		covered = WindowSeconds
	}
	out := &v1.ConsumerObservation{SchemaVersion: 1, Scope: "process_consumer_operations", Service: r.service, InstanceId: r.instance, Host: r.host, Os: runtime.GOOS, Arch: runtime.GOARCH, StartedAt: stamp(r.started), SampledAt: stamp(now), UptimeSeconds: uptime, WindowSeconds: WindowSeconds, WindowObservedSeconds: covered, WindowComplete: uptime >= WindowSeconds, WindowBasis: "60_one_second_buckets_including_partial_current_second;duration_at_completion", Goroutines: uint64(runtime.NumGoroutine()), HeapAllocBytes: mem.Alloc, RuntimeSysBytes: mem.Sys, LogicalCpus: uint32(runtime.NumCPU()), Gomaxprocs: uint32(runtime.GOMAXPROCS(0)), Unsupported: []string{"broker_ack_confirmation", "durable_pending_and_redelivery", "unique_business_event_count", "cross_host_clock_skew", "end_to_end_latency", "unregistered_operations", "host_cpu_and_disk_utilization"}}
	for _, o := range r.ops {
		var win counts
		for _, b := range o.buckets {
			if b.second >= now.Unix()-59 && b.second <= now.Unix() {
				add(&win, b.counts)
			}
		}
		out.Operations = append(out.Operations, &v1.ConsumerOperation{Key: o.def.key, Kind: o.def.kind, Input: o.def.input, CompletionScope: o.def.completion, Total: project(o.total), Window: project(win), Inflight: o.inflight, LastReceivedAt: stamp(o.lastReceived), LastStartedAt: stamp(o.lastStarted), LastCompletedAt: stamp(o.lastCompleted)})
	}
	sort.Slice(out.Operations, func(i, j int) bool { return out.Operations[i].Key < out.Operations[j].Key })
	return out
}

type service struct{ r *Registry }

func (s service) GetConsumerObservation(context.Context, *v1.GetConsumerObservationRequest) (*v1.ConsumerObservation, error) {
	return s.r.Snapshot(), nil
}

// RegisterHTTP uses the generated proto binding. Only admin should expose this upstream endpoint to clients.
func RegisterHTTP(server *khttp.Server, r *Registry) {
	v1.RegisterConsumerObservabilityHTTPServer(server, service{r})
}
