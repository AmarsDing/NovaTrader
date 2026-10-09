//go:build windows

package gateway

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

func hostResources(ctx context.Context) []map[string]string {
	out := []map[string]string{diskNode()}
	return append(out, gpuNodes(ctx)...)
}

func diskNode() map[string]string {
	root := `C:\`
	if wd, err := os.Getwd(); err == nil && len(wd) >= 2 && wd[1] == ':' {
		root = wd[:2] + `\`
	}
	var free, total uint64
	err := windows.GetDiskFreeSpaceEx(windows.StringToUTF16Ptr(root), &free, &total, nil)
	if err != nil || total == 0 {
		return nodeStatus("磁盘", "down", root+" 读不到容量")
	}
	detail := root + " 已用 " + gib(total-free) + " / 共 " + gib(total) + "，可用 " + gib(free)
	status := "ok"
	if float64(free)/float64(total) < 0.1 {
		status = "down"
	}
	return nodeStatus("磁盘", status, detail)
}

func gib(n uint64) string {
	v := float64(n) / (1024 * 1024 * 1024)
	return strconv.FormatFloat(v, 'f', 1, 64) + " GB"
}

func gpuNodes(ctx context.Context) []map[string]string {
	cctx, cancel := context.WithTimeout(ctx, 700*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(cctx, "nvidia-smi",
		"--query-gpu=memory.used,memory.total,name",
		"--format=csv,noheader,nounits")
	out, err := cmd.Output()
	if err != nil {
		return []map[string]string{nodeStatus("显存", "down", "未检测到 nvidia-smi")}
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	rows := make([]map[string]string, 0, len(lines))
	for i, line := range lines {
		fields := strings.Split(line, ",")
		if len(fields) < 3 {
			continue
		}
		used := strings.TrimSpace(fields[0])
		total := strings.TrimSpace(fields[1])
		name := strings.TrimSpace(strings.Join(fields[2:], ","))
		label := "显存"
		if len(lines) > 1 {
			label = "显存 GPU" + strconv.Itoa(i)
		}
		rows = append(rows, nodeStatus(label, "ok", name+" 已用 "+used+" MiB / 共 "+total+" MiB"))
	}
	if len(rows) == 0 {
		return []map[string]string{nodeStatus("显存", "down", "nvidia-smi 没有返回显存")}
	}
	return rows
}
