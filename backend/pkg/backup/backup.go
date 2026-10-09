// Package backup 按日复制配置，并调用 pg_dump 导出业务库。
package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Run 把 configPath 复制到 dest/日期/，再导出 PostgreSQL。pgDump 为空时从 PATH 查找。
func Run(ctx context.Context, pgDump, configPath, dest, databaseURL string, now time.Time) (string, error) {
	dir := filepath.Join(dest, now.Format("2006-01-02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if configPath != "" {
		if err := copyFile(configPath, filepath.Join(dir, filepath.Base(configPath))); err != nil {
			return "", err
		}
	}
	if pgDump == "" {
		pgDump = "pg_dump"
	}
	out := filepath.Join(dir, "novatrader.sql")
	cmd := exec.CommandContext(ctx, pgDump, "--dbname", databaseURL, "--file", out, "--no-owner")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return dir, fmt.Errorf("backup: pg_dump: %w", err)
	}
	return dir, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
