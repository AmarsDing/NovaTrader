package biz

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"server/pkg/events"
	"server/pkg/tradecal"
)

func TestQuietPhases(t *testing.T) {
	loc := tradecal.Shanghai()
	day := time.Date(2026, 10, 9, 0, 0, 0, 0, loc)
	cases := []struct {
		at    time.Time
		quiet bool
		phase tradecal.Phase
	}{
		{day.Add(2 * time.Hour), true, tradecal.Closed},
		{day.Add(8*time.Hour + 30*time.Minute), false, tradecal.PreOpen},
		{day.Add(9*time.Hour + 20*time.Minute), false, tradecal.CallAuction},
		{day.Add(10 * time.Hour), false, tradecal.AMTrading},
		{day.Add(12 * time.Hour), true, tradecal.NoonBreak},
		{day.Add(14 * time.Hour), false, tradecal.PMTrading},
		{day.Add(15*time.Hour + 10*time.Minute), true, tradecal.PostClose},
		{time.Date(2026, 10, 10, 10, 0, 0, 0, loc), true, tradecal.Closed},
	}
	for _, c := range cases {
		s, err := tradecal.Default.SessionAt(c.at)
		if err != nil {
			t.Fatal(err)
		}
		if s.Phase != c.phase || Quiet(s) != c.quiet {
			t.Fatalf("%s phase %s quiet %v, want %s quiet %v", c.at.Format("15:04"), s.Phase, Quiet(s), c.phase, c.quiet)
		}
	}
}

func TestClassify(t *testing.T) {
	pending := mustEnv(t, "strategy", events.SubjectSignal, map[string]any{
		"signal_id": 7, "status": "pending", "stock_code": "600519.SH", "stock_name": "贵州茅台",
		"signal_type": "buy", "reasoning": "首板",
	})
	msg, ok := Classify(pending)
	if !ok || msg.Priority != PriCritical || msg.DedupKey != "signal:7" || msg.Fields["symbol"] != "600519.SH" {
		t.Fatalf("signal %+v ok %v", msg, ok)
	}
	approved := mustEnv(t, "strategy", events.SubjectSignal, map[string]any{"signal_id": 7, "status": "approved"})
	if _, ok := Classify(approved); ok {
		t.Fatal("status change must not notify")
	}
	risk := mustEnv(t, "risk", events.SubjectRiskAlert, map[string]any{"kind": "killswitch", "message": "停机", "account_type": "LIVE"})
	msg, ok = Classify(risk)
	if !ok || msg.Priority != PriCritical || msg.DedupKey != "risk:killswitch:LIVE" {
		t.Fatalf("risk %+v", msg)
	}
	warn := mustEnv(t, "datahub", events.SubjectNotifyRequest, map[string]any{
		"level": "warn", "category": "datahub", "title": "数据源切换：daily", "body": "切了", "dedup_key": "datahub:数据源切换：daily",
	})
	msg, ok = Classify(warn)
	if !ok || msg.Priority != PriCritical || msg.DedupKey != "datahub:数据源切换：daily" {
		t.Fatalf("warn %+v", msg)
	}
	info := mustEnv(t, "datahub", events.SubjectNotifyRequest, map[string]any{"level": "info", "title": "数据源恢复：daily", "body": "回切"})
	msg, ok = Classify(info)
	if !ok || msg.Priority != PriMedium {
		t.Fatalf("info %+v", msg)
	}
	fill := mustEnv(t, "trade", events.SubjectTradeFill, map[string]any{"Symbol": "600000.SH", "Side": "buy", "Qty": 100, "Price": 10.5, "ClientOrderID": "c1"})
	msg, ok = Classify(fill)
	if !ok || msg.Priority != PriHigh || msg.Fields["qty"] != "100" || msg.Fields["price"] != "10.5" {
		t.Fatalf("fill %+v", msg)
	}
	zero := mustEnv(t, "trade", events.SubjectTradeFill, map[string]any{"Symbol": "600000.SH", "Qty": 0})
	if _, ok := Classify(zero); ok {
		t.Fatal("zero fill")
	}
	intel := mustEnv(t, "intel", events.SubjectIntelAlert, map[string]any{"level": "critical", "title": "利空", "cluster_id": 3, "stocks": []string{"600519.SH"}})
	msg, ok = Classify(intel)
	if !ok || msg.Priority != PriCritical || msg.DedupKey != "intel:3" || msg.Fields["symbol"] != "600519.SH" {
		t.Fatalf("intel %+v", msg)
	}
	brief := mustEnv(t, "brain", events.SubjectBriefing, map[string]any{"date": "2026-10-09", "headline": "偏暖"})
	msg, ok = Classify(brief)
	if !ok || msg.Priority != PriHigh || msg.Title != "晨报 2026-10-09" {
		t.Fatalf("brief %+v", msg)
	}
	review := mustEnv(t, "brain", events.SubjectStrategyReview, map[string]any{"date": "2026-10-09", "body": "少做"})
	msg, ok = Classify(review)
	if !ok || msg.Priority != PriMedium || msg.DedupKey != "review:2026-10-09" {
		t.Fatalf("review %+v", msg)
	}
}

