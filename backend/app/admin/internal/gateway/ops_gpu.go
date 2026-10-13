package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// ops_gpu.go：监控驾驶舱 P1 运维两接口（§7 表，只读）。
//
// GET /api/ops/gpu：nvidia-smi 的显存/利用率/温度/功耗（700ms 超时）+ brain 服务的
// 在途/排队与今日 tokens/s（打 brain 的 /v1/brain/stats 与 /v1/brain/usage，800ms）。
// 两路任一失败都诚实标注，不填 0。
//
// GET /api/ops/broker-files：东财 CSV 文件单目录状态。broker-gw 边车当前不存在
// （pkg/brokerif/emfile 只有写入方），接口如实返回 available=false 并点名缺口，
// 不编「待写 0」这种看似正常的假数据。

func (g *Gate) opsGPU(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.principal(r); !ok {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
	defer cancel()

	reply := map[string]any{}
	gpus, ok := queryGPU(ctx)
	gpuRows := make([]map[string]any, 0, len(gpus))
	for _, info := range gpus {
		gpuRows = append(gpuRows, map[string]any{
			"name":           info.Name,
			"mem_used_mib":   info.MemUsedMiB,
			"mem_total_mib":  info.MemTotalMiB,
			"util_pct":       info.UtilPct, // -1 = nvidia-smi 返回 N/A
			"temp_c":         info.TempC,
			"power_w":        info.PowerW,
		})
	}
	reply["gpus"] = gpuRows
	reply["gpu_available"] = ok
	if !ok {
		reply["gpu_note"] = "未检测到 nvidia-smi（或非 Windows 构建）"
	}

	// brain 在途/排队 + 今日吞吐。brain 未配置/无响应时整段为 null，由前端写「未接入」。
	if base := g.Settings.Upstreams["brain"]; base != "" {
		client := g.httpClient()
		type tierStat struct {
			Tier     string   `json:"tier"`
			Model    string   `json:"model"`
			InFlight *float64 `json:"in_flight"`
			Queued   *float64 `json:"queued"`
		}
		var stats struct {
			Tiers []tierStat `json:"tiers"`
		}
		if body, code, err := getJSON(ctx, client, base+"/v1/brain/stats"); err == nil && code == http.StatusOK {
			if json.Unmarshal(body, &stats) == nil {
				var inFlight, queued float64
				seen := false
				for _, t := range stats.Tiers {
					if t.InFlight != nil {
						inFlight += *t.InFlight
						seen = true
					}
					if t.Queued != nil {
						queued += *t.Queued
						seen = true
					}
				}
				if seen {
					reply["brain_in_flight"] = inFlight
					reply["brain_queued"] = queued
				}
			}
		}
		var usage struct {
			Total struct {
				TotalTokens float64 `json:"total_tokens"`
				Calls       float64 `json:"calls"`
			} `json:"total"`
			Since string `json:"since"`
		}
		if body, code, err := getJSON(ctx, client, base+"/v1/brain/usage?window=today"); err == nil && code == http.StatusOK {
			if json.Unmarshal(body, &usage) == nil && usage.Since != "" {
				if since, perr := time.Parse(time.RFC3339, usage.Since); perr == nil {
					hours := time.Since(since).Hours()
					if hours > 0 && usage.Total.Calls > 0 {
						reply["brain_tokens_per_hour"] = usage.Total.TotalTokens / hours
						reply["brain_calls_today"] = usage.Total.Calls
					}
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, reply)
}

func (g *Gate) opsBrokerFiles(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.principal(r); !ok {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	// broker-gw 边车当前不存在：没有消费方在读文件单目录，目录状态无从谈起。
	// 如实说缺口，待 M09 边车落地后再补真实计数。
	writeJSON(w, http.StatusOK, map[string]any{
		"available": false,
		"note":      "broker-gw 进程不存在：东财 CSV 文件单目录没有消费方（pkg/brokerif/emfile 只有写入方）。此接口待 M09 边车落地后返回真实计数。",
		"pending":   nil,
		"written":   nil,
		"consumed":  nil,
		"leftover":  nil,
		"last_ok":   nil,
	})
}
