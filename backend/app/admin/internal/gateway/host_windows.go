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

// gpuInfo 是一块显卡的指标（nvidia-smi，700ms 超时）。
type gpuInfo struct {
	Name        string
	MemUsedMiB  float64
	MemTotalMiB float64
	UtilPct     float64 // -1 表示 nvidia-smi 返回 N/A
	TempC       float64 // -1 同上
	PowerW      float64 // -1 同上
}

// queryGPU 查询显存、利用率、温度、功耗。nvidia-smi 不存在或超时返回 nil, false。
func queryGPU(ctx context.Context) ([]gpuInfo, bool) {
	cctx, cancel := context.WithTimeout(ctx, 700*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(cctx, "nvidia-smi",
		"--query-gpu=memory.used,memory.total,name,utilization.gpu,temperature.gpu,power.draw",
		"--format=csv,noheader,nounits")
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	rows := make([]gpuInfo, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, ",")
		if len(fields) < 3 {
			continue
		}
		info := gpuInfo{
			MemUsedMiB:  parseCSVFloat(fields[0]),
			MemTotalMiB: parseCSVFloat(fields[1]),
			UtilPct:     -1,
			TempC:       -1,
			PowerW:      -1,
		}
		// name 之后才是利用率/温度/功耗（名字里可能带逗号，从尾部取三列）
		if len(fields) >= 6 {
			info.UtilPct = parseCSVFloatNA(fields[len(fields)-3])
			info.TempC = parseCSVFloatNA(fields[len(fields)-2])
			info.PowerW = parseCSVFloatNA(fields[len(fields)-1])
			info.Name = strings.TrimSpace(strings.Join(fields[2:len(fields)-3], ","))
		} else {
			info.Name = strings.TrimSpace(strings.Join(fields[2:], ","))
		}
		rows = append(rows, info)
	}
	return rows, len(rows) > 0
}

func parseCSVFloat(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

// parseCSVFloatNA 解析可能为 N/A 的列；N/A 返回 -1（监控纪律：不可得不是 0）。
func parseCSVFloatNA(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "N/A") {
		return -1
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return -1
	}
	return v
}

func gpuNodes(ctx context.Context) []map[string]string {
	gpus, ok := queryGPU(ctx)
	if !ok {
		return []map[string]string{nodeStatus("显存", "down", "未检测到 nvidia-smi")}
	}
	rows := make([]map[string]string, 0, len(gpus))
	for i, info := range gpus {
		used := strconv.FormatFloat(info.MemUsedMiB, 'f', 0, 64)
		total := strconv.FormatFloat(info.MemTotalMiB, 'f', 0, 64)
		label := "显存"
		if len(gpus) > 1 {
			label = "显存 GPU" + strconv.Itoa(i)
		}
		rows = append(rows, nodeStatus(label, "ok", info.Name+" 已用 "+used+" MiB / 共 "+total+" MiB"))
	}
	return rows
}
