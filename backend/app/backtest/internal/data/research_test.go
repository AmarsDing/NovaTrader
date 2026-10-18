package data

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"server/app/backtest/internal/biz"
	"server/pkg/dataset"
)

func researchFixture(t *testing.T) (*dataset.Manifest, string) {
	t.Helper()
	root, err := filepath.Abs("../../../../..")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "research-worker", "examples", "synthetic", "manifest.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m dataset.Manifest
	if err = json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if err = m.Verify(); err != nil {
		t.Fatal(err)
	}
	input, err := os.ReadFile(filepath.Join(root, "research-worker", "examples", "synthetic", "performance.input.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &m, string(input)
}

func TestResearchExecutorRealPythonAndArtifactIntegrity(t *testing.T) {
	python := os.Getenv("NT_TEST_RESEARCH_PYTHON")
	if python == "" {
		t.Skip("set NT_TEST_RESEARCH_PYTHON to isolated installed research interpreter")
	}
	observedBefore := researchObservation(t)
	root, err := filepath.Abs("../../../../..")
	if err != nil {
		t.Fatal(err)
	}
	m, input := researchFixture(t)
	settings := biz.ResearchSettings{Enabled: true, Python: python, Script: filepath.Join(root, "research-worker", "worker.py"), Root: t.TempDir()}
	executor := NewResearchExecutor(settings)
	job := &biz.ResearchJob{RunID: "synthetic-go-worker", Kind: "performance", ManifestID: m.ManifestID, ManifestHash: m.Hash, AsOf: m.Cutoff, InputJSON: input, OptionsJSON: "{}", TimeoutSeconds: 30}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result := executor.Execute(ctx, job, m)
	if result.Status != "succeeded" {
		t.Fatalf("actual worker failed: %+v", result)
	}
	job.ResultJSON = result.JSON
	job.ArtifactHash = result.ArtifactHash
	artifact, err := executor.Artifact(ctx, job, "performance.html")
	if err != nil || len(artifact.Content) == 0 {
		t.Fatal(err)
	}
	if _, err = executor.Artifact(ctx, job, "../input.json"); err == nil {
		t.Fatal("unregistered traversal accepted")
	}
	if err = os.WriteFile(filepath.Join(settings.Root, "runs", job.RunID, "performance.html"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = executor.Artifact(ctx, job, "performance.html"); err == nil {
		t.Fatal("artifact tampering accepted")
	}
	if repeated := executor.Execute(ctx, job, m); repeated.Status != "failed" || repeated.Reason != "immutable_run_directory_exists_or_unavailable" {
		t.Fatal("run overwritten", repeated)
	}
	observedAfter := researchObservation(t)
	if observedAfter.Succeeded != observedBefore.Succeeded+1 || observedAfter.Failed != observedBefore.Failed+1 {
		t.Fatalf("real executor success/failure confused: before=%v after=%v", observedBefore, observedAfter)
	}
}

func TestResearchWorkerEnvironmentNeverForwardsCredentials(t *testing.T) {
	t.Setenv("NT_JEV_API_KEY", "must-not-forward")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "must-not-forward")
	for _, entry := range researchEnvironment(t.TempDir()) {
		if strings.Contains(entry, "must-not-forward") || strings.Contains(entry, "NT_JEV") || strings.Contains(entry, "AWS_") {
			t.Fatal("credential leaked to research process")
		}
	}
}
func TestResearchArtifactRejectsUnregisteredPathsAndOversize(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "good.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../good.json", "C:/secret.json", "/secret.json", "a\\b", "a//b"} {
		if _, err := safeResearchFile(root, name, 10); err == nil {
			t.Fatalf("unsafe path accepted: %s", name)
		}
	}
	if _, err := safeResearchFile(root, "good.json", 1); err == nil {
		t.Fatal("size budget ignored")
	}
}

func TestResearchExecutorCancellationKillsChild(t *testing.T) {
	python := os.Getenv("NT_TEST_RESEARCH_PYTHON")
	if python == "" {
		t.Skip("set NT_TEST_RESEARCH_PYTHON")
	}
	observedBefore := researchObservation(t)
	root := t.TempDir()
	script := filepath.Join(root, "sleep.py")
	if err := os.WriteFile(script, []byte("import time\ntime.sleep(30)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m, input := researchFixture(t)
	executor := NewResearchExecutor(biz.ResearchSettings{Python: python, Script: script, Root: filepath.Join(root, "runtime")})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	result := executor.Execute(ctx, &biz.ResearchJob{RunID: "cancel-fixture", Kind: "performance", ManifestID: m.ManifestID, ManifestHash: m.Hash, AsOf: m.Cutoff, InputJSON: input, OptionsJSON: "{}"}, m)
	if result.Reason != "execution_context_ended" || time.Since(start) > 5*time.Second {
		t.Fatal("child cancellation unbounded", result, time.Since(start))
	}
	observedAfter := researchObservation(t)
	if observedAfter.Cancelled != observedBefore.Cancelled+1 || observedAfter.Succeeded != observedBefore.Succeeded {
		t.Fatalf("child cancellation called success: before=%v after=%v", observedBefore, observedAfter)
	}
}
