package biz

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/google/wire"
	"server/pkg/backtest"
	"server/pkg/dataset"
	"server/pkg/observe"
)

var ResearchProviderSet = wire.NewSet(NewResearchSettings, NewResearchUsecase)

const ResearchSubject = "sys.research"

type ResearchSettings struct {
	Enabled              bool
	Python, Script, Root string
}

func NewResearchSettings() ResearchSettings {
	return ResearchSettings{Enabled: os.Getenv("NT_RESEARCH_ENABLED") == "true", Python: os.Getenv("NT_RESEARCH_PYTHON"), Script: os.Getenv("NT_RESEARCH_SCRIPT"), Root: os.Getenv("NT_RESEARCH_ROOT")}
}

type ResearchRequest struct {
	Kind, ManifestID, InputJSON, OptionsJSON string
	AsOf                                     time.Time
	Seed                                     int64
	TimeoutSeconds                           int
}
type ResearchJob struct {
	RunID, Kind, ManifestID, ManifestHash, InputJSON, OptionsJSON, InputHash, CreatedBy string
	AsOf, CreatedAt                                                                     time.Time
	StartedAt, FinishedAt, LeaseUntil                                                   *time.Time
	Seed, Revision                                                                      int64
	TimeoutSeconds                                                                      int
	Status, Reason, Owner, ResultJSON, ArtifactHash                                     string
	CancelRequested                                                                     bool
}
type ResearchResult struct{ Status, Reason, JSON, ArtifactHash string }
type ResearchArtifact struct {
	Name, SHA256 string
	Content      []byte
}
type ResearchRepo interface {
	CreateResearch(context.Context, *ResearchJob) (*ResearchJob, error)
	GetResearch(context.Context, string) (*ResearchJob, error)
	ListResearch(context.Context, string, int, int) ([]*ResearchJob, error)
	ClaimResearch(context.Context, string, time.Time) (*ResearchJob, error)
	FinishResearch(context.Context, string, string, ResearchResult) error
	CancelResearch(context.Context, string) (*ResearchJob, error)
	RecoverResearch(context.Context, time.Time) error
	DispatchResearch(context.Context) error
}
type ResearchExecutor interface {
	Execute(context.Context, *ResearchJob, *dataset.Manifest) ResearchResult
	Artifact(context.Context, *ResearchJob, string) (ResearchArtifact, error)
}
type ResearchUsecase struct {
	repo     ResearchRepo
	source   backtest.Source
	exec     ResearchExecutor
	settings ResearchSettings
	owner    string
}

func NewResearchUsecase(repo ResearchRepo, source backtest.Source, exec ResearchExecutor, settings ResearchSettings) *ResearchUsecase {
	return &ResearchUsecase{repo: repo, source: source, exec: exec, settings: settings, owner: uuid.NewString()}
}
func (uc *ResearchUsecase) Enabled() bool { return uc.settings.Enabled }
func ResearchHash(body []byte) string     { sum := sha256.Sum256(body); return hex.EncodeToString(sum[:]) }

var researchID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)
var forbiddenResearchKey = regexp.MustCompile(`(?i)api.?key|password|secret|credential|account.?id|broker`)

