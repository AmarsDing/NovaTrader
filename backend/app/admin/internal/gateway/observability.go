package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	v1 "server/api/admin/v1"
	"server/pkg/events"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/transport"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/protobuf/proto"
)

const observationSampleCapacity = 80
const observationEdgeCapacity = 128
const observationWindowSeconds = 60
const observationPayloadLimit = 64 << 10

var observationID = regexp.MustCompile(`^[A-Za-z0-9_.:/-]{1,128}$`)
var observationSymbol = regexp.MustCompile(`^(?:SH|SZ|BJ)?[0-9]{6}(?:[.](?:SH|SZ|BJ))?$`)
var observationSources = map[string]bool{"admin": true, "datahub": true, "market": true, "intel": true, "brain": true, "strategy": true, "backtest": true, "risk": true, "trade": true, "notify": true}
var observationEnums = map[string]bool{}

func init() {
	for _, value := range strings.Fields("SIM LIVE BUY SELL buy sell buy_limit sell_limit approved pending risk_checking executing done closed rejected expired cancelled accepted submitted partial filled new confirmed waiting unknown ok warning critical info error start complete failed degraded queued running technical news capital overseas dispatch collect analyze vote review select plan risk technical_analysis news_analysis capital_analysis overseas_analysis") {
		observationEnums[value] = true
	}
}

type observationBucket struct {
	second               int64
	count, bytes, errors uint64
}
type observationCounter struct {
	subject, source, topic            string
	received, accepted, bytes, errors uint64
	lastAt                            time.Time
	lastEvent, lastTrace              string
	buckets                           [observationWindowSeconds]observationBucket
}

type observationBinding struct {
	state string
	at    time.Time
}

type observationLedger struct {
	mu           sync.Mutex
	now          func() time.Time
	startedAt    time.Time
	epoch        string
	rows         map[string]*observationCounter
	samples      [observationSampleCapacity]*v1.ObservationSample
	next, length int
	lastEviction time.Time
	errors       uint64
	overflow     uint64
	bindings     map[string]observationBinding
}

func newObservationLedger(epoch string, now func() time.Time) *observationLedger {
	o := &observationLedger{now: now, startedAt: now().UTC(), epoch: epoch, rows: map[string]*observationCounter{}, bindings: map[string]observationBinding{}}
	o.rows["unregistered\x00unknown"] = &observationCounter{subject: "unregistered", source: "unknown", topic: "unknown"}
	return o
}
func knownObservationSubject(subject string) string {
	for _, route := range natsRoutes {
		if route.subject == subject {
			return subject
		}
	}
	return "unregistered"
}
func safeObservationID(value string) string {
	if observationID.MatchString(value) {
		return value
	}
	return ""
}
func observationTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
func optionalObservationTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return observationTime(*value)
}

