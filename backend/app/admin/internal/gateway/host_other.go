//go:build !windows

package gateway

import "context"

// gpuInfo 见 host_windows.go；非 Windows 构建不采集显卡。
type gpuInfo struct {
	Name        string
	MemUsedMiB  float64
	MemTotalMiB float64
	UtilPct     float64
	TempC       float64
	PowerW      float64
}

func queryGPU(context.Context) ([]gpuInfo, bool) { return nil, false }

func hostResources(context.Context) []map[string]string {
	return []map[string]string{
		nodeStatus("磁盘", "down", "当前构建不采集磁盘"),
		nodeStatus("显存", "down", "当前构建不采集显存"),
	}
}
