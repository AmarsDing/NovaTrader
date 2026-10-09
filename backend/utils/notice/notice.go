/*
 * @Author: hu 2245749018@qq.com
 * @Date: 2022-08-29 10:53:33
 * @LastEditors: hu 2245749018@qq.com
 * @LastEditTime: 2022-09-02 10:17:58
 * @FilePath: \wire4g\library\notice\notice.go
 * @Description:
 *
 * Copyright (c) 2022 by hu 2245749018@qq.com, All Rights Reserved.
 */
package notice

// import (
// 	"context"
// 	"encoding/json"
// 	"errors"
// 	"fmt"
// 	// sysDao "gfast/app/system/dao"
// 	// sysModel "gfast/app/system/model"
// 	// "gfast/app/wire4g/dao"
// 	// "gfast/app/wire4g/model"
// 	"strings"

// 	"github.com/gogf/gf/v2/encoding/gjson"
// 	"github.com/gogf/gf/v2/errors/gerror"
// 	"github.com/gogf/gf/v2/frame/g"
// )

// func SendMessage(level, types, content string) error {
// 	defer func() {
// 		i := recover()
// 		g.Log().Error(i)
// 	}()
// 	list, err := getNotice()
// 	if err != nil {
// 		return err
// 	}
// 	for _, v := range list {
// 		if v.Status != 1 {
// 			continue
// 		}
// 		if v.Wechat == 1 || v.Tel == 1 || v.Sms == 1 || v.MfmTel == 1 || v.MfmSms == 1 || v.MfmBroadcast == 1 || v.MfmEmail == 1 {
// 			//根据获取到的需要告警的信息去获取用户信息
// 			lur, err := sysDao.SysUser.FindById(context.TODO(), v.Uid)
// 			if err != nil {
// 				g.Log().Error(err)
// 				continue
// 			}
// 			message := &Message{Type: types, Level: level, Content: content, Method: &Method{}}

// 			fmt.Println(message)
// 			//根据获取到的信息去组装需要发送的告警信息
// 			message.GetMessage(v, lur)
// 			err = message.Send()
// 			if err != nil {
// 				g.Log().Error(err)
// 				continue
// 			}
// 		}
// 	}
// 	return nil
// }

// //获取表中notic表中的多有数据
// func getNotice() (list []*model.Dev4GNotice, err error) {
// 	// 从数据库中去获取所有可以发送告警信息的对象
// 	var req = &dao.Dev4GNoticSearchReq{}
// 	//获取参数
// 	ctx := context.TODO()
// 	m := dao.Dev4GNotice.Ctx(ctx)

// 	data := ([]*model.Dev4GNotice)(nil)
// 	err = m.Order("id asc").Scan(&data)
// 	if err != nil {
// 		g.Log().Error(err)
// 		err = gerror.New("获取数据失败")
// 	}
// 	list = make([]*model.Dev4GNotice, 0)
// 	if req.ExcludeId != 0 {
// 		for _, v := range data {
// 			if req.ExcludeId != int64(v.Id) && v != nil {
// 				list = append(list, v)
// 			}
// 		}
// 	} else {
// 		list = data
// 	}
// 	return
// }

// //调用发送信息的接口去发送告警信息
// func (msg *Message) Send() error {
// 	sed_msg, _ := json.Marshal(msg)
// 	s1 := strings.Replace(string(sed_msg), "\\\\", "\\", -1)
// 	fmt.Println(s1)
// 	res, err := g.Client().ContentJson().Post("http://127.0.0.1:8000/alarm/sendMessage", s1)
// 	if err != nil {
// 		g.Log().Error(err)
// 		return err
// 	}
// 	defer res.Close()
// 	s := res.ReadAllString()
// 	j := gjson.New(s)
// 	if j.GetString("message") != "发送成功" {
// 		g.Log().Error("告警信息发送失败，原因是:", j.GetString("message"))
// 		return errors.New(j.GetString("message"))
// 	}
// 	return nil
// }

// //根据获取到的信息去组装需要发送的告警信息
// func (msg *Message) GetMessage(v *model.Dev4GNotice, lur *sysModel.GetUserRes) {
// 	if v.Wechat == 1 {
// 		msg.SetWechat(lur)
// 	}
// 	if v.Tel == 1 {
// 		msg.SetTel(lur)
// 	}
// 	if v.Sms == 1 {
// 		msg.SetSms(lur)
// 	}
// 	if v.MfmTel == 1 || v.MfmSms == 1 || v.MfmBroadcast == 1 || v.MfmEmail == 1 {
// 		msg.Method.MFM920 = &MFM920{Msg: make([]*Msg, 0)}
// 		if v.MfmTel == 1 {
// 			msg.SetMfmTel(lur)
// 		}
// 		if v.MfmSms == 1 {
// 			msg.SetMfmSms(lur)
// 		}
// 		if v.MfmBroadcast == 1 {
// 			msg.SetMfmBroadcast(lur)
// 		}
// 		if v.MfmEmail == 1 {
// 			msg.SetMfmEmail(lur)
// 		}
// 	}
// }

