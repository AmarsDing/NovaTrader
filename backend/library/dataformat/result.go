// Package dataformat 是全仓「没取到 ≠ 0」纪律的统一封装。
//
// 核心约定：一次取数的结果只有两种形态——有值（OK），或缺失（Missing）且必须
// 带原因。禁止用零值冒充「没取到」，禁止把「源失败」与「真无数据」混为一谈。
//
// 用法：
//
//	r := dataformat.OK(42.0)
//	r := dataformat.Missing[float64](dataformat.WhySourceDown, "eastmoney HTTP 429")
//	if v, ok := r.Value(); ok { ... }        // 只有 ok 才能拿到值
//	r2 := dataformat.Map(r, func(v float64) float64 { return v * 100 }) // 缺失自动传播
//
// 各层配合：datahub 校验链（biz.Issue）负责把问题写 data_quality_issue；
// 本封装负责让「缺失」在函数返回值层面无法被静默吞掉。展示层（前端）只渲染
// Value() 成功取到的值，缺失时显示「—」/「没取到（原因）」。
package dataformat

import "fmt"

// Why 是缺失原因。取值稳定，供日志 / data_quality_issue.message / 前端文案引用。
type Why string

const (
	// WhyNotRun 采集没跑 / 任务未执行（如调度未到点、依赖服务未上）。
	WhyNotRun Why = "not_run"
	// WhySourceDown 源端失败：限流/封禁/超时/5xx/连接重置等（P3-9 已打标）。
	WhySourceDown Why = "source_down"
	// WhyEmpty 源成功返回但窗口内真无数据（如 90 天解禁窗 0 行且采集新鲜）。
	WhyEmpty Why = "empty"
	// WhyStale 有数据但已过期，不可当最新用。
	WhyStale Why = "stale"
	// WhyFiltered 数据被校验链隔离（异常值/时间倒挂/覆盖率不足等）。
	WhyFiltered Why = "filtered"
	// WhyUnknown 拿不到任何证据判断有无（兜底，能不用就不用）。
	WhyUnknown Why = "unknown"
)

// Result 是一次取数的结果。零值即 WhyUnknown 缺失，天然安全：
// 声明后忘了赋值也不会变成「0 = 有值」。
type Result[T any] struct {
	value T
	why   Why
	note  string
	ok    bool
}

// OK 返回一个有值结果。
func OK[T any](v T) Result[T] {
	return Result[T]{value: v, ok: true}
}

// Missing 返回一个缺失结果，why 必填（兜底用 WhyUnknown），note 写人话给面板/日志看。
func Missing[T any](why Why, note string) Result[T] {
	if why == "" {
		why = WhyUnknown
	}
	return Result[T]{why: why, note: note}
}

// IsOK 报告是否有值。
func (r Result[T]) IsOK() bool { return r.ok }

// Value 取值。ok=false 时 v 是 T 零值，调用方禁止在 ok=false 时使用 v。
func (r Result[T]) Value() (v T, ok bool) { return r.value, r.ok }

// ValueOr 有值取值，缺失返回 def。用之前想清楚：def 不是「没取到」的替身，
// 只用于展示层已经决定兜底语义的场景（如进度条宽度）。
func (r Result[T]) ValueOr(def T) T {
	if !r.ok {
		return def
	}
	return r.value
}

// Why 返回缺失原因；有值时返回空串。
func (r Result[T]) Why() Why { return r.why }

// Note 返回缺失说明；有值时返回空串。
func (r Result[T]) Note() string { return r.note }

// String 实现 fmt.Stringer，日志里直接打印。缺失时形如 "missing(source_down): ..."。
func (r Result[T]) String() string {
	if r.ok {
		return fmt.Sprintf("ok(%v)", r.value)
	}
	if r.note == "" {
		return fmt.Sprintf("missing(%s)", r.why)
	}
	return fmt.Sprintf("missing(%s): %s", r.why, r.note)
}

// Map 变换有值结果；缺失原样传播（why/note 保留）。变换函数不会被调用在缺失值上。
func Map[A, B any](r Result[A], f func(A) B) Result[B] {
	if !r.ok {
		return Result[B]{why: r.why, note: r.note}
	}
	return OK(f(r.value))
}

// FlatMap 同 Map，但变换本身也可能缺失。
func FlatMap[A, B any](r Result[A], f func(A) Result[B]) Result[B] {
	if !r.ok {
		return Result[B]{why: r.why, note: r.note}
	}
	return f(r.value)
}

// First 返回第一个有值的结果；全部缺失时返回最后一个的缺失原因（保留最有信息量的 why/note）。
func First[T any](results ...Result[T]) Result[T] {
	var last Result[T]
	for _, r := range results {
		if r.ok {
			return r
		}
		last = r
	}
	if last.ok || last.why == "" {
		return Missing[T](WhyUnknown, "没有任何候选结果")
	}
	return last
}
