package biz

import (
	"context"
	"errors"
	"sync"
	"time"

	"server/pkg/events"
	"server/pkg/tradecal"
)

// Pipeline 做分级、合并、免打扰和双渠道发送。
type Pipeline struct {
	cfg     Config
	store   Store
	desktop Desktop
	feishu  Feishu
	now     func() time.Time
	session func(time.Time) (tradecal.Session, error)
	sleep   func(time.Duration)
	mu      sync.Mutex
}

func NewPipeline(cfg Config, store Store, desktop Desktop, feishu Feishu) *Pipeline {
	return &Pipeline{
		cfg: cfg, store: store, desktop: desktop, feishu: feishu,
		now:     time.Now,
		session: tradecal.Default.SessionAt,
		sleep:   time.Sleep,
	}
}

// Handle 处理一条上游事件。数据库失败返回错误，渠道失败只记在记录里。
func (p *Pipeline) Handle(ctx context.Context, env events.Envelope) error {
	msg, ok := Classify(env)
	if !ok {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.handle(ctx, msg)
}

func (p *Pipeline) handle(ctx context.Context, msg Message) error {
	msg.CreatedAt = p.now()
	row, dup, err := p.store.Insert(ctx, msg)
	if err != nil {
		return err
	}
	if dup {
		if row.Status == StatusPending {
			return p.deliver(ctx, row)
		}
		return nil
	}
	since := msg.CreatedAt.Add(-p.cfg.window())
	anchor, err := p.store.Recent(ctx, row.DedupKey, since, row.ID)
	if err != nil {
		return err
	}
	if anchor != nil {
		return p.store.MarkMerged(ctx, row.ID, anchor.ID, msg)
	}
	quiet, err := p.quiet(msg.CreatedAt)
	if err != nil && row.Priority != PriCritical {
		return p.store.MarkHeld(ctx, row.ID)
	}
	if quiet && row.Priority != PriCritical {
		return p.store.MarkHeld(ctx, row.ID)
	}
	return p.deliver(ctx, row)
}

// Flush 在放行时段把压住的通知发出去。同一 dedup_key 只发最新的一条。
func (p *Pipeline) Flush(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	quiet, err := p.quiet(p.now())
	if err != nil || quiet {
		return nil
	}
	rows, err := p.store.ListStatus(ctx, StatusHeld)
	if err != nil {
		return err
	}
	groups := map[string][]Message{}
	var order []string
	for _, row := range rows {
		if _, ok := groups[row.DedupKey]; !ok {
			order = append(order, row.DedupKey)
		}
		groups[row.DedupKey] = append(groups[row.DedupKey], row)
	}
	for _, key := range order {
		g := groups[key]
		latest := g[len(g)-1]
		for _, old := range g[:len(g)-1] {
			if err := p.store.MarkMerged(ctx, old.ID, latest.ID, latest); err != nil {
				return err
			}
		}
		fresh, err := p.store.Get(ctx, latest.ID)
		if err != nil {
			return err
		}
		if err := p.deliver(ctx, fresh); err != nil {
			return err
		}
	}
	return nil
}

// Recover 重投进程退出前没写完的 pending。
func (p *Pipeline) Recover(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	rows, err := p.store.ListStatus(ctx, StatusPending)
	if err != nil {
		return err
	}
	quiet, qerr := p.quiet(p.now())
	for _, row := range rows {
		if (qerr != nil || quiet) && row.Priority != PriCritical {
			if err := p.store.MarkHeld(ctx, row.ID); err != nil {
				return err
			}
			continue
		}
		if err := p.deliver(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

func (p *Pipeline) List(ctx context.Context, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	return p.store.List(ctx, limit)
}

func (p *Pipeline) Get(ctx context.Context, id int) (Message, error) {
	return p.store.Get(ctx, id)
}

func (p *Pipeline) quiet(at time.Time) (bool, error) {
	s, err := p.session(at)
	if err != nil {
		return true, err
	}
	return Quiet(s), nil
}

func (p *Pipeline) deliver(ctx context.Context, row Message) error {
	fresh := row
	if got, err := p.store.Get(ctx, row.ID); err == nil {
		fresh = got
	}
	text := Render(p.cfg.template(fresh.Category), fresh)
	note := DesktopNote{
		ID: fresh.ID, EventID: fresh.EventID, Priority: fresh.Priority,
		Category: fresh.Category, Title: fresh.Title, Body: text, At: p.now(), TraceID: fresh.TraceID,
	}
	var deskErr, feiErr error
	var deskN, feiN int
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		deskN, deskErr = p.retry(ctx, func() error { return p.desktop.Push(ctx, note) })
	}()
	go func() {
		defer wg.Done()
		feiN, feiErr = p.retry(ctx, func() error { return p.feishu.Send(ctx, text) })
	}()
	wg.Wait()
	fresh.Rendered = text
	fresh.DesktopStatus = channelStatus(deskErr)
	fresh.DesktopAttempts = deskN
	fresh.DesktopError = errText(deskErr)
	fresh.FeishuStatus = channelStatus(feiErr)
	fresh.FeishuAttempts = feiN
	fresh.FeishuError = errText(feiErr)
	fresh.Status = messageStatus(fresh.DesktopStatus, fresh.FeishuStatus)
	fresh.SentAt = p.now()
	return p.store.SaveDelivery(ctx, fresh)
}

func (p *Pipeline) retry(ctx context.Context, fn func() error) (int, error) {
	n := p.cfg.retries()
	var err error
	for i := 1; i <= n; i++ {
		if ctx.Err() != nil {
			return i, ctx.Err()
		}
		err = fn()
		if err == nil || errors.Is(err, ErrSkipped) {
			return i, err
		}
		if i < n {
			p.sleep(p.cfg.base() << (i - 1))
		}
	}
	return n, err
}

func channelStatus(err error) string {
	switch {
	case err == nil:
		return ChannelSent
	case errors.Is(err, ErrSkipped):
		return ChannelSkipped
	default:
		return ChannelFailed
	}
}

func errText(err error) string {
	if err == nil || errors.Is(err, ErrSkipped) {
		return ""
	}
	return clip(err.Error(), 500)
}

func messageStatus(desktop, feishu string) string {
	sent := desktop == ChannelSent || feishu == ChannelSent
	bad := desktop == ChannelFailed || feishu == ChannelFailed
	if sent && bad {
		return StatusPartial
	}
	if sent {
		return StatusSent
	}
	return StatusFailed
}
