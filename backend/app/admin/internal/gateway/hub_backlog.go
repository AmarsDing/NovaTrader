package gateway

import (
	"errors"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

// BacklogAge 是一个 durable 消费者的持久积压最老年龄（毫秒，时间戳为 NATS 服务端入流时刻）。
// 任一方向取不到就是 -1，绝不拿 0 充当「没有积压」——原因写在 basis 里。
type BacklogAge struct {
	PendingAgeMs int64 // 已入流、未投递的最老消息年龄
	AckAgeMs     int64 // 已投递、未确认的最老消息年龄
	Basis        string
}

// 沿 delivered/floor 向后找第一条匹配消息的最大探测步数。过滤器基本都是单主题，
// 64 步足够跨过少量不匹配的夹缝；跨不过就如实报 -1，不猜下一条。
const backlogScanLimit = 64

// oldestBacklog 计算过滤消费者的两类最老年龄。
// 口径：pending = Delivered.Stream 之后第一条主题匹配消息的入流时刻；
// ack = AckFloor.Stream+1（显式 ack 的连续语义保证该条未确认）。
// 消息时间为流服务端时钟；与 now（网关时钟）为负差说明跨主机时钟偏差，
// 此时如实报 -1 而不是给一个假精度的数字。
func oldestBacklog(js nats.JetStreamContext, stream, filter string, ci *nats.ConsumerInfo, now time.Time) BacklogAge {
	out := BacklogAge{PendingAgeMs: -1, AckAgeMs: -1}
	pendingBasis := "无积压"
	ackBasis := "无未确认"

	if ci.NumPending > 0 {
		switch {
		case strings.ContainsAny(filter, "*>"):
			pendingBasis = "通配过滤最老未投递未接入"
		default:
			seq := ci.Delivered.Stream + 1
			if seq == 0 {
				seq = 1
			}
			found := false
			for probe := 0; probe < backlogScanLimit; probe++ {
				msg, err := js.GetMsg(stream, seq)
				if err != nil {
					if errors.Is(err, nats.ErrMsgNotFound) {
						seq++
						continue
					}
					pendingBasis = "取流内消息失败"
					break
				}
				if msg.Subject != filter {
					seq++
					continue
				}
				found = true
				out.PendingAgeMs = ageOrUnmeasurable(now, msg.Time)
				if out.PendingAgeMs < 0 {
					pendingBasis = "时钟偏差年龄为负不可信"
				} else {
					pendingBasis = "delivered后首条匹配"
				}
				break
			}
			if !found && pendingBasis != "取流内消息失败" && pendingBasis != "时钟偏差年龄为负不可信" {
				pendingBasis = "超出探测窗口未定位"
			}
		}
	}

	if ci.NumAckPending > 0 {
		// AckFloor 是流序列（跨主题）：floor+1 可能落在不匹配的夹缝消息上，
		// 必须像 pending 一样向后扫到第一条匹配消息——显式 ack 的连续语义
		// 保证 floor 之后第一条本消费者的消息仍未确认。
		if strings.ContainsAny(filter, "*>") {
			ackBasis = "通配过滤最老未确认未接入"
		} else {
			seq := ci.AckFloor.Stream + 1
			if seq == 0 {
				seq = 1
			}
			found := false
			for probe := 0; probe < backlogScanLimit; probe++ {
				msg, err := js.GetMsg(stream, seq)
				if err != nil {
					if errors.Is(err, nats.ErrMsgNotFound) {
						seq++
						continue
					}
					ackBasis = "取ack_floor后首条失败"
					break
				}
				if msg.Subject != filter {
					seq++
					continue
				}
				found = true
				out.AckAgeMs = ageOrUnmeasurable(now, msg.Time)
				if out.AckAgeMs < 0 {
					ackBasis = "时钟偏差年龄为负不可信"
				} else {
					ackBasis = "ack_floor后首条匹配"
				}
				break
			}
			if !found && ackBasis != "取ack_floor后首条失败" && ackBasis != "时钟偏差年龄为负不可信" {
				ackBasis = "超出探测窗口未定位"
			}
		}
	}

	out.Basis = "pending:" + pendingBasis + ";ack:" + ackBasis
	return out
}

func ageOrUnmeasurable(now, at time.Time) int64 {
	if at.IsZero() {
		return -1
	}
	age := now.Sub(at).Milliseconds()
	if age < 0 {
		return -1
	}
	return age
}
