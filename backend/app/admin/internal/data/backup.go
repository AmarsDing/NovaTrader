package data

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"server/app/admin/internal/gateway"
)

// 备份用的两个环境变量：可执行文件与输出目录。
// 默认 pg_dump 走 PATH，输出到当前工作目录下的 backups/。
const (
	envBackupDir = "NT_BACKUP_DIR"
	envPgDump    = "NT_PG_DUMP"

	backupTimeout  = 10 * time.Minute
	backupStderrN  = 300
	backupFileMode = 0o600
)

// Backup 用 pg_dump 导出一份逻辑备份，文件名带库名与时间戳。
// 备份目录与 pg_dump 路径可用 NT_BACKUP_DIR / NT_PG_DUMP 覆盖。
// 失败时删掉半成品文件，绝不留下看似成功的空备份。
func (s *AuditStore) Backup(ctx context.Context) (gateway.BackupResult, error) {
	if s == nil || s.pg == nil {
		return gateway.BackupResult{}, errors.New("postgres 未配置")
	}
	pg := s.pg
	host := strings.TrimSpace(pg.GetHost())
	user := strings.TrimSpace(pg.GetUser())
	db := strings.TrimSpace(pg.GetDatabase())
	pass := pg.GetPassword()
	if host == "" || user == "" || db == "" {
		if strings.TrimSpace(pg.GetDsn()) != "" {
			return gateway.BackupResult{}, errors.New("备份暂时只认 host/user/database 三项配置；当前用的是 dsn，请把连接信息拆成显式字段")
		}
		return gateway.BackupResult{}, errors.New("postgres 缺 host/user/database，无法备份")
	}
	port := pg.GetPort()
	if port == 0 {
		port = 5432
	}
	bin := envOr(envPgDump, "pg_dump")
	if _, err := exec.LookPath(bin); err != nil {
		return gateway.BackupResult{}, fmt.Errorf("找不到 pg_dump（%s）", bin)
	}
	dir := envOr(envBackupDir, "backups")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return gateway.BackupResult{}, fmt.Errorf("备份目录不可写：%w", err)
	}

	started := time.Now()
	file := filepath.Join(dir, fmt.Sprintf("novatrader-%s-%s.sql", db, started.Format("20060102-150405")))
	ctx, cancel := context.WithTimeout(ctx, backupTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin,
		"-h", host,
		"-p", strconv.Itoa(int(port)),
		"-U", user,
		"-d", db,
		"-f", file,
		"--no-owner", "--no-privileges",
	)
	// 口令只进子进程环境，不落命令行、不进日志。
	cmd.Env = append(os.Environ(), "PGPASSWORD="+pass)
	if ssl := strings.TrimSpace(pg.GetSslMode()); ssl != "" {
		cmd.Env = append(cmd.Env, "PGSSLMODE="+ssl)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		_ = os.Remove(file)
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		if pass != "" {
			msg = strings.ReplaceAll(msg, pass, "***")
		}
		return gateway.BackupResult{}, fmt.Errorf("pg_dump 失败：%s", clip(msg, backupStderrN))
	}
	info, err := os.Stat(file)
	if err != nil {
		return gateway.BackupResult{}, fmt.Errorf("备份文件读不到：%w", err)
	}
	if err := os.Chmod(file, backupFileMode); err != nil && !errors.Is(err, os.ErrPermission) {
		return gateway.BackupResult{}, fmt.Errorf("备份文件权限设置失败：%w", err)
	}
	return gateway.BackupResult{
		Path:       file,
		Size:       info.Size(),
		Database:   db,
		DurationMs: time.Since(started).Milliseconds(),
		At:         started,
	}, nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
