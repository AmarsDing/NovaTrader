package data

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	riskv1 "server/api/risk/v1"
	"server/pkg/admission"
)

func (c *RiskClient) ReadAdmissionRisk(ctx context.Context) (admission.Sample, error) {
	if c.addr == "" {
		return admission.Sample{}, fmt.Errorf("admission: risk addr not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.addr+"/risk/v1/state", nil)
	if err != nil {
		return admission.Sample{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return admission.Sample{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return admission.Sample{}, fmt.Errorf("admission: risk state unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return admission.Sample{}, err
	}
	var state riskv1.StateReply
	if err = (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &state); err != nil {
		return admission.Sample{}, fmt.Errorf("admission: invalid risk state")
	}
	// Optional readiness preserves unknown on legacy servers. Configuration
	// readiness alone never proves the order-checking engine is ready.
	s := admission.Sample{RiskReady: state.Ready, RiskMode: state.GetMode(), ConfigReady: state.ConfigReady}
	if state.KillSwitch != nil {
		active := state.KillSwitch.Active
		s.KillActive = &active
	}
	if state.ConfigReady != nil {
		revision := int(state.ConfigRevision)
		s.RiskConfigRevision = &revision
	}
	if value := state.AccountAsOf["SIM"]; value != "" {
		at, err := time.Parse(time.RFC3339Nano, value)
		if err == nil {
			s.RiskAccountAsOf = &at
		}
	}
	return s, nil
}
