// M09 东方财富文件单通道（emfile）的服务端观测接线（G7/N8 观测侧落地）。
//
// 纪律（与决策 3、remote 包 doc 一致）：
//   - 这里只做健康探测与状态记账；实盘委托出口（Submit/Cancel）不在本服务放开关，
//     LIVE 委托仍由 biz 的 paper_days 计日与准入门禁双闸拦截；
//   - 配置缺省：提供者返回 nil，emfile 通道在状态面如实缺席；
//   - 配置存在但材料/格式不合法：返回一个只有 Tracker、状态已为 down 的实例并大声告警，
//     绝不报虚假 idle（「没取到≠0」同口径：状态未知如实表达，不粉饰）。
package core

import (
	"context"
	"os"
	"strings"
	"time"

	"server/conf"
	"server/pkg/brokerif"
	"server/pkg/brokerif/remote"

	"github.com/go-kratos/kratos/v2/log"
)

const (
	defaultLiveTimeout = 10 * time.Second
	defaultLiveProbe   = 10 * time.Second
	minLiveProbe       = 2 * time.Second
	maxLiveProbe       = 60 * time.Second
)

// LiveChannel 持有 emfile 通道的客户端与状态机单例；Client 可能为 nil
// （配置不合法的起不来情形），此时通道如实停在 down。
type LiveChannel struct {
	Client   *remote.Client
	Tracker  *brokerif.Tracker
	interval time.Duration
}

// NewLiveChannel 是 wire 提供者：conf 里 trade.broker_file 缺省或 URL 为空 → 返回 nil
// （读面如实缺席）；配置在但起不来 → 返回已 down 的半件实例（详见包级纪律注释）。
func NewLiveChannel(c *conf.Trade, logger log.Logger) *LiveChannel {
	if c == nil || c.GetBrokerFile() == nil || strings.TrimSpace(c.GetBrokerFile().GetUrl()) == "" {
		return nil
	}
	bc := c.GetBrokerFile()
	helper := log.NewHelper(log.With(logger, "module", "trade/live-channel"))
	tracker := brokerif.NewTracker(time.Now())
	fail := func(reason string) *LiveChannel {
		if err := tracker.Transition(brokerif.Down, time.Now()); err != nil {
			helper.Errorf("emfile channel: mark down: %v", err)
		}
		helper.Errorf("emfile channel down at startup: %s", reason)
		return &LiveChannel{Tracker: tracker, interval: liveProbeInterval(bc)}
	}
	ca, err := os.ReadFile(bc.GetCaFile())
	if err != nil {
		return fail("read ca_file: " + err.Error())
	}
	cert, err := os.ReadFile(bc.GetCertFile())
	if err != nil {
		return fail("read cert_file: " + err.Error())
	}
	key, err := os.ReadFile(bc.GetKeyFile())
	if err != nil {
		return fail("read key_file: " + err.Error())
	}
	client, err := remote.New(remote.Config{
		URL:            strings.TrimSpace(bc.GetUrl()),
		AccountID:      bc.GetAccountId(),
		CAPEM:          ca,
		CertificatePEM: cert,
		PrivateKeyPEM:  key,
		Timeout:        liveCallTimeout(bc),
		Track:          tracker, // 每次请求按 Observer 口径折算迁移
	})
	if err != nil {
		return fail("build client: " + err.Error())
	}
	helper.Infof("emfile channel armed: url=%s account=%s", strings.TrimSpace(bc.GetUrl()), bc.GetAccountId())
	return &LiveChannel{Client: client, Tracker: tracker, interval: liveProbeInterval(bc)}
}

// Interval 是健康探测节拍，Runner 的探测循环按它走。
func (lc *LiveChannel) Interval() time.Duration {
	if lc == nil || lc.interval <= 0 {
		return defaultLiveProbe
	}
	return lc.interval
}

// Probe 做一次健康探测并返回健康载荷与错误（供运行循环做观测记账、限频日志，
// 以及 transport_unknown>0 的告警边沿判定）。
// remote.Client.Health 内部已按记账矩阵记 Ok/Fail：传输与回包语义（protocol/
// 账户回声/ISOLATED/隔离约束）任何一项不过关都算 Fail，本层不重复折算。
// Client 未建成时（配置不合法的真实 down）是幂等空操作，返回零值健康与 nil。
func (lc *LiveChannel) Probe(ctx context.Context) (remote.Health, error) {
	if lc == nil || lc.Client == nil {
		return remote.Health{}, nil
	}
	return lc.Client.Health(ctx)
}

func liveCallTimeout(bc *conf.BrokerFile) time.Duration {
	if d := bc.GetTimeout().AsDuration(); d >= time.Second && d <= 30*time.Second {
		return d
	}
	return defaultLiveTimeout
}

func liveProbeInterval(bc *conf.BrokerFile) time.Duration {
	d := bc.GetProbeInterval().AsDuration()
	if d < minLiveProbe {
		return defaultLiveProbe
	}
	if d > maxLiveProbe {
		return maxLiveProbe
	}
	return d
}
