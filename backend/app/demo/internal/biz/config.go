package biz

import (
	"context"
	v1 "server/api/demo/v1"

	"github.com/go-kratos/kratos/v2/log"
)

type ConfigRepo interface {
	GetConfig(ctx context.Context, code string) (*v1.ConfigReply, error)
}

var config_instance *ConfigCase

type ConfigCase struct {
	repo ConfigRepo
	conf *v1.ConfigReply
	log  *log.Helper
}

func NewConfigCase(repo ConfigRepo, logger log.Logger) *ConfigCase {
	if config_instance == nil {
		config_instance = &ConfigCase{repo: repo, conf: nil, log: log.NewHelper(log.With(logger, "linemonitor", "getconfig"))}
		return config_instance
	}
	return config_instance
}

func (cc *ConfigCase) GetConfig(ctx context.Context, appname string) error {
	cfg, err := cc.repo.GetConfig(ctx, appname)
	if err != nil {
		return err
	}
	cc.conf = cfg
	return nil
}
