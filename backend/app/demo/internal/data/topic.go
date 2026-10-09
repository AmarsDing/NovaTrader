package data

import (
	"context"
	"errors"
	"fmt"
	common "server/api/common/v1"
	"server/app/demo/internal/biz"

	"github.com/go-kratos/kratos/v2/log"
)

var _ biz.TopicRepo = (*topicCaseRepo)(nil)

type topicCaseRepo struct {
	data *Data
	subs map[string]string
	pubs map[string]string
	log  *log.Helper
}

func NewTopicRepo(data *Data, logger log.Logger) biz.TopicRepo {
	return &topicCaseRepo{
		data: data,
		subs: make(map[string]string),
		pubs: make(map[string]string),
		log:  log.NewHelper(log.With(logger, "admin", "data/gettopic")),
	}
}

func (tc *topicCaseRepo) GetTopic(ctx context.Context, code string) error {
	reply, err := tc.data.config.GetTopic(ctx, &common.GetTopicRequest{
		Appname: "admin",
	})
	if err != nil {
		return err
	}
	for _, pub := range reply.Pub {
		tc.pubs[pub.Key] = pub.Value
	}
	for _, sub := range reply.Sub {
		tc.subs[sub.Key] = sub.Value
	}
	return nil
}

func (tc *topicCaseRepo) GetSub(name string) string {
	if topic, b := tc.subs[name]; b {
		return topic
	}
	return ""
}
func (tc *topicCaseRepo) GetPub(name string) string {
	if topic, b := tc.pubs[name]; b {
		return topic
	}
	return ""
}

// nats  pub主题检查
func (tc *topicCaseRepo) CheckPubs(topics ...string) error {
	errstr := ""
	for _, topic := range topics {
		if t := tc.GetPub(topic); t == "" {
			errstr += fmt.Sprintf("[pub = %s: not exist] ", topic)
		}
	}
	if errstr != "" {
		return errors.New(errstr)
	}
	return nil
}

// nats  sub主题检查
func (tc *topicCaseRepo) CheckSubs(topics ...string) error {
	errstr := ""
	for _, topic := range topics {
		if t := tc.GetSub(topic); t == "" {
			errstr += fmt.Sprintf("[pub = %s: not exist] ", topic)
		}
	}
	if errstr != "" {
		return errors.New(errstr)
	}
	return nil
}