// //获取微信处理对象
// func (msg *Message) SetWechat(lur *sysModel.GetUserRes) {
// 	if lur.QywechatUid != "" {
// 		var wechat = &Wechat{
// 			Tousers:    []string{lur.QywechatUid},
// 			Agentid:    cfg.Wechat.Agentid, //企业号中的应用id
// 			Corpid:     cfg.Wechat.Corpid,  //企业号的标识
// 			Corpsecret: cfg.Wechat.Corpsecret,
// 		}
// 		msg.Method.Wechat = wechat
// 	}
// }

// //获取电话处理对象
// func (msg *Message) SetTel(lur *sysModel.GetUserRes) {
// 	// if lur.QywechatUid != "" {
// 	// 	var wechat = &Wechat{
// 	// 		Tousers:    []string{lur.QywechatUid},
// 	// 		Agentid:    cfg.Wechat.Agentid, //企业号中的应用id
// 	// 		Corpid:     cfg.Wechat.Corpid,  //企业号的标识
// 	// 		Corpsecret: cfg.Wechat.Corpsecret,
// 	// 	}
// 	// 	msg.Method.Wechat = wechat
// 	// }
// }

// //获取短信处理对象
// func (msg *Message) SetSms(lur *sysModel.GetUserRes) {
// 	if lur.QywechatUid != "" {
// 		var sms = &Sms{
// 			AccessKeyId:     cfg.Sms.AccessKeyId,     //AccessKey ID
// 			AccessKeySecret: cfg.Sms.AccessKeySecret, //AccessKey Secret
// 			SignName:        cfg.Sms.SignName,        //短信签名名称
// 			TemplateCode:    cfg.Sms.TemplateCode,    //短信模板CODE
// 			PhoneNumbers:    []string{lur.Mobile},    //接收短信的手机号码
// 		}
// 		msg.Method.Sms = sms
// 	}
// }

// //获取mfm模块电话处理对象
// func (msg *Message) SetMfmTel(lur *sysModel.GetUserRes) {
// 	if lur.Mobile != "" {
// 		var mfmMsg = &Msg{
// 			Address: cfg.Mfm.Address,
// 			Type:    "Call",
// 			To:      lur.Mobile,
// 			// Sub:     cfg.Mfm.AccessKeyId, //邮件主题，当通知类型为右键的时候，必须得有邮件主题，才能发送邮件信息
// 		}

// 		msg.Method.MFM920.Msg = append(msg.Method.MFM920.Msg, mfmMsg)
// 	}
// }

// //获取mfm模块短信处理对象
// func (msg *Message) SetMfmSms(lur *sysModel.GetUserRes) {
// 	if lur.Mobile != "" {
// 		var mfmMsg = &Msg{
// 			Address: cfg.Mfm.Address,
// 			Type:    "SMS",
// 			To:      lur.Mobile,
// 			// Sub:     cfg.Mfm.AccessKeyId, //邮件主题，当通知类型为右键的时候，必须得有邮件主题，才能发送邮件信息
// 		}

// 		msg.Method.MFM920.Msg = append(msg.Method.MFM920.Msg, mfmMsg)
// 	}
// }

// //获取mfm模块广播处理对象
// func (msg *Message) SetMfmBroadcast(lur *sysModel.GetUserRes) {
// 	if lur.Mobile != "" {
// 		var mfmMsg = &Msg{
// 			Address: cfg.Mfm.Address,
// 			Type:    "Broadcast",
// 			To:      "1",
// 			// Sub:     cfg.Mfm.AccessKeyId, //邮件主题，当通知类型为右键的时候，必须得有邮件主题，才能发送邮件信息
// 		}

// 		msg.Method.MFM920.Msg = append(msg.Method.MFM920.Msg, mfmMsg)
// 	}
// }

// //获取mfm模块邮箱处理对象
// func (msg *Message) SetMfmEmail(lur *sysModel.GetUserRes) {
// 	if lur.UserEmail != "" {
// 		var mfmMsg = &Msg{
// 			Address: cfg.Mfm.Address,
// 			Type:    "Email",
// 			To:      lur.UserEmail,
// 			Sub:     "告警信息", //邮件主题，当通知类型为右键的时候，必须得有邮件主题，才能发送邮件信息
// 		}

// 		msg.Method.MFM920.Msg = append(msg.Method.MFM920.Msg, mfmMsg)
// 	}
// }
