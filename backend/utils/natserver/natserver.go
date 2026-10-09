package natserver

import (
	"fmt"
	"server/conf"
	"time"

	"github.com/nats-io/nats.go"
)

type NatServer struct {
	nc      *nats.Conn
	enc     *nats.EncodedConn
	encoder string
}

// 2 nats 初始化
func NewNats(natsconfig *conf.Nats, Encoders string) *NatServer {
	var nc *nats.Conn
	var err error
	if nc, err = nats.Connect(natsconfig.GetUrl(),
		nats.DontRandomize(),
		// 重连次数
		nats.MaxReconnects(-1),
		// 重连缓冲区大小 1M = 1 * 1024 * 1024
		nats.ReconnectBufSize(10*1024*1024),
		// 重连等待时间
		nats.ReconnectWait(3*time.Second),
		nats.DisconnectHandler(NatsDisconnectHandler),
		nats.DisconnectErrHandler(NatsDisconnectErrHandler),
		nats.ReconnectHandler(NatsReconnectHandler),
		nats.ClosedHandler(NatsClosedHandler),
		nats.DiscoveredServersHandler(NatsDiscoveredServersHandler),
		nats.ErrorHandler(NatsErrorHandler)); err != nil {
		panic(err)
	}

	var enc *nats.EncodedConn
	// json = nats.JSON_ENCODER
	// protobuf = protobuf.PROTOBUF_ENCODER
	if enc, err = nats.NewEncodedConn(nc, Encoders); err != nil {
		panic(err)
	}

	return &NatServer{
		nc:      nc,
		enc:     enc,
		encoder: Encoders,
	}
}

// NATS 断开连接操作
func NatsDisconnectHandler(nc *nats.Conn) {
	if nc.IsClosed() {
		fmt.Printf("nats disconnect: %s", nc.ConnectedUrl())
		return
	}
	nc.Close()
	fmt.Printf("nats disconnect: %s", nc.ConnectedUrl())
}
func NatsDisconnectErrHandler(nc *nats.Conn, err error) {
	if err != nil {
		fmt.Printf("nats NatsDisconnectErrHandler : %s", err.Error())
		return
	}
}

// NATS 重连操作
func NatsReconnectHandler(nc *nats.Conn) {
	fmt.Printf("nats reconnect: %s", nc.ConnectedUrl())
}

// NATS 关闭操作
func NatsClosedHandler(nc *nats.Conn) {
	if nc.IsClosed() {
		fmt.Printf("nats disconnect: %s", nc.ConnectedUrl())
		return
	}
	nc.Close()
	fmt.Printf("Exiting: %v", nc.LastError())
}

// NATS 发现服务操作
func NatsDiscoveredServersHandler(nc *nats.Conn) {
	fmt.Printf("nats discovered servers: %s", nc.ConnectedUrl())
}

// NATS 操作过程中出现错误处理
func NatsErrorHandler(nc *nats.Conn, subscription *nats.Subscription, e error) {

	if nc.IsClosed() {
		fmt.Printf("nats disconnect: %s", nc.ConnectedUrl())
		return
	}
	nc.Close()
	fmt.Printf("nats error: %s", e.Error())
}

func (ns *NatServer) RawEncodeConn() *nats.EncodedConn {
	return ns.enc
}

func (ns *NatServer) RawConn() *nats.Conn {
	return ns.nc
}

func (ns *NatServer) Public(topic string, msg interface{}) error {

	if err := ns.enc.Publish(topic, msg); err != nil {
		return err
	}
	err := ns.enc.Flush()
	if err != nil {
		return err
	}
	err = ns.enc.LastError()
	if err != nil {
		return err
	}
	return nil
}

func (ns *NatServer) RegistSub(topic string, handler func(*nats.Msg)) (*nats.Subscription, error) {

	subscribe, err := ns.enc.Subscribe(topic, handler)

	if err != nil {
		return nil, err
	}

	err = ns.enc.Flush()

	if err != nil {
		return nil, err
	}

	return subscribe, nil
}

func (ns *NatServer) RegistSubQueque(topic, cluster string, handler func(*nats.Msg)) (*nats.Subscription, error) {

	subscribe, err := ns.enc.QueueSubscribe(topic, cluster, handler)

	if err != nil {
		return nil, err
	}

	return subscribe, nil
}

func (ns *NatServer) PubReply() {

}

func (ns *NatServer) Close() {
	ns.enc.Close()
	ns.nc.Close()
}
