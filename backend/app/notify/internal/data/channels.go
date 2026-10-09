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
	"server/pkg/events"
)

// Desktop 把通知发到 notify.desktop，由 admin 转给 WebSocket。
type Desktop struct {
	bus *events.Bus
}

func NewDesktop(bus *events.Bus) *Desktop { return &Desktop{bus: bus} }

func (d *Desktop) Push(ctx context.Context, note biz.DesktopNote) error {
	if d == nil || d.bus == nil {
		return fmt.Errorf("notify: desktop bus is nil")
	}
	env, err := events.New("notify", events.SubjectNotifyDesktop, note.TraceID, note)
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
}

func NewFeishu(c *conf.Notify) *Feishu {
	f := &Feishu{client: &http.Client{Timeout: 5 * time.Second}, now: time.Now}
	if c != nil {
		f.webhook = strings.TrimSpace(c.GetFeishuWebhook())
		f.secret = c.GetFeishuSecret()
	}
	return f
}

func (f *Feishu) Send(ctx context.Context, text string) error {
	if f == nil || f.webhook == "" {
		return biz.ErrSkipped
	}
	body, err := biz.FeishuBody(text, f.secret, f.now())
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.webhook, bytes.NewReader(body))
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
