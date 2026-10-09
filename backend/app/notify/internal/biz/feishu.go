package biz

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// FeishuBody 是出站文本消息。没有按钮，也没有回调地址。
func FeishuBody(text, secret string, now time.Time) ([]byte, error) {
	payload := map[string]any{
		"msg_type": "text",
		"content":  map[string]string{"text": text},
	}
	if secret != "" {
		ts := now.Unix()
		sign, err := feishuSign(secret, ts)
		if err != nil {
			return nil, err
		}
		payload["timestamp"] = strconv.FormatInt(ts, 10)
		payload["sign"] = sign
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("notify: feishu body: %w", err)
	}
	return b, nil
}

// feishuSign 按飞书自定义机器人的算法：用「秒\nsecret」做密钥，对空内容取 HMAC-SHA256。
func feishuSign(secret string, ts int64) (string, error) {
	mac := hmac.New(sha256.New, []byte(fmt.Sprintf("%d\n%s", ts, secret)))
	if _, err := mac.Write(nil); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}
