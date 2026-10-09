package data

import (
	"server/app/demo/internal/biz"
	"server/conf"
	"server/utils/natserver"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/encoders/protobuf"
)

var _ biz.NatsRepo = (*NatsCoreRepo)(nil)

var (
	NatsInstance *NatsCoreRepo = nil
)

type NatsCoreRepo struct {
	nats *natserver.NatServer
}

func NewNatsCoreRepo(natsconfig *conf.Nats) biz.NatsRepo {
	if NatsInstance == nil {
		NatsInstance = &NatsCoreRepo{
			nats: natserver.NewNats(natsconfig, protobuf.PROTOBUF_ENCODER),
		}
		return NatsInstance
	}
	return NatsInstance
}

// nats 注册监听
func (nc *NatsCoreRepo) RegistSub(topic string, handle func(*nats.Msg)) error {
	_, err := nc.nats.RegistSub(topic, handle)
	if err != nil {
		return err
	}
	return nil
}

// nats 发布消息
func (nc *NatsCoreRepo) Publish(topic string, msg interface{}) error {
	err := nc.nats.Public(topic, msg)
	if err != nil {
		return err
	}
	return nil
}

func (nc *NatsCoreRepo) RawNats() *natserver.NatServer {
	return nc.nats
}

func (nc *NatsCoreRepo) Close() {
	nc.nats.Close()
}
