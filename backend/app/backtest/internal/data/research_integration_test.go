package data

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/google/uuid"
	v1 "server/api/backtest/v1"
	"server/app/backtest/internal/biz"
	"server/app/backtest/internal/service"
	"server/ent"
	"server/ent/datasetmanifest"
	"server/ent/outbox"
	"server/ent/researchjob"
	"server/pkg/dataset"
	"server/pkg/dbinit"
	"server/pkg/entclient"
	"server/pkg/events"
)

func researchClient(t *testing.T) *ent.Client {
	t.Helper()
	raw := os.Getenv("NT_TEST_PG_PORT")
	if raw == "" {
		t.Skip("set NT_TEST_PG_PORT for isolated research integration")
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		t.Fatal("invalid explicit PG port")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := entclient.Open(ctx, entclient.Config{DB: dbinit.Config{Host: "127.0.0.1", Port: int32(port), User: "postgres", Password: "postgres", Database: "novatrader_research_test", SSLMode: "disable", Timeout: 3 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn.Client
}
func researchCleanup(t *testing.T, client *ent.Client, ids *[]string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = client.ResearchJob.Delete().Where(researchjob.RunIDIn((*ids)...)).Exec(ctx)
		rows, _ := client.Outbox.Query().Where(outbox.SubjectEQ(biz.ResearchSubject)).All(ctx)
		known := map[string]bool{}
		for _, id := range *ids {
			known[id] = true
		}
		for _, row := range rows {
			var value struct {
				RunID string `json:"run_id"`
			}
			_ = json.Unmarshal(row.Payload, &value)
			if known[value.RunID] {
				_ = client.Outbox.DeleteOneID(row.ID).Exec(ctx)
			}
		}
	})
}
func rawResearchJob() *biz.ResearchJob {
	return &biz.ResearchJob{RunID: uuid.NewString(), Kind: "performance", ManifestID: "sha256:" + strings.Repeat("a", 64), ManifestHash: strings.Repeat("a", 64), AsOf: time.Now().Add(-time.Hour), InputJSON: "{}", OptionsJSON: "{}", InputHash: strings.Repeat("b", 64), CreatedBy: "synthetic-test", TimeoutSeconds: 30}
}

func TestResearchPostgresClaimCancelRecoverAndTransactionalEvents(t *testing.T) {
	client := researchClient(t)
	repo := NewResearchRepo(client, nil)
	ctx := context.Background()
	ids := []string{}
	researchCleanup(t, client, &ids)
	first, err := repo.CreateResearch(ctx, rawResearchJob())
	if err != nil {
		t.Fatal(err)
	}
	ids = append(ids, first.RunID)
	var wins atomic.Int32
	var wg sync.WaitGroup
	owner := make(chan string, 2)
	for _, name := range []string{"owner-a", "owner-b"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			claimed, e := repo.ClaimResearch(ctx, name, time.Now())
			if e != nil {
				t.Error(e)
			}
			if claimed != nil {
				wins.Add(1)
				owner <- name
			}
		}(name)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("duplicate claim", wins.Load())
	}
	winner := <-owner
	cancelled, err := repo.CancelResearch(ctx, first.RunID)
	if err != nil || !cancelled.CancelRequested {
		t.Fatal(err)
	}
	if err = repo.FinishResearch(ctx, first.RunID, winner, biz.ResearchResult{Status: "succeeded", JSON: "must be discarded", ArtifactHash: "untrusted"}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetResearch(ctx, first.RunID)
	if err != nil || got.Status != "cancelled" || got.ResultJSON != "" || got.Revision != 4 {
		t.Fatal(got, err)
	}
	second, err := repo.CreateResearch(ctx, rawResearchJob())
	if err != nil {
		t.Fatal(err)
	}
	ids = append(ids, second.RunID)
	if _, err = repo.ClaimResearch(ctx, "crashed-owner", time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = repo.RecoverResearch(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = repo.FinishResearch(ctx, second.RunID, "crashed-owner", biz.ResearchResult{Status: "succeeded"}); err != nil {
		t.Fatal(err)
	}
	got, err = repo.GetResearch(ctx, second.RunID)
	if err != nil || got.Status != "failed" || got.Reason != "execution_lease_expired_no_automatic_replay" {
		t.Fatal(got, err)
	}
	rows, err := client.Outbox.Query().Where(outbox.SubjectEQ(biz.ResearchSubject)).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	versions := map[int64]bool{}
	for _, row := range rows {
		var payload struct {
			RunID    string `json:"run_id"`
			Revision int64  `json:"revision"`
		}
		if json.Unmarshal(row.Payload, &payload) == nil && payload.RunID == first.RunID {
			versions[payload.Revision] = true
			if strings.Contains(string(row.Payload), "input_json") {
				t.Fatal("input leaked in event")
			}
		}
	}
	if len(versions) != 4 {
		t.Fatal("state and events diverged", versions)
	}
}

func TestResearchHTTPToFrozenManifestRealWorkerAndArtifact(t *testing.T) {
	python := os.Getenv("NT_TEST_RESEARCH_PYTHON")
	if python == "" {
		t.Skip("set NT_TEST_RESEARCH_PYTHON")
	}
	client := researchClient(t)
	ctx := context.Background()
	m, input := researchFixture(t)
	body, _ := json.Marshal(m)
	err := client.DatasetManifest.Create().SetManifestID(m.ManifestID).SetHash(m.Hash).SetCutoff(m.Cutoff).SetContent(body).OnConflictColumns(datasetmanifest.FieldManifestID).DoNothing().Exec(ctx)
	if err != nil && !strings.Contains(err.Error(), "no rows") {
		t.Fatal(err)
	}
	root, _ := filepath.Abs("../../../../..")
	settings := biz.ResearchSettings{Enabled: true, Python: python, Script: filepath.Join(root, "research-worker", "worker.py"), Root: t.TempDir()}
	repo := NewResearchRepo(client, nil)
	executor := NewResearchExecutor(settings)
	uc := biz.NewResearchUsecase(repo, NewSource(client), executor, settings)
	server := khttp.NewServer()
	v1.RegisterResearchHTTPServer(server, service.NewResearchService(uc))
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	request, _ := json.Marshal(map[string]any{"kind": "performance", "manifest_id": m.ManifestID, "as_of": m.Cutoff.Format(time.RFC3339Nano), "input_json": input, "timeout_seconds": 30})
	response, err := http.Post(httpServer.URL+"/v1/backtest/research/jobs", "application/json", strings.NewReader(string(request)))
	if err != nil {
		t.Fatal(err)
	}
	reply, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("create status %d: %s", response.StatusCode, reply)
	}
	var created struct {
		RunID string `json:"runId"`
	}
	if err = json.Unmarshal(reply, &created); err != nil || created.RunID == "" {
		t.Fatal("missing API job", string(reply), err)
	}
	ids := []string{created.RunID}
	researchCleanup(t, client, &ids)
	ran, err := uc.RunResearch(ctx)
	if err != nil || !ran {
		t.Fatal("did not execute", err)
	}
	finished, err := repo.GetResearch(ctx, created.RunID)
	if err != nil || finished.Status != "succeeded" || finished.ManifestHash != m.Hash || finished.ArtifactHash == "" {
		t.Fatalf("finished=%+v err=%v", finished, err)
	}
	response, err = http.Get(fmt.Sprintf("%s/v1/backtest/research/jobs/%s/artifact?name=performance.html", httpServer.URL, created.RunID))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		b, _ := io.ReadAll(response.Body)
		t.Fatal(response.StatusCode, string(b))
	}
	var artifact struct {
		Content []byte `json:"content"`
		SHA     string `json:"sha256"`
	}
	if err = json.NewDecoder(response.Body).Decode(&artifact); err != nil || biz.ResearchHash(artifact.Content) != artifact.SHA {
		t.Fatal("API artifact identity mismatch", err)
	}
	// Default-off and hidden credentials are rejected before any queue mutation.
	disabled := biz.NewResearchUsecase(repo, NewSource(client), executor, biz.ResearchSettings{})
	if _, err = disabled.CreateResearch(ctx, biz.ResearchRequest{}, "test"); err == nil {
		t.Fatal("disabled create accepted")
	}
	if _, err = uc.CreateResearch(ctx, biz.ResearchRequest{Kind: "performance", ManifestID: m.ManifestID, AsOf: m.Cutoff, InputJSON: `{"api_key":"forbidden"}`}, "test"); err == nil {
		t.Fatal("credential JSON accepted")
	}
}

type waitResearchExecutor struct{ start chan struct{} }

func (w waitResearchExecutor) Execute(ctx context.Context, _ *biz.ResearchJob, _ *dataset.Manifest) biz.ResearchResult {
	close(w.start)
	<-ctx.Done()
	return biz.ResearchResult{Status: "failed"}
}
func (waitResearchExecutor) Artifact(context.Context, *biz.ResearchJob, string) (biz.ResearchArtifact, error) {
	return biz.ResearchArtifact{}, biz.ErrNotFound
}
func TestResearchRunningCancelStopsUsecase(t *testing.T) {
	client := researchClient(t)
	ctx := context.Background()
	m, input := researchFixture(t)
	body, _ := json.Marshal(m)
	_ = client.DatasetManifest.Create().SetManifestID(m.ManifestID).SetHash(m.Hash).SetCutoff(m.Cutoff).SetContent(body).OnConflictColumns(datasetmanifest.FieldManifestID).DoNothing().Exec(ctx)
	repo := NewResearchRepo(client, nil)
	started := make(chan struct{})
	uc := biz.NewResearchUsecase(repo, NewSource(client), waitResearchExecutor{started}, biz.ResearchSettings{Enabled: true})
	created, err := uc.CreateResearch(ctx, biz.ResearchRequest{Kind: "performance", ManifestID: m.ManifestID, AsOf: m.Cutoff, InputJSON: input, TimeoutSeconds: 30}, "test")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{created.RunID}
	researchCleanup(t, client, &ids)
	done := make(chan error, 1)
	go func() { _, err := uc.RunResearch(ctx); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	if _, err = uc.CancelResearch(ctx, created.RunID); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not stop worker")
	}
	got, err := repo.GetResearch(ctx, created.RunID)
	if err != nil || got.Status != "cancelled" {
		t.Fatal(got, err)
	}
}

func TestResearchTransactionalEventReachesNATS(t *testing.T) {
	url := os.Getenv("NT_TEST_NATS_URL")
	if url == "" {
		t.Skip("set NT_TEST_NATS_URL for isolated delivery validation")
	}
	client := researchClient(t)
	bus, err := events.Dial(url)
	if err != nil {
		t.Fatal("explicit NATS unavailable", err)
	}
	defer bus.Close()
	sub, err := bus.Conn().SubscribeSync(biz.ResearchSubject)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	if err = bus.Conn().Flush(); err != nil {
		t.Fatal(err)
	}
	repo := NewResearchRepo(client, bus)
	row, err := repo.CreateResearch(context.Background(), rawResearchJob())
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{row.RunID}
	researchCleanup(t, client, &ids)
	if err = repo.DispatchResearch(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		message, e := sub.NextMsg(time.Until(deadline))
		if e != nil {
			break
		}
		var env events.Envelope
		if json.Unmarshal(message.Data, &env) != nil {
			continue
		}
		if env.EntityID == row.RunID {
			if env.CorrelationID != row.RunID || env.EntityVersion != 1 || env.Subject != biz.ResearchSubject {
				t.Fatal("incorrect causal research event", env)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatal("persisted research state was not delivered to isolated NATS")
	}
}
