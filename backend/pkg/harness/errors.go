package harness

import "fmt"

// Error harness 结构化错误：Kind 为哨兵（errors.Is 分流用），Detail 为
// 如实原因文案。纪律：Detail 只带上游状态码/错误消息/配置项名，绝不带
// token、webhook secret 等密钥材料（错误会进日志与页面）。
type Error struct {
	Kind   error
	Detail string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return e.Kind.Error()
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Detail)
}

// Unwrap 使 errors.Is(err, ErrHarness*) 命中 Kind 哨兵。
func (e *Error) Unwrap() error { return e.Kind }
