// Package registry 用数据库记录服务心跳。
package registry

import (
	"context"
	"fmt"
	"time"

	"server/ent"
	"server/ent/serviceinstance"
)

// Beater 供常驻循环刷新心跳，调用方不必持有 *ent.Client。
type Beater struct {
	client *ent.Client
}

// NewBeater 供 Wire 注入。
func NewBeater(client *ent.Client) *Beater {
	return &Beater{client: client}
}

// Beat 刷新一条心跳。
func (b *Beater) Beat(ctx context.Context, name, instanceID, addr string) error {
	if b == nil || b.client == nil {
		return fmt.Errorf("registry: no client")
	}
	return Beat(ctx, b.client, name, instanceID, addr)
}

// Beat 写入或刷新一条心跳。
func Beat(ctx context.Context, client *ent.Client, name, instanceID, addr string) error {
	row, err := client.ServiceInstance.Query().
		Where(
			serviceinstance.ServiceNameEQ(name),
			serviceinstance.InstanceIDEQ(instanceID),
		).Only(ctx)
	if ent.IsNotFound(err) {
		if err := client.ServiceInstance.Create().
			SetServiceName(name).
			SetInstanceID(instanceID).
			SetAddr(addr).
			Exec(ctx); err != nil {
			return fmt.Errorf("registry: create: %w", err)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if err := client.ServiceInstance.UpdateOneID(row.ID).
		SetAddr(addr).
		SetHeartbeatAt(time.Now()).
		Exec(ctx); err != nil {
		return fmt.Errorf("registry: beat: %w", err)
	}
	return nil
}

// Alive 返回 heartbeat_at 晚于 since 的实例。
func Alive(ctx context.Context, client *ent.Client, since time.Time) ([]*ent.ServiceInstance, error) {
	rows, err := client.ServiceInstance.Query().
		Where(serviceinstance.HeartbeatAtGT(since)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return rows, nil
}
