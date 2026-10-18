package observe

import (
	"fmt"
	"math"
	"regexp"
	"time"

	v1 "server/api/platform/v1"

	"github.com/google/uuid"
)

var hostLabel = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

// ValidateSnapshot checks the bounded, compiled registry contract before admin forwards it.
// Unknown service versions and arbitrary labels fail closed instead of leaking upstream text.
func ValidateSnapshot(service string, in *v1.ConsumerObservation) error {
	defs, ok := definitions[service]
	if !ok || in == nil || in.SchemaVersion != 1 || in.Scope != "process_consumer_operations" || in.Service != service {
		return fmt.Errorf("observe: unsupported identity or version")
	}
	if _, err := uuid.Parse(in.InstanceId); err != nil {
		return fmt.Errorf("observe: invalid instance")
	}
	if !hostLabel.MatchString(in.Host) || !hostLabel.MatchString(in.Os) || !hostLabel.MatchString(in.Arch) || !validStamp(in.StartedAt, false) || !validStamp(in.SampledAt, false) {
		return fmt.Errorf("observe: invalid process metadata")
	}
	if !finitePositive(in.UptimeSeconds) || in.WindowSeconds != WindowSeconds || !finitePositive(in.WindowObservedSeconds) || in.WindowObservedSeconds > WindowSeconds || in.WindowObservedSeconds > in.UptimeSeconds || in.WindowComplete != (in.UptimeSeconds >= WindowSeconds) || in.WindowBasis != "60_one_second_buckets_including_partial_current_second;duration_at_completion" {
		return fmt.Errorf("observe: invalid window")
	}
	if len(in.Operations) != len(defs) || len(in.Unsupported) > 32 {
		return fmt.Errorf("observe: invalid operation count")
	}
	expected := make(map[string]definition, len(defs))
	for _, d := range defs {
		expected[d.key] = d
	}
	for _, op := range in.Operations {
		if op == nil {
			return fmt.Errorf("observe: nil operation")
		}
		d, exists := expected[op.Key]
		if !exists || op.Kind != d.kind || op.Input != d.input || op.CompletionScope != d.completion || !validCounters(op.Total) || !validCounters(op.Window) || !validStamp(op.LastReceivedAt, true) || !validStamp(op.LastStartedAt, true) || !validStamp(op.LastCompletedAt, true) {
			return fmt.Errorf("observe: invalid operation")
		}
		remaining := op.Total.Attempted
		for _, count := range []uint64{op.Total.Succeeded, op.Total.Rejected, op.Total.Failed, op.Total.Cancelled, op.Total.Skipped} {
			if count > remaining {
				return fmt.Errorf("observe: inconsistent completions")
			}
			remaining -= count
		}
		if remaining != op.Inflight || op.Total.DurationCount != op.Total.Attempted-remaining {
			return fmt.Errorf("observe: inconsistent inflight")
		}
		delete(expected, op.Key)
	}
	// The server contract uses a closed set; do not forward arbitrary upstream strings.
	in.Unsupported = []string{"broker_ack_confirmation", "durable_pending_and_redelivery", "unique_business_event_count", "cross_host_clock_skew", "end_to_end_latency", "unregistered_operations", "host_cpu_and_disk_utilization"}
	return nil
}

func validStamp(raw string, optional bool) bool {
	if raw == "" {
		return optional
	}
	if len(raw) > 64 {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, raw)
	return err == nil
}
func finitePositive(n float64) bool { return n >= 0 && !math.IsNaN(n) && !math.IsInf(n, 0) }
func validCounters(in *v1.ConsumerCounters) bool {
	if in == nil || !finitePositive(in.DurationTotalMs) || !finitePositive(in.DurationMaxMs) {
		return false
	}
	if in.DurationP95UpperMs != nil {
		if in.DurationCount == 0 {
			return false
		}
		found := false
		for _, b := range bounds {
			found = found || *in.DurationP95UpperMs == b
		}
		if !found {
			return false
		}
	}
	return true
}
