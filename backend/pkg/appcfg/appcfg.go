package appcfg

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"server/ent"
	"server/ent/strategyconfig"
)

// 配置页写入 strategy_config 的 app.* 键。各服务启动后按这个缓存读取，盖过配置文件。

const ttl = 5 * time.Second

type cache struct {
	client *ent.Client
	mu     sync.Mutex
	at     time.Time
	rows   map[string]string
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
	rows := Snapshot(ctx)
	return rows[key]
}

// Snapshot 返回当前 app 配置的副本。
func Snapshot(ctx context.Context) map[string]string {
	c := current.Load()
	if c == nil {
		return map[string]string{}
	}
	return c.snapshot(ctx)
}

func (c *cache) snapshot(ctx context.Context) map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rows != nil && time.Since(c.at) < ttl {
		return clone(c.rows)
	}
	loaded, err := c.load(ctx)
	if err != nil {
		if c.rows == nil {
			c.rows = map[string]string{}
		}
		c.at = time.Now()
		return clone(c.rows)
	}
	c.rows = loaded
	c.at = time.Now()
	return clone(c.rows)
}

func (c *cache) load(ctx context.Context) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	rows, err := c.client.StrategyConfig.Query().
		Where(strategyconfig.ConfigKeyHasPrefix("app.")).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.ConfigKey] = row.ConfigValue
	}
	return out, nil
}

func clone(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
