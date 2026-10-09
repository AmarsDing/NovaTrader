package gateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const tokenTTL = 7 * 24 * time.Hour

// User 是配置里的本地账号。密码只存 bcrypt。
type User struct {
	Name         string `json:"name"`
	DisplayName  string `json:"display_name"`
	Role         string `json:"role"`
	PasswordHash string `json:"password_hash"`
}

// Principal 是验过令牌之后的操作者。
type Principal struct {
	Name    string
	Display string
	Role    string
}

type claims struct {
	User    string `json:"u"`
	Role    string `json:"r"`
	Display string `json:"n"`
	Exp     int64  `json:"exp"`
}

// Issue 签发桌面端令牌。
func Issue(secret string, user User, now time.Time) (string, error) {
	if strings.TrimSpace(secret) == "" {
		return "", errors.New("token secret is empty")
	}
	body, err := json.Marshal(claims{
		User:    user.Name,
		Role:    user.Role,
		Display: displayOf(user),
		Exp:     now.Add(tokenTTL).Unix(),
	})
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(body)
	return payload + "." + sign(secret, payload), nil
}

// Parse 校验令牌。过期或签名不对都返回错误。
func Parse(secret, token string, now time.Time) (Principal, error) {
	payload, mac, ok := strings.Cut(token, ".")
	if !ok || payload == "" || mac == "" || secret == "" {
		return Principal{}, errors.New("bad token")
	}
	if !hmac.Equal([]byte(mac), []byte(sign(secret, payload))) {
		return Principal{}, errors.New("bad token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Principal{}, errors.New("bad token")
	}
	var c claims
	if err := json.Unmarshal(raw, &c); err != nil || c.User == "" || now.Unix() >= c.Exp {
		return Principal{}, errors.New("bad token")
	}
	return Principal{Name: c.User, Display: c.Display, Role: c.Role}, nil
}

// AllowSocket 决定 WebSocket 升级是否放行。只有验过的令牌可以连。
func AllowSocket(secret, token string, now time.Time) bool {
	_, err := Parse(secret, token, now)
	return err == nil
}

// CheckPassword 用 bcrypt 比对。账号不存在时也走一次比对，避免用耗时区分账号。
func CheckPassword(users []User, name, password string) (User, bool) {
	var dummy = []byte("$2a$10$7EqJtq98hPqEX7fNZaFWo.OLxY5C5nC5nC5nC5nC5nC5nC5nC5nC")
	for _, user := range users {
		if user.Name != name {
			continue
		}
		if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
			return User{}, false
		}
		return user, true
	}
	_ = bcrypt.CompareHashAndPassword(dummy, []byte(password))
	return User{}, false
}

func displayOf(user User) string {
	if user.DisplayName != "" {
		return user.DisplayName
	}
	return user.Name
}

func sign(secret, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func isOwner(role string) bool {
	return strings.EqualFold(role, "owner")
}
