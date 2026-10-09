package biz

import (
	"context"

	"github.com/go-kratos/kratos/v2/log"
)

var topic_instance *TopicCase

type TopicRepo interface {
	GetTopic(ctx context.Context, appname string) error
	GetSub(name string) string
	GetPub(name string) string
	CheckPubs(...string) error
	CheckSubs(...string) error
}

type TopicCase struct {
	repo TopicRepo
	log  *log.Helper
}

func NewTopicCase(repo TopicRepo, logger log.Logger) *TopicCase {
	if topic_instance == nil {
		topic_instance = &TopicCase{
			repo: repo,
			log:  log.NewHelper(log.With(logger, "snmpMonitor", "getconfig")),
		}
		return topic_instance
	}
	return topic_instance

}

func (tc *TopicCase) GetTopic(ctx context.Context, appname string) error {
	err := tc.repo.GetTopic(ctx, appname)
	if err != nil {
		return err
	}
	return nil
}

func (tc *TopicCase) GetSub(name string) string {
	return tc.repo.GetSub(name)
}

func (tc *TopicCase) GetPub(name string) string {
	return tc.repo.GetPub(name)
}

func (tc *TopicCase) CheckSubs(topics ...string) error {
	return tc.repo.CheckSubs(topics...)
}

func (tc *TopicCase) CheckPubs(topics ...string) error {
	return tc.repo.CheckPubs(topics...)
}
