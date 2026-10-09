package notice

import (
	"context"
	"fmt"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/util/gconv"
)

//需要传入的数据
type Message struct {
	Type    string  ` json:"type,omitempty"`
	Level   string  ` json:"level,omitempty"`
	Content string  `json:"content,omitempty"`
	Method  *Method `json:"method,omitempty"`
}
type Method struct {
	Wechat *Wechat `json:"wechat,omitempty"`
	Sms    *Sms    `json:"sms,omitempty"`
	MFM920 *MFM920 `json:"mfm,omitempty"`
}

//这里定义微信发送信息需要的配置
type Wechat struct {
	Tousers    []string `json:"tousers,omitempty"`    //企业号中的用户账号
	Toparty    string   `json:"toparty,omitempty"`    //企业号中的部门id
	Agentid    string   `json:"agentid,omitempty"`    //企业号中的应用id
	Corpid     string   `json:"corpid,omitempty"`     //企业号的标识
	Corpsecret string   `json:"corpsecret,omitempty"` //企业号的应用的secret
}

//这里哦定义发送短信需要的配置
type Sms struct {
	AccessKeyId     string   `json:"accessKeyId,omitempty"`     //AccessKey ID
	AccessKeySecret string   `json:"accessKeySecret,omitempty"` //AccessKey Secret
	SignName        string   `json:"signName,omitempty"`        //短信签名名称
	TemplateCode    string   `json:"templateCode,omitempty"`    //短信模板CODE
	PhoneNumbers    []string `json:"phoneNumbers,omitempty"`    //接收短信的手机号码
}

//这里哦定义MFM920发送告警信息需要的配置
type MFM920 struct {
	Msg []*Msg `json:"msg,omitempty"`
}
type Msg struct {
	Address string `json:"address,omitempty"`
	Type    string `json:"type,omitempty"`
	To      string `json:"to,omitempty"`
	Sub     string `json:"sub,omitempty"` //邮件主题，当通知类型为右键的时候，必须得有邮件主题，才能发送邮件信息
}

//配置中notice的基本信息
type Cfg struct {
	Sms    SmsCfg    `json:"sms"`
	Wechat WechatCfg `json:"wechat"`
	Mfm    MfmCfg    `json:"mfm"`
}

type SmsCfg struct {
	AccessKeyId     string `json:"accessKeyId"`
	AccessKeySecret string `json:"accessKeySecret"`
	SignName        string `json:"signName"`
	TemplateCode    string `json:"templateCode"`
}
type WechatCfg struct {
	Agentid    string `json:"agentid"`
	Corpid     string `json:"corpid"`
	Corpsecret string `json:"corpsecret"`
}
type MfmCfg struct {
	Address  string `json:"address"`
	Encoding string `json:"encoding"`
}

var cfg Cfg

func init() {
	var m2 = make(map[string]interface{})
	var m = make(map[string]interface{})
	_, err := g.Cfg().Get(context.TODO(), ".", &m2)
	if err != nil {
		fmt.Println(err)
		return
	}
	// m2 := g.Cfg().GetMap(".")
	fmt.Println(m2)
	// m := g.Cfg().GetMap("notice")
	_, err = g.Cfg().Get(context.TODO(), "notice", &m)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(m)
	err = gconv.Scan(m, &cfg)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(cfg)
	// SendMessage("等级1", "类型一", "9528")
	// panic(1032)
}
