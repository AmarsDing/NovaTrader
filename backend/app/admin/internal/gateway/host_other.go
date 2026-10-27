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
	// W3 拆态收口：不采集 ≠ 坏——非 Windows 构建没有采集器，恒发 down 会在 DGX（Linux）生产目标上
	// 画假红灯；unknown 暗点表示「没测过」，与 models 未配置/sources 未上报同一词表。
	return []map[string]string{
		nodeStatus("磁盘", "unknown", "当前构建不采集磁盘"),
		nodeStatus("显存", "unknown", "当前构建不采集显存"),
	}
}
