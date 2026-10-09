package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCopiesConfigBeforeDump(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfg, []byte("postgres: local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := Run(context.Background(), filepath.Join(dir, "missing-pg-dump.exe"), cfg, filepath.Join(dir, "backups"), "postgres://local/novatrader", time.Date(2026, 10, 8, 16, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("expected pg_dump to fail")
	}
	if _, statErr := os.Stat(filepath.Join(out, "config.yaml")); statErr != nil {
		t.Fatal(statErr)
	}
}
