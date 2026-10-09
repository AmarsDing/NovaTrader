package websocket

import (
	"runtime/debug"
	"sync"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/gogf/gf/v2/container/garray"
	"github.com/gogf/gf/v2/util/guid"
	"github.com/gorilla/websocket"
)

const (
	// 用户连接超时时间
	heartbeatExpirationTime = 6 * 60
)

// 用户登录
type login struct {
	UserId uint64
	Client *Client
}

// GetKey 读取客户端数据
func (l *login) GetKey() (key string) {
	key = GetUserKey(l.UserId)
	return
}

// Client 客户端连接
type Client struct {
	Addr          string          // 客户端地址
	ID            string          // 连接唯一标识
	Socket        *websocket.Conn // 用户连接
	Send          chan *WResponse // 待发送的数据
	SendClose     bool            // 发送是否关闭
	UserId        uint64          // 用户ID，用户登录以后才有
	FirstTime     uint64          // 首次连接事件
	HeartbeatTime uint64          // 用户上次心跳时间
	LoginTime     uint64          // 登录时间 登录以后才有
	isApp         bool            // 是否是app
	tags          garray.StrArray // 标签
	log           *log.Helper
	topics        map[string]struct{}
	topicMu       sync.Mutex
}

// NewClient 初始化
func NewClient(addr string, socket *websocket.Conn, firstTime uint64, log *log.Helper) (client *Client) {
	client = &Client{
		Addr:          addr,
		ID:            guid.S(),
		Socket:        socket,
		Send:          make(chan *WResponse, 100),
		SendClose:     false,
		FirstTime:     firstTime,
		HeartbeatTime: firstTime,
		log:           log,
	}
	return
}

// 读取客户端数据
func (c *Client) read() {
	defer func() {
		if r := recover(); r != nil {
			c.log.Error("write stop", string(debug.Stack()), r)
		}
	}()

	defer func() {
		c.close()
	}()

	for {
		_, message, err := c.Socket.ReadMessage()
		if err != nil {
			return
		}
		// 处理程序
		// fmt.Println(message)
		ProcessData(c, message)
	}
}

// 向客户端写数据
func (c *Client) write() {
	defer func() {
		if r := recover(); r != nil {
			c.log.Error("write stop", string(debug.Stack()), r)
		}
	}()
	defer func() {
		clientManager.Unregister <- c
		_ = c.Socket.Close()
	}()
	for {
		select {
		case message, ok := <-c.Send:
			if !ok {
				// 发送数据错误 关闭连接
				return
			}
			err := c.Socket.WriteJSON(message)
			if err != nil {
				c.log.Error(err)
				return
			}
		}
	}
}

// SendMsg 发送数据
func (c *Client) SendMsg(msg *WResponse) {
	if c == nil || c.SendClose {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			// fmt.Println("SendMsg stop:", r, string(debug.Stack()))
			c.log.Error("SendMsg stop", r, string(debug.Stack()))
		}
	}()
	c.Send <- msg
}

// SetTopics 替换这条连接订阅的主题。
func (c *Client) SetTopics(list []string) {
	if c == nil {
		return
	}
	next := make(map[string]struct{}, len(list))
	for _, topic := range list {
		if topic != "" {
			next[topic] = struct{}{}
		}
	}
	c.topicMu.Lock()
	c.topics = next
	c.topicMu.Unlock()
}

// Allows 表示这条连接订阅了该主题。
func (c *Client) Allows(topic string) bool {
	if c == nil {
		return false
	}
	c.topicMu.Lock()
	defer c.topicMu.Unlock()
	_, ok := c.topics[topic]
	return ok
}

// Heartbeat 心跳更新
func (c *Client) Heartbeat(currentTime uint64) {
	c.HeartbeatTime = currentTime
	return
}

// IsHeartbeatTimeout 心跳是否超时
func (c *Client) IsHeartbeatTimeout(currentTime uint64) (timeout bool) {
	if c.HeartbeatTime+heartbeatExpirationTime <= currentTime {
		// panic("============超时")
		timeout = true
	}
	return
}

// 关闭客户端
func (c *Client) close() {
	if c.SendClose {
		return
	}
	c.SendClose = true
	close(c.Send)
}
