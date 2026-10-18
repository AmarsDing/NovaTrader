package data

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"server/app/backtest/internal/biz"
	"server/pkg/dataset"
	"server/pkg/observe"
)

type ResearchExecutor struct{ settings biz.ResearchSettings }

func NewResearchExecutor(settings biz.ResearchSettings) *ResearchExecutor {
	return &ResearchExecutor{settings: settings}
}

type workerArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type workerResult struct {
	RunID            string           `json:"run_id"`
	ManifestID       string           `json:"manifest_id"`
	ManifestHash     string           `json:"manifest_hash"`
	ManifestFileHash string           `json:"manifest_file_sha256"`
	Status           string           `json:"status"`
	Reason           string           `json:"reason"`
	ArtifactHash     string           `json:"artifact_hash"`
	Artifacts        []workerArtifact `json:"artifacts"`
}

func researchFailure(reason string) biz.ResearchResult {
	return biz.ResearchResult{Status: "failed", Reason: reason}
}
func safeResearchFile(root, name string, max int64) ([]byte, error) {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || !strings.EqualFold(resolved, filepath.Clean(root)) {
		return nil, fmt.Errorf("artifact root unavailable or linked")
	}
	if name == "" || filepath.IsAbs(name) || strings.Contains(name, "\\") || strings.Contains(name, ":") {
		return nil, biz.ErrBadRequest
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return nil, biz.ErrBadRequest
		}
	}
	path := filepath.Join(root, filepath.FromSlash(name))
	current := root
	for _, part := range strings.Split(name, "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("symlink artifact forbidden")
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > max {
		return nil, fmt.Errorf("artifact size/type invalid")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, max+1))
	if int64(len(body)) > max {
		return nil, fmt.Errorf("artifact too large")
	}
	return body, err
}
func (e *ResearchExecutor) paths() (string, error) {
	if !filepath.IsAbs(e.settings.Python) || !filepath.IsAbs(e.settings.Script) || !filepath.IsAbs(e.settings.Root) {
		return "", fmt.Errorf("absolute configured Python/script/root required")
	}
	for _, path := range []string{e.settings.Python, e.settings.Script} {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("research runtime unavailable")
		}
	}
	root := filepath.Clean(e.settings.Root)
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || !strings.EqualFold(resolved, root) {
		return "", fmt.Errorf("research root must not be a symlink")
	}
	return root, nil
}
func researchEnvironment(work string) []string {
	env := []string{"PYTHONNOUSERSITE=1", "PYTHONHASHSEED=0", "OMP_NUM_THREADS=1", "OPENBLAS_NUM_THREADS=1", "MKL_NUM_THREADS=1", "NUMBA_NUM_THREADS=1", "MPLBACKEND=Agg", "MPLCONFIGDIR=" + filepath.Join(work, "mpl"), "HOME=" + work, "USERPROFILE=" + work}
	for _, key := range []string{"SYSTEMROOT", "WINDIR", "PATH", "PATHEXT", "TEMP", "TMP"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}
func (e *ResearchExecutor) Execute(ctx context.Context, job *biz.ResearchJob, m *dataset.Manifest) (out biz.ResearchResult) {
	finish := observe.ForService("backtest").Op("research_execute").Begin()
	defer func() { finish(researchExecutionOutcome(ctx, out)) }()
	root, err := e.paths()
	if err != nil {
		return biz.ResearchResult{Status: "unavailable", Reason: "research_runtime_not_configured"}
	}
	if err = m.Verify(); err != nil || m.ManifestID != job.ManifestID || m.Hash != job.ManifestHash {
		return researchFailure("frozen_manifest_invalid")
	}
	if strings.ContainsAny(job.RunID, "/\\:") || job.RunID == "" {
		return researchFailure("invalid_run_identity")
	}
	inputs := filepath.Join(root, "inputs")
	if err = os.MkdirAll(inputs, 0700); err != nil {
		return researchFailure("input_directory_unavailable")
	}
	work := filepath.Join(inputs, job.RunID)
	if err = os.Mkdir(work, 0700); err != nil {
		return researchFailure("immutable_run_directory_exists_or_unavailable")
	}
	manifestJSON, err := json.Marshal(m)
	if err != nil {
		return researchFailure("manifest_encoding_failed")
	}
	input := []byte(job.InputJSON)
	manifestSHA := biz.ResearchHash(manifestJSON)
	var options map[string]any
	dec := json.NewDecoder(strings.NewReader(job.OptionsJSON))
	dec.UseNumber()
	if err = dec.Decode(&options); err != nil {
		return researchFailure("invalid_stored_options")
	}
	workerJob := map[string]any{"schema_version": 1, "run_id": job.RunID, "kind": job.Kind, "as_of": job.AsOf.Format(time.RFC3339Nano), "seed": job.Seed, "options": options,
		"manifest": map[string]any{"id": m.ManifestID, "hash": m.Hash, "path": "manifest.json", "sha256": manifestSHA}, "input": map[string]any{"path": "input.json", "sha256": biz.ResearchHash(input)}}
	request, err := json.Marshal(workerJob)
	if err != nil {
		return researchFailure("job_encoding_failed")
	}
	for name, body := range map[string][]byte{"manifest.json": manifestJSON, "input.json": input, "job.json": request} {
		if err = os.WriteFile(filepath.Join(work, name), body, 0400); err != nil {
			return researchFailure("input_export_failed")
		}
	}
	outputRoot := filepath.Join(root, "runs")
	cmd := exec.CommandContext(ctx, e.settings.Python, "-I", e.settings.Script, "--job", filepath.Join(work, "job.json"), "--output", outputRoot)
	cmd.Dir = work
	cmd.Env = researchEnvironment(work)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 2 * time.Second
	configureResearchProcess(cmd)
	err = cmd.Run()
	if ctx.Err() != nil {
		return researchFailure("execution_context_ended")
	}
	outputDir := filepath.Join(outputRoot, job.RunID)
	body, readErr := safeResearchFile(outputDir, "result.json", 4*1024*1024)
	if readErr != nil {
		if err != nil {
			return researchFailure("worker_exit_without_result")
		}
		return researchFailure("worker_result_missing")
	}
	var result workerResult
	if json.Unmarshal(body, &result) != nil || result.RunID != job.RunID || result.ManifestID != job.ManifestID || result.ManifestHash != job.ManifestHash || result.ManifestFileHash != manifestSHA {
		return researchFailure("worker_result_identity_mismatch")
	}
	if len(result.Artifacts) > 1000 {
		return researchFailure("artifact_count_exceeded")
	}
	seen := map[string]bool{}
	total := int64(0)
	for _, artifact := range result.Artifacts {
		if seen[artifact.Path] {
			return researchFailure("duplicate_artifact")
		}
		seen[artifact.Path] = true
		b, e := safeResearchFile(outputDir, artifact.Path, 64*1024*1024)
		if e != nil || biz.ResearchHash(b) != artifact.SHA256 || int64(len(b)) != artifact.Bytes {
			return researchFailure("artifact_integrity_failed")
		}
		total += int64(len(b))
		if total > 64*1024*1024 {
			return researchFailure("total_artifact_budget_exceeded")
		}
	}
	canonicalArtifacts := make([]map[string]any, 0, len(result.Artifacts))
	for _, a := range result.Artifacts {
		canonicalArtifacts = append(canonicalArtifacts, map[string]any{"path": a.Path, "sha256": a.SHA256, "bytes": a.Bytes})
	}
	var canonical bytes.Buffer
	encoder := json.NewEncoder(&canonical)
	encoder.SetEscapeHTML(false)
	if encoder.Encode(canonicalArtifacts) != nil || biz.ResearchHash(bytes.TrimSuffix(canonical.Bytes(), []byte("\n"))) != result.ArtifactHash {
		return researchFailure("artifact_manifest_hash_mismatch")
	}
	// Python's artifact_hash is retained in its report; the host independently
	// addresses the exact verified result bytes as the downloadable result identity.
	if err != nil && result.Status == "succeeded" {
		return researchFailure("worker_exit_status_mismatch")
	}
	switch result.Status {
	case "succeeded", "unavailable", "rejected", "failed", "timed_out":
	default:
		return researchFailure("invalid_worker_status")
	}
	return biz.ResearchResult{Status: result.Status, Reason: result.Reason, JSON: string(body), ArtifactHash: biz.ResearchHash(body)}
}
func (e *ResearchExecutor) Artifact(ctx context.Context, job *biz.ResearchJob, name string) (biz.ResearchArtifact, error) {
	if ctx.Err() != nil {
		return biz.ResearchArtifact{}, ctx.Err()
	}
	if job.ResultJSON == "" || biz.ResearchHash([]byte(job.ResultJSON)) != job.ArtifactHash {
		return biz.ResearchArtifact{}, biz.ErrNotFound
	}
	if name == "result.json" {
		return biz.ResearchArtifact{Name: name, SHA256: job.ArtifactHash, Content: []byte(job.ResultJSON)}, nil
	}
	root, err := e.paths()
	if err != nil {
		return biz.ResearchArtifact{}, err
	}
	var result workerResult
	if err = json.Unmarshal([]byte(job.ResultJSON), &result); err != nil {
		return biz.ResearchArtifact{}, err
	}
	for _, item := range result.Artifacts {
		if item.Path == name {
			body, e := safeResearchFile(filepath.Join(root, "runs", job.RunID), name, 8*1024*1024)
			if e != nil {
				return biz.ResearchArtifact{}, e
			}
			if biz.ResearchHash(body) != item.SHA256 || int64(len(body)) != item.Bytes {
				return biz.ResearchArtifact{}, fmt.Errorf("artifact hash/length mismatch")
			}
			return biz.ResearchArtifact{Name: name, SHA256: item.SHA256, Content: body}, nil
		}
	}
	return biz.ResearchArtifact{}, biz.ErrNotFound
}
