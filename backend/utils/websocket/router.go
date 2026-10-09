/*
 * @Description: ##  描述文件功能  ##
 * @Author: AmarsDing
 * @Date: 2022-05-26 15:39:57
 * @Copyright: 北京迈特力德信息技术有限公司, METLED@2021
 */
package websocket

import (
	"github.com/gogf/gf/encoding/gjson"
)

const (
	Error = "error"
	Login = "login"
	Join  = "join"
	Quit  = "quit"
	IsApp = "is_app"
	Ping  = "ping"
)

// TextHandler 处理桌面端的订阅消息。返回 true 表示这条消息已经消化，不再走旧的 e/d 协议。
var TextHandler func(client *Client, message []byte) bool

// ProcessData 处理数据
func ProcessData(client *Client, message []byte) {
	defer func() {
		if r := recover(); r != nil {
			clientManager.log.Error("处理数据 stop", r)
			// fmt.Println("处理数据 stop", r)
		}
	}()
	if TextHandler != nil && TextHandler(client, message) {
		return
	}
	json, err := gjson.DecodeToJson(message)
	if err != nil {
		// fmt.Println(err)
		clientManager.log.Error("数据解析失败：", err)
	}

	request := &request{}
	err = json.Scan(request)
	if err != nil {
		// fmt.Println("数据解析失败：", err)
		clientManager.log.Error("数据解析失败：", err)
		return
	}
	switch request.Event {
	case Login:
		LoginController(client, request)

	case Join:
		JoinController(client, request)

	case Quit:
		QuitController(client, request)

	case IsApp:
		IsAppController(client)

	case Ping:
		PingController(client)

	}
}
