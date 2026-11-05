package harness

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// 驱动常量。
const (
	// DriverBlocked 恒阻断（默认）：token 未配置/契约未实机验证前的一切调用
	// 返回 ErrHarnessContractUnverified（永驻兜底，对齐 HCF ADR-AGT-01）。
	DriverBlocked = "blocked"
	// DriverHTTP 真实底座适配器（机器面 Bearer）。
	DriverHTTP = "http"
)

// Conf harness 子系统配置。
//
// 配置纪律（NT 红线）：密钥类一律引用（env:NAME / enc:...，经
// pkg/llm.ResolveAPIKey 解析），严禁把 Bearer token 明文或密文之外的形式
// 落库/落 yaml 字面量之外的位置；地址类写字面量。
type Conf struct {
	Driver string `json:"driver" yaml:"driver"` // blocked | http
	URL    string `json:"url" yaml:"url"`       // http://192.168.4.44:8810（实机核实 2026-10-20）
	// TokenRef Bearer 引用（env:/enc:/明文三写法，llm.ResolveAPIKey 语义）。
	TokenRef string        `json:"token_ref" yaml:"token_ref"`
	Timeout  time.Duration `json:"timeout" yaml:"timeout"`
}

// Normalize 补齐安全默认值（缺段时仍保持 blocked 兜底）。
func (c *Conf) Normalize() {
	c.Driver = strings.ToLower(strings.TrimSpace(c.Driver))
	if c.Driver == "" {
		c.Driver = DriverBlocked
	}
	c.URL = strings.TrimRight(strings.TrimSpace(c.URL), "/")
	c.TokenRef = strings.TrimSpace(c.TokenRef)
	if c.TokenRef == "" {
		c.TokenRef = "env:NT_ARANEA_TOKEN"
	}
	if c.Timeout <= 0 {
		c.Timeout = 30 * time.Second
	}
}

// Validate 无效配置 fail-fast（胜过错配静默运行）。
func (c *Conf) Validate() error {
	if c.Driver != DriverBlocked && c.Driver != DriverHTTP {
		return fmt.Errorf("harness: driver %q 非法（allowed: blocked|http）", c.Driver)
	}
	if c.Driver == DriverHTTP && c.URL == "" {
		return errors.New("harness: driver=http 但 url 为空")
	}
	if u := strings.ToLower(c.URL); u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return fmt.Errorf("harness: url %q 必须是 http(s) 字面量", c.URL)
	}
	if c.Timeout < time.Second {
		return fmt.Errorf("harness: timeout=%s 过低（≥1s）", c.Timeout)
	}
	return nil
}
