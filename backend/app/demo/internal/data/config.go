package data

import (
	"context"
	v1 "server/api/demo/v1"
	"server/app/demo/internal/biz"

	"github.com/go-kratos/kratos/v2/log"
)

var _ biz.ConfigRepo = (*configCaseRepo)(nil)

type configCaseRepo struct {
	data *Data
	log  *log.Helper
}

func NewConfigRepo(data *Data, logger log.Logger) biz.ConfigRepo {
	return &configCaseRepo{
		data: data,
		log:  log.NewHelper(log.With(logger, "demo", "data/getconfig")),
	}
}

func (c *configCaseRepo) GetConfig(ctx context.Context, appname string) (*v1.ConfigReply, error) {
	reply, err := c.data.config.GetConfig(ctx, &v1.ConfigRequest{
		Appname: appname,
	})
	if err != nil {
		return nil, err
	}
	return reply, nil
}
