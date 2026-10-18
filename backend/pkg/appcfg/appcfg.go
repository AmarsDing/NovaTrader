package appcfg

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"server/ent"
	"server/pkg/params"
)

// 配置页写入 strategy_config 的 app.* 键。各服务启动后按这个缓存读取，盖过配置文件。

const ttl = 5 * time.Second

type cache struct {
	client   *ent.Client
	mu       sync.Mutex
	at       time.Time
	rows     map[string]string
	revision int
	failed   bool
	receipts map[string]time.Time
}

var current atomic.Pointer[cache]

// Bind 让本进程之后的 Get 读这份库。各服务打开 ent 时调用一次。
func Bind(client *ent.Client) {
	if client == nil {
		return
	}
	current.Store(&cache{client: client})
}

// Get 返回一个键。没绑定或库暂时读不到时返回空字符串。
func Get(ctx context.Context, key string) string {
	if key == "" {
		return ""
	}
	c := current.Load()
	if c == nil {
		return ""
	}
	service, _ := params.Target(key)
	rows := c.snapshot(ctx, service)
	return rows[key]
}

// Snapshot 返回当前 app 配置的副本。
func Snapshot(ctx context.Context) map[string]string {
	c := current.Load()
	if c == nil {
		return map[string]string{}
	}
	return c.snapshot(ctx, "brain")
}

func (c *cache) snapshot(ctx context.Context, service string) map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	defer c.ack(ctx, service)
	if c.rows != nil && time.Since(c.at) < ttl {
		return clone(c.rows)
	}
	loaded, err := c.load(ctx)
	if err != nil {
		c.failed = true
		if c.rows == nil {
			c.rows = map[string]string{}
		}
		c.at = time.Now()
		return clone(c.rows)
	}
	c.failed = false
	if c.revision != loaded.Revision {
		c.receipts = nil
	}
	c.revision = loaded.Revision
	c.rows = loaded.Values
	c.at = time.Now()
	return clone(c.rows)
}

func (c *cache) load(ctx context.Context) (params.Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return params.Read(ctx, c.client)
}

func (c *cache) ack(ctx context.Context, service string) {
	if service == "unknown" {
		return
	}
	if time.Since(c.receipts[service]) < 30*time.Second {
		return
	}
	status := "cache_loaded"
	if c.failed {
		status = "failed"
	}
	ackCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if params.Record(ackCtx, c.client, service, "app_cache", c.revision, status) == nil {
		if c.receipts == nil {
			c.receipts = map[string]time.Time{}
		}
		c.receipts[service] = time.Now()
	}
}

func clone(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
