package biz

import (
	"server/utils/natserver"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/nats-io/nats.go"
)

var nats_instance *NatsCase

type NatsRepo interface {
	RegistSub(topic string, handle func(*nats.Msg)) error
	Publish(topic string, msg interface{}) error
	RawNats() *natserver.NatServer
	Close()
}

type NatsCase struct {
	repo NatsRepo
	log  *log.Helper
}

func NewNatsCase(repo NatsRepo, logger log.Logger) *NatsCase {
	if nats_instance == nil {
		nats_instance = &NatsCase{
			repo: repo,
			log:  log.NewHelper(log.With(logger, "admin", "natscase")),
		}
		return nats_instance
	}
	return nats_instance

}

func (nc *NatsCase) RegistSub(topic string, handle func(*nats.Msg)) error {
	return nc.repo.RegistSub(topic, handle)
}
func (nc *NatsCase) Publish(topic string, msg interface{}) error {
	return nc.repo.Publish(topic, msg)
}
func (nc *NatsCase) RawNats() *natserver.NatServer {
	return nc.repo.RawNats()
}
func (nc *NatsCase) Close() {
	nc.repo.Close()
}
