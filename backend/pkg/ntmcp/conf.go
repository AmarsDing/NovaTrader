// Package ntmcp 是 NovaTrader 的 MCP 机器面（三仓联动 P3，2026-11-02）：
// 独立只读进程（cmd/ntmcp），底座 aranea 经 trpc-mcp-go Streamable HTTP
// 消费四个只读工具（query_market_snapshot / get_positions /
// list_trade_signals / get_backtest_report）。
//
// 红线（与 HCF AGT / M14 同口径，不可妥协）：
//   - 只读——下单/改仓/审批类写操作一律走 M09 风控链与人工审批，不进 MCP；
//     NT 侧绝不暴露下单工具（M14 设计「本模块不触碰交易链路红线」）。
//   - 「错误≠空列表」——仓储/Redis 错误落得工具 IsError 如实报，绝不塌成
//     空数组装活（与 harness driver=blocked 恒阻断同一诚实声明纪律）。
//   - 密钥纪律——Bearer token 走 env:/enc:/明文三写法经 pkg/llm.ResolveAPIKey
//     解析（启动时解析、失败即拒启动）；yaml 本身零密钥。
//   - enabled=false 默认关：进程直接退出（部署窗口置 true + 配好 token_ref
//     后方对外）。
package ntmcp

import (
	"errors"
	"strings"
	"time"
)

// Conf nt-mcp 子系统配置（plain struct + json tag，configs/config.yaml 的
// `ntmcp:` 段直扫——与 admin gateway FileConfig 同为 yaml 直读先例）。
type Conf struct {
	Enabled     bool          `json:"enabled"`
	Listen      string        `json:"listen"`
	Path        string        `json:"path"`
	TokenRef    string        `json:"token_ref"` // env:/enc:/明文三写法（llm.ResolveAPIKey 语义）
	PageSizeMax int           `json:"page_size_max"`
	HTTPTimeout time.Duration `json:"http_timeout"`
	Tools       ToolGates     `json:"tools"`
}

// ToolGates 逐项工具白名单（默认全开；四件全为只读工具）。
type ToolGates struct {
	MarketSnapshot bool `json:"market_snapshot"`
	Positions      bool `json:"positions"`
	TradeSignals   bool `json:"trade_signals"`
	BacktestReport bool `json:"backtest_report"`
}

// Normalize 补默认（yaml 键缺失时仍安全：默认关闭、token_ref 指向环境变量）。
func (c *Conf) Normalize() {
	if c.Listen == "" {
		c.Listen = ":9303"
	}
	if c.Path == "" {
		c.Path = "/mcp"
	}
	if c.TokenRef == "" {
		c.TokenRef = "env:NT_MCP_TOKEN"
	}
	if c.PageSizeMax <= 0 || c.PageSizeMax > 500 {
		c.PageSizeMax = 50
	}
	if c.HTTPTimeout <= 0 {
		c.HTTPTimeout = 15 * time.Second
	}
}

// Validate 判红线（fail-fast 胜过错配静默运行）。
func (c *Conf) Validate() error {
	if !strings.Contains(c.Listen, ":") {
		return errors.New("ntmcp.listen 缺端口（形如 :9303）")
	}
	if !strings.HasPrefix(c.Path, "/") {
		return errors.New("ntmcp.path 必须以 / 开头")
	}
	return nil
}
