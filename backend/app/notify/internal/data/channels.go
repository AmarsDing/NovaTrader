package data

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"server/app/notify/internal/biz"
	"server/conf"
	"server/pkg/appcfg"
	"server/pkg/events"
)

// DesktopPublisher 是 Desktop 依赖的最小发布口（*events.Bus 满足），便于按行为单测。
type DesktopPublisher interface {
	Publish(ctx context.Context, env events.Envelope) error
}

// Desktop 把通知发到 notify.desktop，由 admin 转给 WebSocket。
type Desktop struct {
	bus DesktopPublisher
}

func NewDesktop(bus DesktopPublisher) *Desktop { return &Desktop{bus: bus} }

func (d *Desktop) Push(ctx context.Context, note biz.DesktopNote) error {
	if d == nil || d.bus == nil {
		return fmt.Errorf("notify: desktop bus is nil")
	}
	var env events.Envelope
	var err error
	if _, ok := events.ParentEnvelope(ctx); ok {
		// 直发链：消费侧放进 ctx 的父信封是真因果，trace/causation/correlation/source_time 全续。
		env, err = events.NewWithContext(ctx, "notify", events.SubjectNotifyDesktop, note)
	} else {
		// 无父直发与静默重投：行内链号仍为准，没有真实父就不编 causation_id。
		env, err = events.New("notify", events.SubjectNotifyDesktop, note.TraceID, note)
	}
	if err != nil {
		return err
	}
	return d.bus.Publish(ctx, env)
}

// Feishu 是出站 Webhook。地址为空时跳过。
type Feishu struct {
	webhook string
	secret  string
	client  *http.Client
	now     func() time.Time
	// conf 是 appcfg 查询缝（默认 appcfg.Get），测试注入假覆盖值才办得到
	// 「overlay 生效」回归——appcfg.Get 本身绑的是进程内 ent 缓存。
	conf func(ctx context.Context, key string) string
}

func NewFeishu(c *conf.Notify) *Feishu {
	f := &Feishu{client: &http.Client{Timeout: 5 * time.Second}, now: time.Now, conf: appcfg.Get}
	if c != nil {
		f.webhook = strings.TrimSpace(c.GetFeishuWebhook())
		f.secret = c.GetFeishuSecret()
	}
	return f
}

func (f *Feishu) Send(ctx context.Context, text string) error {
	if f == nil {
		return biz.ErrSkipped
	}
	// appcfg overlay 优先于 boot 配置：webhook/secret 都必须用合并后的局部变量。
	webhook := f.webhook
	if v := strings.TrimSpace(f.conf(ctx, "app.notify.webhook")); v != "" {
		webhook = v
	}
	secret := f.secret
	if v := f.conf(ctx, "app.notify.secret"); v != "" {
		secret = v
	}
	if webhook == "" {
		return biz.ErrSkipped
	}
	body, err := biz.FeishuBody(text, secret, f.now())
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("feishu http %d: %s", resp.StatusCode, bytes.TrimSpace(raw))
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var reply struct {
		Code       *int   `json:"code"`
		StatusCode *int   `json:"StatusCode"`
		Msg        string `json:"msg"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil
	}
	if reply.Code != nil && *reply.Code != 0 {
		return fmt.Errorf("feishu code %d: %s", *reply.Code, reply.Msg)
	}
	if reply.StatusCode != nil && *reply.StatusCode != 0 {
		return fmt.Errorf("feishu status %d: %s", *reply.StatusCode, reply.Msg)
	}
	return nil
}