func (o *observationLedger) countLocked(subject, source, topic string, size int, at time.Time, rejected bool) *observationCounter {
	subject = knownObservationSubject(subject)
	if !observationSources[source] {
		source = "unknown"
	}
	if _, ok := allowedTopics[topic]; !ok {
		topic = "unknown"
	}
	key := subject + "\x00" + source
	row := o.rows[key]
	if row == nil {
		if len(o.rows) >= observationEdgeCapacity {
			row = o.rows["unregistered\x00unknown"]
			o.overflow++
		} else {
			row = &observationCounter{subject: subject, source: source, topic: topic}
			o.rows[key] = row
		}
	}
	row.received++
	row.bytes += uint64(size)
	row.lastAt = at
	index := at.Unix() % observationWindowSeconds
	if index < 0 {
		index += observationWindowSeconds
	}
	bucket := &row.buckets[index]
	if bucket.second != at.Unix() {
		*bucket = observationBucket{second: at.Unix()}
	}
	bucket.count++
	bucket.bytes += uint64(size)
	if rejected {
		row.errors++
		bucket.errors++
		o.errors++
	} else {
		row.accepted++
	}
	return row
}
func (o *observationLedger) reject(subject, topic string, size int, at time.Time) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.countLocked(subject, "unknown", topic, size, at, true)
}
func (o *observationLedger) receive(env events.Envelope, topic string, size int, at time.Time) {
	if o == nil {
		return
	}
	payload, status := projectObservationPayload(env.Payload)
	sample := &v1.ObservationSample{ReceivedAt: observationTime(at), Subject: knownObservationSubject(env.Subject), Source: env.Source, Topic: topic,
		EventId: safeObservationID(env.EventID), TraceId: safeObservationID(env.TraceID), SchemaVersion: int32(env.SchemaVersion), CausationId: safeObservationID(env.CausationID), CorrelationId: safeObservationID(env.CorrelationID), EntityVersion: env.EntityVersion,
		EventTime: observationTime(env.Time), SourceTime: optionalObservationTime(env.SourceTime), PublisherReceivedAt: optionalObservationTime(env.ReceivedAt), PublishedAt: optionalObservationTime(env.PublishedAt), Payload: payload, PayloadStatus: status}
	if !observationSources[sample.Source] {
		sample.Source = "unknown"
	}
	if env.Source != "trade" && env.Source != "risk" {
		sample.EntityId = safeObservationID(env.EntityID)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	row := o.countLocked(env.Subject, env.Source, topic, size, at, false)
	row.lastEvent = sample.EventId
	row.lastTrace = sample.TraceId
	if o.length == observationSampleCapacity {
		o.lastEviction = at
	} else {
		o.length++
	}
	o.samples[o.next] = sample
	o.next = (o.next + 1) % observationSampleCapacity
}

// Projection is deny-by-default. Nested objects, free text and all account values are omitted.
func projectObservationPayload(raw json.RawMessage) (map[string]string, string) {
	out := map[string]string{}
	if len(raw) > observationPayloadLimit {
		return out, "oversized_omitted"
	}
	var object map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil || object == nil {
		return out, "non_object_omitted"
	}
	for _, key := range []string{"symbol", "stock_code", "code"} {
		if value, ok := object[key].(string); ok && observationSymbol.MatchString(value) {
			out[key] = value
		}
	}
	for _, key := range []string{"signal_id", "order_id", "client_order_id", "fill_id", "plan_id"} {
		if value, ok := object[key].(string); ok {
			if safe := safeObservationID(value); safe != "" {
				out[key] = safe
			}
		}
	}
	for _, key := range []string{"status", "side", "kind", "stage", "level", "account_type"} {
		if value, ok := object[key].(string); ok && observationEnums[value] {
			out[key] = value
		}
	}
	for _, key := range []string{"count", "seq"} {
		if value, ok := object[key].(json.Number); ok {
			if n, err := value.Int64(); err == nil && n >= 0 && n <= 1_000_000_000 {
				out[key] = strconv.FormatInt(n, 10)
			}
		}
	}
	if value, ok := object["stale"].(bool); ok {
		out["stale"] = strconv.FormatBool(value)
	}
	if value, ok := object["freq"].(string); ok && (value == "1m" || value == "1d" || value == "tick") {
		out["freq"] = value
	}
	if value, ok := object["as_of"].(string); ok {
		if at, err := time.Parse(time.RFC3339Nano, value); err == nil {
			out["as_of"] = observationTime(at)
		}
	}
	return out, "projected"
}

func observationCoverage() []string {
	return []string{"business_consumer_acknowledgement", "producer_publish_to_receive_latency_and_clock_skew", "full_host_process_inventory", "upstream_unsubscribed_subjects", "complete_event_history", "full_payload_or_account_values", "configuration_load_receipts", "latency_percentiles", "unique_business_event_count"}
}
func (o *observationLedger) binding(subject string, ok bool, at time.Time) {
	if knownObservationSubject(subject) == "unregistered" {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	state := "failed"
	if ok {
		state = "bound"
	}
	o.bindings[subject] = observationBinding{state: state, at: at}
}
func (o *observationLedger) registeredRoutesLocked() []*v1.ObservationRoute {
	out := make([]*v1.ObservationRoute, 0, len(natsRoutes))
	for _, route := range natsRoutes {
		kind := "jetstream"
		if route.core {
			kind = "nats_core"
		}
		binding, ok := o.bindings[route.subject]
		if !ok {
			binding.state = "declared"
		}
		out = append(out, &v1.ObservationRoute{Subject: route.subject, Topic: route.topic, Durable: route.durable, Transport: kind, Observation: "gateway_receive_only", BindingState: binding.state, BindingUpdatedAt: observationTime(binding.at)})
	}
	return out
}
func (o *observationLedger) snapshot(subject, trace string, limit int) *v1.GetObservabilityReply {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := o.now().UTC()
	start := now.Truncate(time.Second).Add(-59 * time.Second)
	out := &v1.GetObservabilityReply{SchemaVersion: 1, Available: true, Scope: "admin_gateway_ingress", ServerEpoch: o.epoch, AsOf: observationTime(now), StartedAt: observationTime(o.startedAt), WindowStart: observationTime(start), WindowSeconds: observationWindowSeconds, WindowBasis: "60_one_second_buckets_including_partial_current_second", RegisteredRoutes: o.registeredRoutesLocked(), ErrorsTotal: o.errors, SampleCapacity: observationSampleCapacity, CounterCapacity: observationEdgeCapacity, CounterOverflowTotal: o.overflow, StatisticsScope: "subject_source_counters;trace_id_filters_samples_only", Unsupported: observationCoverage(), Note: "Received counts are gateway ingress before coalescing; registration and connection do not prove business readiness."}
	out.WindowObservedSeconds = now.Sub(o.startedAt).Seconds()
	if out.WindowObservedSeconds < 0 {
		out.WindowObservedSeconds = 0
	}
	out.WindowComplete = out.WindowObservedSeconds >= 60
	if out.WindowObservedSeconds > 60 {
		out.WindowObservedSeconds = 60
	}
	if !o.lastEviction.IsZero() && !o.lastEviction.Before(start) {
		out.SamplesTruncated = true
	}
	for _, row := range o.rows {
		if row.received == 0 || (subject != "" && row.subject != subject) {
			continue
		}
		edge := &v1.ObservationEdge{Subject: row.subject, Source: row.source, Topic: row.topic, ReceivedTotal: row.received, AcceptedTotal: row.accepted, BytesTotal: row.bytes, ErrorsTotal: row.errors, LastReceivedAt: observationTime(row.lastAt), LastEventId: row.lastEvent, LastTraceId: row.lastTrace}
		for _, bucket := range row.buckets {
			if bucket.second >= start.Unix() && bucket.second <= now.Unix() {
				edge.WindowCount += bucket.count
				edge.WindowBytes += bucket.bytes
				edge.WindowErrors += bucket.errors
			}
		}
		out.Edges = append(out.Edges, edge)
	}
	sort.Slice(out.Edges, func(i, j int) bool {
		a, b := out.Edges[i], out.Edges[j]
		return a.Subject+"\x00"+a.Source < b.Subject+"\x00"+b.Source
	})
	for i := 0; i < o.length; i++ {
		sample := o.samples[(o.next-1-i+observationSampleCapacity)%observationSampleCapacity]
		at, err := time.Parse(time.RFC3339Nano, sample.ReceivedAt)
		if err != nil || at.Before(start) || at.After(now) || (subject != "" && sample.Subject != subject) || (trace != "" && sample.TraceId != trace) {
			continue
		}
		if len(out.Samples) >= limit {
			out.SamplesTruncated = true
			break
		}
		out.Samples = append(out.Samples, proto.Clone(sample).(*v1.ObservationSample))
	}
	return out
}

func (h *Hub) ObservationSnapshot(subject, trace string, limit int) *v1.GetObservabilityReply {
	out := h.observations.snapshot(subject, trace, limit)
	out.NatsConnected = h.Up()
	return out
}

type observabilityService struct{ gate *Gate }

func (s observabilityService) GetObservability(ctx context.Context, req *v1.GetObservabilityRequest) (*v1.GetObservabilityReply, error) {
	tr, ok := transport.FromServerContext(ctx)
	if !ok {
		return nil, errors.Unauthorized("AUTH_REQUIRED", "未登录")
	}
	request := &http.Request{Header: http.Header{"Authorization": []string{tr.RequestHeader().Get("Authorization")}}}
	if _, ok := s.gate.principal(request); !ok {
		return nil, errors.Unauthorized("AUTH_REQUIRED", "未登录")
	}
	subject, trace := strings.TrimSpace(req.Subject), strings.TrimSpace(req.TraceId)
	if (subject != "" && !observationID.MatchString(subject)) || (trace != "" && !observationID.MatchString(trace)) || req.Limit < 0 {
		return nil, errors.BadRequest("INVALID_FILTER", "subject/trace_id需为有限精确标识，limit不能为负")
	}
	limit := int(req.Limit)
	if limit == 0 || limit > observationSampleCapacity {
		limit = observationSampleCapacity
	}
	if s.gate.Observations == nil {
		return &v1.GetObservabilityReply{SchemaVersion: 1, Scope: "admin_gateway_ingress", AsOf: observationTime(s.gate.now()), WindowSeconds: 60, SampleCapacity: 80, Unsupported: observationCoverage(), Note: "Gateway observation hub is not connected; metrics are unavailable."}, nil
	}
	return s.gate.Observations.ObservationSnapshot(subject, trace, limit), nil
}

// Delegate to a generated Kratos HTTP binding, keeping the existing Gate auth entrance.
func (g *Gate) serveObservability(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErr(w, http.StatusMethodNotAllowed, "观测接口只读")
		return
	}
	g.observationOnce.Do(func() {
		server := khttp.NewServer()
		v1.RegisterObservabilityHTTPServer(server, observabilityService{gate: g})
		g.observationHandler = server
	})
	g.observationHandler.ServeHTTP(w, r)
}
