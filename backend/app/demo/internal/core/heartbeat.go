package core

// 监控心跳信息

//判断当前服务是否为主节点的对象
import (
	"context"
	"errors"

	common "server/api/common/v1"
	"server/app/demo/internal/biz"
	"server/conf"
	"sync"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/gogf/gf/encoding/gjson"
	"github.com/gogf/gf/util/gconv"
	"github.com/gogf/gf/v2/container/gmap"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtimer"
	"github.com/nats-io/nats.go"
)

const (
	heartbeat_topic             = "demo.heartbeat"
	HeartbeatExpirationTime int = 3
)

type IHeartBeat interface {
	RunHeartBeat()
	// Close()
}

type HeartBeat struct {
	dis         *conf.Discovery
	name        string //  服务名
	Status      bool
	Host        string
	log         *log.Helper
	nc          *biz.NatsCase
	topic       string
	insts       *gmap.StrIntMap
	entry       *gtimer.Entry
	StatusMutex sync.RWMutex
}

func NewHeartbeat(dis *conf.Discovery, logger log.Logger, nc *biz.NatsCase) *HeartBeat {
	return &HeartBeat{
		dis:   dis,
		name:  appname,
		Host:  dis.Host,
		log:   log.NewHelper(log.With(logger, appname, "hb")),
		nc:    nc,
		topic: heartbeat_topic,
		insts: gmap.NewStrIntMap(true),
	}
}

// 如果当前是主，并且程序关闭了，就使用此方法
func (hb *HeartBeat) Close() {
	if hb.entry != nil {
		hb.entry.Stop()
	}
}

// 定时获取当前状态，并向nats发布
func (hb *HeartBeat) RunHeartBeat() {
	err := hb.nc.RegistSub(hb.topic, func(m *nats.Msg) {
		var a = &common.HeartBeat{}
		err := hb.nc.RawNats().RawEncodeConn().Enc.Decode(hb.topic, m.Data, a)
		if err != nil {
			hb.log.Error(err)
			return
		}
		hb.insts.Set(a.Host, int(time.Now().Unix()))
	})
	if err != nil {
		hb.log.Error(err)
		return
	}
	hb.entry = gtimer.Add(context.TODO(), time.Second, func(ctx context.Context) {
		hb.sendHost()
		hb.ClearInst()
		hb.HostStatus()
	})
}

// 定时的方法clearTimeoutConnections
// 定时发送host的方法
func (hb *HeartBeat) sendHost() {
	err := hb.nc.Publish(hb.topic, &common.HeartBeat{Host: hb.Host})
	if err != nil {
		hb.log.Error(err)
	}
}

// 定时清理的方法
func (hb *HeartBeat) ClearInst() {
	currentTime := int(time.Now().Unix())
	var deleteKey = ""
	hb.insts.Iterator(func(host string, heartbeat int) bool {
		if heartbeat+HeartbeatExpirationTime < currentTime {
			deleteKey = host
		}
		return true
	})
	if deleteKey != "" {
		hb.insts.Remove(deleteKey)
	}
}

// 从discovery中查询当前节点的
func (hb *HeartBeat) HostStatus() {
	s, err := hb.getPriHostName()
	if err != nil {
		hb.log.Error(err)
		return
	}
	hb.StatusMutex.Lock()
	if hb.Host == s {
		hb.Status = true
	} else {
		hb.Status = false
	}
	hb.StatusMutex.Unlock()
}
func (hb *HeartBeat) GetStatus() bool {
	return hb.Status
}

// 获取可用ip
func (hb *HeartBeat) getPriHostName() (string, error) {
	for _, v := range hb.dis.Nodes {
		path := "/discovery/fetch?zone=" + hb.dis.Zone + "&env=" + hb.dis.Env + "&appid=" + hb.name + "&status=" + "1"
		r, err := g.Client().Get(context.TODO(), "http://"+v+path)
		if err != nil {
			r.Close()
			continue
		} else {
			s := r.ReadAllString()
			instances, err2 := hb.getHttpNodeByJson(s)
			if err2 != nil {
				r.Close()
				return "", err2
			}
			hostName := hb.getHostNameByWeight(instances)
			r.Close()
			return hostName, nil
		}
	}
	return "", errors.New("获取失败")
}

// 获取http转发的实例列表
func (hb *HeartBeat) getHttpNodeByJson(s string) ([]Instance, error) {
	j := gjson.New(s, true)
	if j.GetInt64("code") != 0 {
		return nil, errors.New(j.GetString("message"))
	}
	var instances []Instance
	for _, v := range j.GetMap("data.instances") {
		var instance []Instance
		gconv.Structs(v, &instance)
		instances = append(instances, instance...)
	}
	return instances, nil
}

// 获取权重大的节点ip
func (hb *HeartBeat) getHostNameByWeight(instances []Instance) string {
	//获取切片中的metadata信息中的权重并得到最大的权重的那个
	var weight int
	var hostName string
	for _, instance := range instances {
		val := hb.insts.Get(instance.Hostname)
		if val == 0 {
			continue
		}
		s := instance.Metadata
		j2 := gjson.New(s)
		weight1 := j2.GetInt("weight")
		if weight1 < weight {
			continue
		} else if weight1 == weight {
			if instance.Hostname <= hostName {
				continue
			}
		}
		weight = weight1
		hostName = instance.Hostname
	}
	return hostName
}

// 定义实例的结构体
type Instance struct {
	Region          string   `json:"region"`
	Zone            string   `json:"zone"`
	Env             string   `json:"env"`
	Appid           string   `json:"appid"`
	Hostname        string   `json:"hostname"`
	Addrs           []string `json:"addrs"`
	Version         string   `json:"version"`
	Metadata        string   `json:"metadata"`
	Status          int64    `json:"status"`
	RegTimestamp    int64    `json:"reg_timestamp"`
	UpTimestamp     int64    `json:"up_timestamp"`
	RenewTimestamp  int64    `json:"renew_timestamp"`
	DirtyTimestamp  int64    `json:"dirty_timestamp"`
	LatestTimestamp int64    `json:"latest_timestamp"`
}
