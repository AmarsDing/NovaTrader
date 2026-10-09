package core

import (
	"context"
	"errors"
	"server/app/demo/internal/biz"
	"server/conf"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(NewCoreServer, NewHeartbeat)

const (
	// 程序名称
	appname string = "admin"
	// sub 主题

	// 主题的用途:客户端发送切换线路的命令
	sub_linechange string = "linechange"

	// pub 主题

	// 发布线路的原始数据
	pub_origndata string = "origndata"
)

type ICoreServer interface {
	Start(context.Context) error
	Stop(context.Context) error
	Config() *biz.ConfigCase
}

// 读取配置

// 初始化命令监听

// 初始化心跳

// 初始化配置更动监听
type CoreServer struct {
	config    *biz.ConfigCase
	natscore  *biz.NatsCase
	topic     *biz.TopicCase
	Heartbeat *HeartBeat
	log       *log.Helper
	// 主机状态
	master bool
}

func NewCoreServer(config *biz.ConfigCase,
	nats *biz.NatsCase,
	topic *biz.TopicCase,
	Heartbeat *HeartBeat,
	dis *conf.Discovery,
	other *conf.Other,
	logger log.Logger) (*CoreServer, func(), error) {
	cs := &CoreServer{
		config:    config,
		natscore:  nats,
		topic:     topic,
		Heartbeat: Heartbeat,
		log:       log.NewHelper(log.With(logger, appname, "NewCoreServer")),
		master:    false,
	}
	return cs, func() {
		// 关闭心跳
		cs.Heartbeat.Close()
		// 关闭nats
		cs.natscore.Close()
	}, nil
}

func (cs *CoreServer) Start(ctx context.Context) error {

	// 从配置中心获取配置
	err := cs.config.GetConfig(ctx, appname)
	if err != nil {
		return err
	}
	// 从配置中心获取topic
	err = cs.topic.GetTopic(ctx, appname)
	if err != nil {
		return err
	}
	// topic检查是否存在
	err = cs.checkTopic()
	if err != nil {
		return err
	}
	// 启动心跳服务
	go cs.Heartbeat.RunHeartBeat()

	// 核心服务启动(将CoreServer指针传入  ||  看看能不能虚拟出接口)

	// -----------
	return nil
}

func (cs *CoreServer) Stop(ctx context.Context) error {

	// 关闭心跳
	cs.Heartbeat.Close()
	// 关闭nats
	cs.natscore.Close()

	return nil
}

// 获取配置
func (cs *CoreServer) Config() *biz.ConfigCase {
	return cs.config
}

// 获取当前服务的主备状态
func (cs *CoreServer) MasterStatus() bool {
	return cs.Heartbeat.GetStatus()
}

func (cs *CoreServer) checkTopic() error {
	errstr := ""
	if err := cs.checkPubs(); err != nil {
		errstr += err.Error()
	}
	if err := cs.checkSubs(); err != nil {
		errstr += err.Error()
	}
	if errstr != "" {
		return errors.New(errstr)
	}
	return nil
}

// 检查pub主题是否存在

func (cs *CoreServer) checkPubs() error {
	err := cs.topic.CheckPubs("")
	if err != nil {
		return err
	}
	return nil
}

// 检查sub主题是否存在
func (cs *CoreServer) checkSubs() error {
	err := cs.topic.CheckSubs("")
	if err != nil {
		return err
	}
	return nil
}