func TestRenderAndFeishuText(t *testing.T) {
	msg := Message{Title: "贵州茅台", Body: "首板", Category: "signal", Fields: map[string]string{"symbol": "600519.SH"}, MergeCount: 2}
	got := Render("{{title}} {{symbol}} x{{count}}\n{{body}}", msg)
	if got != "贵州茅台 600519.SH x3\n首板" {
		t.Fatalf("render %q", got)
	}
	body, err := FeishuBody(got, "", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if m["msg_type"] != "text" {
		t.Fatalf("msg_type %v", m["msg_type"])
	}
	for _, banned := range []string{"interactive", "button", "card", "callback", "action"} {
		if _, ok := m[banned]; ok {
			t.Fatalf("feishu payload has %s", banned)
		}
	}
	signed, err := FeishuBody("hi", "secret", time.Unix(1599360473, 0))
	if err != nil {
		t.Fatal(err)
	}
	again, _ := FeishuBody("hi", "secret", time.Unix(1599360473, 0))
	if string(signed) != string(again) {
		t.Fatal("sign not stable")
	}
	if err := json.Unmarshal(signed, &m); err != nil {
		t.Fatal(err)
	}
	if m["msg_type"] != "text" || m["sign"] == "" || m["timestamp"] != "1599360473" {
		t.Fatalf("signed %+v", m)
	}
}

func TestSendCriticalAtNightAndHoldBriefing(t *testing.T) {
	p, st, desk, fei := newPipe(true)
	fei.skip = true
	env := mustEnv(t, "risk", events.SubjectRiskAlert, map[string]any{"kind": "breaker", "message": "熔断"})
	if err := p.Handle(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if desk.n.Load() != 1 || st.rows[0].Status != StatusSent || st.rows[0].FeishuStatus != ChannelSkipped {
		t.Fatalf("night critical desk %d row %+v", desk.n.Load(), st.rows[0])
	}
	brief := mustEnv(t, "brain", events.SubjectBriefing, map[string]any{"date": "2026-10-09", "headline": "偏暖"})
	if err := p.Handle(context.Background(), brief); err != nil {
		t.Fatal(err)
	}
	if desk.n.Load() != 1 || st.byID(2).Status != StatusHeld {
		t.Fatalf("briefing should be held, desk %d status %s", desk.n.Load(), st.byID(2).Status)
	}
	p.session = func(time.Time) (tradecal.Session, error) {
		return tradecal.Session{Phase: tradecal.PreOpen}, nil
	}
	if err := p.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if desk.n.Load() != 2 || st.byID(2).Status != StatusSent {
		t.Fatalf("flush desk %d status %s", desk.n.Load(), st.byID(2).Status)
	}
}

func TestMergeAndFailover(t *testing.T) {
	p, st, desk, fei := newPipe(false)
	fei.failUntil = 2
	env := mustEnv(t, "datahub", events.SubjectNotifyRequest, map[string]any{
		"level": "error", "title": "盘中快照不可用", "body": "全挂了", "dedup_key": "datahub:snapshot",
	})
	if err := p.Handle(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if fei.n.Load() != 3 || desk.n.Load() != 1 || st.rows[0].Status != StatusSent {
		t.Fatalf("retry feishu %d desk %d status %s err %s", fei.n.Load(), desk.n.Load(), st.rows[0].Status, st.rows[0].FeishuError)
	}
	again := mustEnv(t, "datahub", events.SubjectNotifyRequest, map[string]any{
		"level": "error", "title": "盘中快照不可用", "body": "还是挂", "dedup_key": "datahub:snapshot",
	})
	if err := p.Handle(context.Background(), again); err != nil {
		t.Fatal(err)
	}
	if desk.n.Load() != 1 || st.byID(2).Status != StatusMerged || st.byID(1).MergeCount != 1 {
		t.Fatalf("merge desk %d second %s count %d", desk.n.Load(), st.byID(2).Status, st.byID(1).MergeCount)
	}
	if err := p.Handle(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if desk.n.Load() != 1 {
		t.Fatal("duplicate event_id resent")
	}

	p2, st2, desk2, fei2 := newPipe(false)
	fei2.err = errors.New("webhook down")
	fill := mustEnv(t, "trade", events.SubjectTradeFill, map[string]any{"symbol": "600000.SH", "side": "sell", "qty": 200, "price": 8, "client_order_id": "o9"})
	if err := p2.Handle(context.Background(), fill); err != nil {
		t.Fatal(err)
	}
	if st2.rows[0].Status != StatusPartial || desk2.n.Load() != 1 || fei2.n.Load() != 3 {
		t.Fatalf("failover status %s desk %d feishu %d", st2.rows[0].Status, desk2.n.Load(), fei2.n.Load())
	}
	raw, _ := json.Marshal(desk2.notes[0])
	var keys map[string]any
	_ = json.Unmarshal(raw, &keys)
	if _, ok := keys["action"]; ok {
		t.Fatal("desktop payload must not carry an action")
	}
	if keys["title"] == "" || keys["body"] == "" {
		t.Fatalf("desktop %+v", keys)
	}
}

func TestRecoverRespectsQuiet(t *testing.T) {
	p, st, desk, fei := newPipe(true)
	fei.skip = true
	st.next = 2
	st.rows = []Message{
		{ID: 1, EventID: "b", Category: "briefing", Priority: PriHigh, Status: StatusPending, Title: "晨报", DedupKey: "briefing:2026-10-09"},
		{ID: 2, EventID: "k", Category: "risk", Priority: PriCritical, Status: StatusPending, Title: "熔断", DedupKey: "risk:breaker:"},
	}
	if err := p.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st.byID(1).Status != StatusHeld || st.byID(2).Status != StatusSent || desk.n.Load() != 1 {
		t.Fatalf("held %s sent %s desk %d", st.byID(1).Status, st.byID(2).Status, desk.n.Load())
	}
}

func newPipe(quiet bool) (*Pipeline, *fakeStore, *fakeDesk, *fakeFei) {
	st := &fakeStore{}
	d := &fakeDesk{}
	f := &fakeFei{}
	p := NewPipeline(Config{MergeWindow: time.Minute, RetryMax: 3, RetryBase: time.Millisecond}, st, d, f)
	p.now = func() time.Time { return time.Date(2026, 10, 9, 2, 0, 0, 0, tradecal.Shanghai()) }
	p.session = func(time.Time) (tradecal.Session, error) {
		if quiet {
			return tradecal.Session{Phase: tradecal.Closed}, nil
		}
		return tradecal.Session{Phase: tradecal.AMTrading, Trading: true}, nil
	}
	p.sleep = func(time.Duration) {}
	return p, st, d, f
}

func mustEnv(t *testing.T, source, subject string, payload any) events.Envelope {
	t.Helper()
	env, err := events.New(source, subject, "trace", payload)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

type fakeStore struct {
	rows []Message
	next int
}

func (s *fakeStore) byID(id int) Message {
	for _, r := range s.rows {
		if r.ID == id {
			return r
		}
	}
	return Message{}
}

func (s *fakeStore) Insert(_ context.Context, msg Message) (Message, bool, error) {
	for _, r := range s.rows {
		if r.EventID == msg.EventID {
			return r, true, nil
		}
	}
	s.next++
	msg.ID = s.next
	if msg.Fields == nil {
		msg.Fields = map[string]string{}
	}
	s.rows = append(s.rows, msg)
	return msg, false, nil
}

func (s *fakeStore) Recent(_ context.Context, key string, since time.Time, excludeID int) (*Message, error) {
	var found *Message
	for i := range s.rows {
		r := &s.rows[i]
		if r.DedupKey != key || r.ID == excludeID || r.CreatedAt.Before(since) {
			continue
		}
		switch r.Status {
		case StatusSent, StatusPartial, StatusHeld, StatusPending:
		default:
			continue
		}
		if found == nil || r.CreatedAt.After(found.CreatedAt) {
			found = r
		}
	}
	return found, nil
}

func (s *fakeStore) MarkMerged(_ context.Context, id, anchorID int, latest Message) error {
	for i := range s.rows {
		if s.rows[i].ID == id {
			s.rows[i].Status = StatusMerged
			s.rows[i].MergedInto = anchorID
		}
		if s.rows[i].ID == anchorID {
			s.rows[i].MergeCount++
			if s.rows[i].Status == StatusHeld || s.rows[i].Status == StatusPending {
				s.rows[i].Title = latest.Title
				s.rows[i].Body = latest.Body
				s.rows[i].Fields = latest.Fields
			}
		}
	}
	return nil
}

func (s *fakeStore) MarkHeld(_ context.Context, id int) error {
	for i := range s.rows {
		if s.rows[i].ID == id {
			s.rows[i].Status = StatusHeld
		}
	}
	return nil
}

func (s *fakeStore) SaveDelivery(_ context.Context, msg Message) error {
	for i := range s.rows {
		if s.rows[i].ID == msg.ID {
			s.rows[i] = msg
		}
	}
	return nil
}

func (s *fakeStore) Get(_ context.Context, id int) (Message, error) {
	for _, r := range s.rows {
		if r.ID == id {
			return r, nil
		}
	}
	return Message{}, ErrNotFound
}

func (s *fakeStore) List(context.Context, int) ([]Message, error) { return s.rows, nil }

func (s *fakeStore) ListStatus(_ context.Context, status string) ([]Message, error) {
	var out []Message
	for _, r := range s.rows {
		if r.Status == status {
			out = append(out, r)
		}
	}
	return out, nil
}

type fakeDesk struct {
	n     atomic.Int32
	notes []DesktopNote
}

func (d *fakeDesk) Push(_ context.Context, note DesktopNote) error {
	d.n.Add(1)
	d.notes = append(d.notes, note)
	return nil
}

type fakeFei struct {
	n         atomic.Int32
	skip      bool
	failUntil int32
	err       error
}

func (f *fakeFei) Send(context.Context, string) error {
	n := f.n.Add(1)
	if f.skip {
		return ErrSkipped
	}
	if f.err != nil {
		return f.err
	}
	if n <= f.failUntil {
		return errors.New("temporary")
	}
	return nil
}
