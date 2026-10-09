/*
 * @Description: ##  描述文件功能  ##
 * @Author: AmarsDing
 * @Date: 2022-05-26 15:39:57
 * @Copyright: 北京迈特力德信息技术有限公司, METLED@2021
 */
package websocket

import "github.com/gogf/gf/v2/frame/g"

// 当前输入对象
type request struct {
	Event string `json:"e"` //事件名称
	Data  g.Map  `json:"d"` //数据
}

// WResponse 输出对象。
// e / d 是旧协议。topic / seq / data 是桌面端断线补推用的字段，两套同时带上。
type WResponse struct {
	Event   string      `json:"e,omitempty"`
	Data    interface{} `json:"d,omitempty"`
	Topic   string      `json:"topic,omitempty"`
	Seq     int64       `json:"seq,omitempty"`
	Payload interface{} `json:"data,omitempty"`
}

type TagWResponse struct {
	Tag       string
	WResponse *WResponse
}

type UserWResponse struct {
	UserID    uint64
	WResponse *WResponse
}

type ClientWResponse struct {
	ID        string
	WResponse *WResponse
}
