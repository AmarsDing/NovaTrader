package biz

import (
	"context"
	"testing"
)

// stubAppcfg 把 appcfgGet 读口缝替换为表驱动存根，跑完还原。
// ready() 是运行期复合口径的唯一入口（W3 复核第⑥条），两个方向都得钉。
func stubAppcfg(t *testing.T, vals map[string]string) {
	t.Helper()
	saved := appcfgGet
	appcfgGet = func(_ context.Context, key string) string { return vals[key] }
	t.Cleanup(func() { appcfgGet = saved })
}

// TestReadyOverridesBothDirections 钉死 ready() 的双向覆盖语义：
// yaml 关 + 配置页开 → 门真开（告警判据按这口径，不能漏告）；yaml 开 + 配置页关 → 门真关（不误告）。
func TestReadyOverridesBothDirections(t *testing.T) {
	// yaml enabled=false、small 有配置、large 无；配置页把总开关打开并给了 small 档。
	stubAppcfg(t, map[string]string{
		"app.intel.enabled": "true",
		"app.intel.small":   "qwen-small",
	})
	s := NewLLMScorer(nil, false, true, false, 4)
	small, large := s.ready()
	if !small || large {
		t.Fatalf("yaml 关+配置页开 应得 (true,false)，实际 (%v,%v)", small, large)
	}

	// 反向：yaml 全开，配置页把总开关关了。
	stubAppcfg(t, map[string]string{"app.intel.enabled": "false"})
	s2 := NewLLMScorer(nil, true, true, true, 4)
	small, large = s2.ready()
	if small || large {
		t.Fatalf("yaml 开+配置页关 应得 (false,false)，实际 (%v,%v)", small, large)
	}
}

// TestReadyCompositeTierReadiness 钉大小档的口径：yaml 与配置页任一有值即成档。
func TestReadyCompositeTierReadiness(t *testing.T) {
	// yaml 只配 small；配置页补 large → 双档就绪。
	stubAppcfg(t, map[string]string{"app.intel.large": "qwen-max"})
	s := NewLLMScorer(nil, true, true, false, 4)
	if small, large := s.ready(); !small || !large {
		t.Fatalf("配置页补 large 应双档就绪，实际 (%v,%v)", small, large)
	}
	// 反向：yaml 只配 large；配置页补 small → 双档就绪。
	stubAppcfg(t, map[string]string{"app.intel.small": "qwen-small"})
	s2 := NewLLMScorer(nil, true, false, true, 4)
	if small, large := s2.ready(); !small || !large {
		t.Fatalf("配置页补 small 应双档就绪，实际 (%v,%v)", small, large)
	}
	// 两边都没 small：on 也没用，门不进。
	stubAppcfg(t, map[string]string{})
	s3 := NewLLMScorer(nil, true, false, true, 4)
	if small, large := s3.ready(); small || !large {
		t.Fatalf("small 双无应 (false,true)，实际 (%v,%v)", small, large)
	}
}
