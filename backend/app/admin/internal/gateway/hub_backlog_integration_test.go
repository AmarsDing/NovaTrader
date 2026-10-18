package gateway

// 持久积压最老年龄的隔离 NATS 验收（N2-01）。
// 纪律与 pkg/events 同款：NT_TEST_NATS_URL 必须指向独立 loopback 端口，绝不许 4222；
// 设了却连不上直接 FAIL，不容 SKIP 充数。不删流、不删共享 durable。

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"server/pkg/events"
)

func isolatedNatsURL(t *testing.T) string {
	t.Helper()
	value := os.Getenv("NT_TEST_NATS_URL")
	if value == "" {
		t.Skip("set NT_TEST_NATS_URL to an isolated loopback NATS test server")
	}
	u, err := url.Parse(value)
	if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") || u.Port() == "" || u.Port() == "4222" {
		t.Fatal("NT_TEST_NATS_URL must use a separate loopback test port, never production 4222")
	}
	return value
}

// 助手逻辑验收：专用丢弃流上验证主题过滤、两类年龄与无积压口径。
func TestOldestBacklogOnThrowawayStream(t *testing.T) {
	nc, err := nats.Connect(isolatedNatsURL(t), nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()[:8]
	stream := "N2AGE" + strings.ToUpper(id)
	hit := "n2age." + id + ".hit"
	if _, err := js.AddStream(&nats.StreamConfig{Name: stream, Subjects: []string{"n2age." + id + ".>"}}); err != nil {
		t.Fatal(err)
	}
	durable := "probe-" + id
	// Pull 型 durable：未拉取前全部积压；拉取后不确认可造 ack_pending。
	if _, err := js.AddConsumer(stream, &nats.ConsumerConfig{Durable: durable, FilterSubject: hit, AckPolicy: nats.AckExplicitPolicy, DeliverPolicy: nats.DeliverAllPolicy}); err != nil {
		t.Fatal(err)
	}
	// 一条主题不符的夹缝消息：最老积压必须跳过它指向第一条 hit。
	if _, err := js.Publish("n2age."+id+".miss", []byte("x")); err != nil {
		t.Fatal(err)
	}
	publishedAt := time.Now()
	if _, err := js.Publish(hit, []byte("first")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if _, err := js.Publish(hit, []byte("second")); err != nil {
		t.Fatal(err)
	}

	before := time.Now()
	ci, err := js.ConsumerInfo(stream, durable)
	if err != nil {
		t.Fatal(err)
	}
	if ci.NumPending != 2 {
		t.Fatalf("pending = %d, want 2", ci.NumPending)
	}
	ages := oldestBacklog(js, stream, hit, ci, before)
	if ages.PendingAgeMs < 0 {
		t.Fatalf("pending age unresolvable: %s", ages.Basis)
	}
	// 最老积压是第一（而非夹缝/第二）条：年龄不小且不过界。
	if ages.PendingAgeMs < 100 || ages.PendingAgeMs > 60000 {
		t.Fatalf("pending age %dms out of bounds (published %v)", ages.PendingAgeMs, publishedAt)
	}
	if ages.AckAgeMs != -1 {
		t.Fatalf("no delivery yet, ack age must be -1, got %d", ages.AckAgeMs)
	}

	// 拉两条、只确认第二条：floor+1 恰落在未确认的第一条上。
	sub, err := js.PullSubscribe(hit, durable, nats.Bind(stream, durable))
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := sub.Fetch(2, nats.MaxWait(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || string(msgs[0].Data) != "first" {
		t.Fatalf("fetched %v", msgs)
	}
	if err := msgs[1].AckSync(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	ci, err = js.ConsumerInfo(stream, durable)
	if err != nil {
		t.Fatal(err)
	}
	if ci.NumAckPending != 1 {
		t.Fatalf("ack_pending = %d, want 1", ci.NumAckPending)
	}
	ages = oldestBacklog(js, stream, hit, ci, time.Now())
	if ages.AckAgeMs < 100 || ages.AckAgeMs > 60000 {
		t.Fatalf("ack age %dms must cover the unacked first message (%s)", ages.AckAgeMs, ages.Basis)
	}
	// 全部投递完后 pending 归零：年龄如实 -1。
	if ages.PendingAgeMs != -1 {
		t.Fatalf("pending drained, age must be -1, got %d", ages.PendingAgeMs)
	}
	_ = sub.Unsubscribe()
}

// Stats 行结构验收：真实事件流上给 admin_notify_desktop 补 durable + 发一条真实事件，
// 行必须带三类年龄字段与口径说明；pending==0 时年龄如实 -1。
func TestHubStatsReportsOldestBacklogAges(t *testing.T) {
	bus, err := events.Dial(isolatedNatsURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()
	js := bus.JetStream()
	// durable 可能因早期套件已存在；AddConsumer 幂等。走与 bind 同形的配置。
	if _, err := js.AddConsumer(events.StreamName, &nats.ConsumerConfig{
		Durable: "admin_notify_desktop", FilterSubject: events.SubjectNotifyDesktop,
		AckPolicy: nats.AckExplicitPolicy, AckWait: 10 * time.Second, MaxDeliver: 5,
	}); err != nil {
		t.Fatal(err)
	}
	env, err := events.New("admin-backlog-test", events.SubjectNotifyDesktop, "n2-age-"+uuid.NewString()[:8], map[string]any{"probe": true})
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	h := &Hub{}
	h.setBus(bus)
	stats := h.Stats()
	if stats["connected"] != true {
		t.Fatalf("stats = %+v", stats)
	}
	stream, ok := stats["stream"].(map[string]any)
	if !ok {
		t.Fatalf("stream section missing: %+v", stats)
	}
	var row map[string]any
	for _, item := range stream["consumers"].([]map[string]any) {
		if item["durable"] == "admin_notify_desktop" {
			row = item
		}
	}
	if row == nil {
		t.Fatalf("admin_notify_desktop row missing: %+v", stream["consumers"])
	}
	if missing, _ := row["missing"].(bool); missing {
		t.Fatalf("durable was just added but row says missing: %+v", row)
	}
	pending := statInt(row["pending"])
	if pending < 1 {
		t.Fatalf("published one event undelivered, pending = %d (%+v)", pending, row)
	}
	age, ok := row["oldest_pending_age_ms"].(int64)
	if !ok || age < 0 {
		t.Fatalf("oldest_pending_age_ms must be a measurable non-negative: %+v", row)
	}
	// 不框上界：隔离流是共享实例，早期套件可能留有更老的未投递积压，
	// 最老年龄指向 leftover 才是真实行为；精确数值界在专用丢弃流用例里断言。
	if _, ok := row["oldest_ack_age_ms"].(int64); !ok {
		t.Fatalf("oldest_ack_age_ms missing: %+v", row)
	}
	if basis, _ := row["oldest_age_basis"].(string); basis == "" {
		t.Fatalf("basis missing: %+v", row)
	}
}

// Stats 行里的计数随来源库版本可能是 int / int64 / uint64，统一提取。
func statInt(value any) int64 {
	switch v := value.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case uint64:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return -1
	}
}
