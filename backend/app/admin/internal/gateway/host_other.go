//go:build !windows

package gateway

import "context"

func hostResources(context.Context) []map[string]string {
	return []map[string]string{
		nodeStatus("磁盘", "down", "当前构建不采集磁盘"),
		nodeStatus("显存", "down", "当前构建不采集显存"),
	}
}