func validateResearchJSON(raw string) (map[string]any, error) {
	if len(raw) == 0 || len(raw) > 4*1024*1024 {
		return nil, fmt.Errorf("%w: research JSON size", ErrBadRequest)
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	var v map[string]any
	if err := d.Decode(&v); err != nil || v == nil {
		return nil, fmt.Errorf("%w: research JSON object required", ErrBadRequest)
	}
	var rest any
	if d.Decode(&rest) != io.EOF {
		return nil, fmt.Errorf("%w: trailing JSON", ErrBadRequest)
	}
	var visit func(any) error
	visit = func(value any) error {
		switch x := value.(type) {
		case map[string]any:
			for k, v := range x {
				if forbiddenResearchKey.MatchString(k) {
					return fmt.Errorf("%w: credential/account field forbidden", ErrBadRequest)
				}
				if err := visit(v); err != nil {
					return err
				}
			}
		case []any:
			for _, v := range x {
				if err := visit(v); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return v, visit(v)
}
func (uc *ResearchUsecase) CreateResearch(ctx context.Context, req ResearchRequest, actor string) (*ResearchJob, error) {
	if !uc.settings.Enabled {
		return nil, fmt.Errorf("%w: research worker disabled", ErrConflict)
	}
	switch req.Kind {
	case "performance", "portfolio", "vectorbt", "jev_evaluate", "prediction_archive", "prediction_evaluate":
	default:
		return nil, fmt.Errorf("%w: unsupported server research kind; RD-Agent bundle requires isolated offline CLI", ErrBadRequest)
	}
	if req.TimeoutSeconds == 0 {
		req.TimeoutSeconds = 60
	}
	if req.TimeoutSeconds < 1 || req.TimeoutSeconds > 300 {
		return nil, fmt.Errorf("%w: timeout must be 1..300 seconds", ErrBadRequest)
	}
	if req.OptionsJSON == "" {
		req.OptionsJSON = "{}"
	}
	if _, err := validateResearchJSON(req.OptionsJSON); err != nil {
		return nil, err
	}
	input, err := validateResearchJSON(req.InputJSON)
	if err != nil {
		return nil, err
	}
	port, ok := uc.source.(DatasetSource)
	if !ok {
		return nil, fmt.Errorf("%w: immutable dataset source unavailable", ErrConflict)
	}
	m, err := port.GetDataset(ctx, req.ManifestID)
	if err != nil {
		return nil, err
	}
	if err = m.Verify(); err != nil {
		return nil, err
	}
	if req.AsOf.IsZero() || req.AsOf.After(m.Cutoff) || req.AsOf.After(time.Now()) || (req.Kind == "prediction_archive" && !req.AsOf.Equal(m.Cutoff)) {
		return nil, fmt.Errorf("%w: research cutoff mismatch", ErrBadRequest)
	}
	if input["manifest_id"] != m.ManifestID || input["manifest_hash"] != m.Hash {
		return nil, fmt.Errorf("%w: input manifest mismatch", ErrBadRequest)
	}
	if actor == "" {
		actor = "local"
	}
	if len(actor) > 64 {
		actor = actor[:64]
	}
	job := &ResearchJob{RunID: uuid.NewString(), Kind: req.Kind, ManifestID: m.ManifestID, ManifestHash: m.Hash, InputJSON: req.InputJSON, OptionsJSON: req.OptionsJSON, AsOf: req.AsOf, Seed: req.Seed, TimeoutSeconds: req.TimeoutSeconds, Status: "queued", CreatedBy: actor}
	var packed bytes.Buffer
	_ = json.NewEncoder(&packed).Encode(struct {
		Kind, Manifest, Input, Options string
		AsOf                           time.Time
		Seed                           int64
		Timeout                        int
	}{job.Kind, job.ManifestHash, job.InputJSON, job.OptionsJSON, job.AsOf, job.Seed, job.TimeoutSeconds})
	job.InputHash = ResearchHash(packed.Bytes())
	return uc.repo.CreateResearch(ctx, job)
}
func (uc *ResearchUsecase) GetResearch(ctx context.Context, id string) (*ResearchJob, error) {
	if !researchID.MatchString(id) {
		return nil, ErrBadRequest
	}
	return uc.repo.GetResearch(ctx, id)
}
func (uc *ResearchUsecase) ListResearch(ctx context.Context, status string, limit, offset int) ([]*ResearchJob, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return uc.repo.ListResearch(ctx, status, limit, offset)
}
func (uc *ResearchUsecase) CancelResearch(ctx context.Context, id string) (*ResearchJob, error) {
	if !researchID.MatchString(id) {
		return nil, ErrBadRequest
	}
	return uc.repo.CancelResearch(ctx, id)
}
func (uc *ResearchUsecase) ResearchArtifact(ctx context.Context, id, name string) (ResearchArtifact, error) {
	job, err := uc.GetResearch(ctx, id)
	if err != nil {
		return ResearchArtifact{}, err
	}
	return uc.exec.Artifact(ctx, job, name)
}
func (uc *ResearchUsecase) RunResearch(ctx context.Context) (bool, error) {
	// State remains authoritative in PostgreSQL when event delivery is unavailable.
	dispatchFinish := observe.ForService("backtest").Op("research_outbox").Begin()
	dispatchErr := uc.repo.DispatchResearch(ctx)
	observe.FinishError(dispatchFinish, dispatchErr)
	if !uc.settings.Enabled {
		return false, nil
	}
	recoverFinish := observe.ForService("backtest").Op("research_recovery").Begin()
	recoverErr := uc.repo.RecoverResearch(ctx, time.Now())
	observe.FinishError(recoverFinish, recoverErr)
	if err := recoverErr; err != nil {
		return false, err
	}
	claimFinish := observe.ForService("backtest").Op("research_claim").Begin()
	job, err := uc.repo.ClaimResearch(ctx, uc.owner, time.Now())
	if err != nil || job == nil {
		if err == nil {
			claimFinish(observe.Skipped)
		} else {
			observe.FinishError(claimFinish, err)
		}
		return false, err
	}
	claimFinish(observe.Succeeded)
	runFinish := observe.ForService("backtest").Op("research_run").Begin()
	runOutcome := observe.Failed
	defer func() { runFinish(runOutcome) }()
	result := ResearchResult{Status: "failed", Reason: "manifest_unavailable"}
	port, ok := uc.source.(DatasetSource)
	if ok {
		m, e := port.GetDataset(ctx, job.ManifestID)
		if e == nil && m.Hash == job.ManifestHash {
			runCtx, cancel := context.WithTimeout(ctx, time.Duration(job.TimeoutSeconds)*time.Second)
			done := make(chan struct{})
			watchStopped := make(chan struct{})
			go func() {
				defer close(watchStopped)
				ticker := time.NewTicker(250 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-done:
						return
					case <-runCtx.Done():
						return
					case <-ticker.C:
						current, e := uc.repo.GetResearch(runCtx, job.RunID)
						if e != nil || current.CancelRequested || current.Owner != uc.owner {
							cancel()
							return
						}
					}
				}
			}()
			result = uc.exec.Execute(runCtx, job, m)
			if runCtx.Err() != nil {
				if ctx.Err() != nil {
					result = ResearchResult{Status: "failed", Reason: "service_interrupted"}
				} else if runCtx.Err() == context.DeadlineExceeded {
					result = ResearchResult{Status: "timed_out", Reason: "wall_time_budget_exceeded"}
				} else {
					result = ResearchResult{Status: "cancelled", Reason: "cancelled_or_control_state_unavailable"}
				}
			}
			close(done)
			cancel()
			<-watchStopped
		}
	}
	finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	finishObservation := observe.ForService("backtest").Op("research_finish").Begin()
	err = uc.repo.FinishResearch(finishCtx, job.RunID, uc.owner, result)
	observe.FinishError(finishObservation, err)
	if err == nil {
		// Finish is CAS-controlled and may be superseded by cancellation/lease
		// loss. The worker's proposed status alone is not the durable job result.
		current, readErr := uc.repo.GetResearch(finishCtx, job.RunID)
		if readErr != nil || current == nil {
			runOutcome = observe.Skipped
		} else {
			switch current.Status {
			case "succeeded":
				runOutcome = observe.Succeeded
			case "rejected", "unavailable":
				runOutcome = observe.Rejected
			case "cancelled", "timed_out":
				runOutcome = observe.Cancelled
			case "failed":
				runOutcome = observe.Failed
			default:
				runOutcome = observe.Skipped
			}
		}
	}
	return true, err
}
