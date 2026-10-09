// Package utils 提供 MySQL、MongoDB、Redis 的通用创建方法，供各 app 复用。
package utils

import (
	"context"
	"strings"
	"time"

	redis "github.com/go-redis/redis/v8"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// MySQLConfig MySQL 连接配置，与 conf.Database 中 MySQL 相关字段对应。
type MySQLConfig struct {
	Source string // DSN，如 root:pass@tcp(127.0.0.1:3306)/db?charset=utf8mb4
}

// MongoConfig MongoDB 连接配置，与 conf.Database 中 MongoDB 相关字段对应。
type MongoConfig struct {
	URI             string        // 连接串，如 mongodb://host:27017/?directConnection=true
	MaxPoolSize     uint64        // 最大连接池，0 使用默认 100
	MinPoolSize     uint64        // 最小连接池，0 使用默认 10
	ConnectTimeout  time.Duration // 连接超时，0 使用 5s
	PingTimeout     time.Duration // Ping 超时，0 使用 10s
	DisconnectTimeout time.Duration // 关闭时 Disconnect 超时，0 使用 5s
}

// RedisConfig Redis 连接配置，与 conf.Redis 对应。
type RedisConfig struct {
	Addr        string
	Password    string
	DB          int
	DialTimeout time.Duration // 0 使用 5s
}

// NewMySQL 根据配置创建 MySQL（GORM）连接，供各项目复用。
// cfg 为 nil 或 Source 为空时返回 (nil, nil)。
func NewMySQL(cfg *MySQLConfig) (*gorm.DB, error) {
	if cfg == nil || cfg.Source == "" {
		return nil, nil
	}
	return gorm.Open(mysql.Open(cfg.Source), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			SingularTable: true,
		},
	})
}

// NewMongoDB 根据配置创建 MongoDB 客户端，供各项目复用。
// cfg 为 nil 或 URI 为空时返回 (nil, noop, nil)。
// 调用方应在退出时执行返回的 cleanup 以关闭连接。
func NewMongoDB(cfg *MongoConfig) (*mongo.Client, func(), error) {
	if cfg == nil || cfg.URI == "" {
		return nil, func() {}, nil
	}
	maxPool := cfg.MaxPoolSize
	if maxPool == 0 {
		maxPool = 100
	}
	minPool := cfg.MinPoolSize
	if minPool == 0 {
		minPool = 10
	}
	connectTimeout := cfg.ConnectTimeout
	if connectTimeout == 0 {
		connectTimeout = 5 * time.Second
	}
	pingTimeout := cfg.PingTimeout
	if pingTimeout == 0 {
		pingTimeout = 10 * time.Second
	}
	disconnectTimeout := cfg.DisconnectTimeout
	if disconnectTimeout == 0 {
		disconnectTimeout = 5 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()

	opts := options.Client().
		ApplyURI(cfg.URI).
		SetMaxPoolSize(maxPool).
		SetMinPoolSize(minPool).
		SetConnectTimeout(connectTimeout)

	client, err := mongo.Connect(ctx, opts)
	if err != nil {
		return nil, nil, err
	}
	if err = client.Ping(ctx, nil); err != nil {
		return nil, nil, err
	}

	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), disconnectTimeout)
		defer cancel()
		_ = client.Disconnect(ctx)
	}
	return client, cleanup, nil
}

// MongoDisconnect 关闭 MongoDB 连接，忽略“已断开”类错误，便于在 cleanup 中调用。
func MongoDisconnect(client *mongo.Client, timeout time.Duration, onError func(err error)) {
	if client == nil {
		return
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := client.Disconnect(ctx); err != nil && onError != nil && !strings.Contains(err.Error(), "client is disconnected") {
		onError(err)
	}
}

// NewRedis 根据配置创建 Redis 客户端，供各项目复用。
// cfg 为 nil 或 Addr 为空时返回 (nil, nil)。
func NewRedis(cfg *RedisConfig) (*redis.Client, error) {
	if cfg == nil || cfg.Addr == "" {
		return nil, nil
	}
	dialTimeout := cfg.DialTimeout
	if dialTimeout == 0 {
		dialTimeout = 5 * time.Second
	}
	client := redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  dialTimeout,
	})
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, err
	}
	return client, nil
}
