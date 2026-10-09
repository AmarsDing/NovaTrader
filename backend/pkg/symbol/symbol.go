// Package symbol 在内部代码、通达信和东方财富文件单之间转换证券代码。
// 内部形式与通达信官方接口一致：600519.SH、000001.SZ、830001.BJ。
// 东方财富量化终端文件单使用 SHSE.600519、SZSE.000001。北交所前缀在仿真账户核对前固定为 BJSE。
package symbol

import (
	"fmt"
	"strings"
)

// Symbol 是一只证券在某一市场上的代码。
type Symbol struct {
	Market string // SH、SZ、BJ
	Code   string // 6 位数字
}

var eastMoneyPrefix = map[string]string{
	"SH": "SHSE",
	"SZ": "SZSE",
	"BJ": "BJSE",
}

// Parse 接受 600519.SH、sh600519、SHSE.600519 三种写法。
func Parse(raw string) (Symbol, error) {
	s := strings.TrimSpace(strings.ToUpper(raw))
	if s == "" {
		return Symbol{}, fmt.Errorf("symbol: empty")
	}
	if i := strings.IndexByte(s, '.'); i > 0 {
		left, right := s[:i], s[i+1:]
		if market, ok := fromEastMoney(left); ok && validCode(right) {
			return Symbol{Market: market, Code: right}, nil
		}
		if validMarket(right) && validCode(left) {
			return Symbol{Market: right, Code: left}, nil
		}
		return Symbol{}, fmt.Errorf("symbol: invalid %q", raw)
	}
	if len(s) == 8 && validMarket(s[:2]) && validCode(s[2:]) {
		return Symbol{Market: s[:2], Code: s[2:]}, nil
	}
	return Symbol{}, fmt.Errorf("symbol: invalid %q", raw)
}

// FromAShareCode 按 A 股代码段推断市场：6 开头沪市，0、3 开头深市，4、8、92 开头北交所。
// 只用于 A 股；指数、基金、债券的代码段与此重叠，不能用这个函数。
func FromAShareCode(code string) (Symbol, error) {
	code = strings.TrimSpace(code)
	if !validCode(code) {
		return Symbol{}, fmt.Errorf("symbol: invalid code %q", code)
	}
	switch {
	case code[0] == '6':
		return Symbol{Market: "SH", Code: code}, nil
	case code[0] == '0' || code[0] == '3':
		return Symbol{Market: "SZ", Code: code}, nil
	case code[0] == '4' || code[0] == '8' || strings.HasPrefix(code, "92"):
		return Symbol{Market: "BJ", Code: code}, nil
	}
	return Symbol{}, fmt.Errorf("symbol: %q is not an A-share code", code)
}

// Tongdaxin 返回 600519.SH。
func (s Symbol) Tongdaxin() string {
	return s.Code + "." + s.Market
}

// EastMoney 返回 SHSE.600519，供文件单 symbol 字段使用。
func (s Symbol) EastMoney() string {
	return eastMoneyPrefix[s.Market] + "." + s.Code
}

func fromEastMoney(prefix string) (string, bool) {
	for market, p := range eastMoneyPrefix {
		if p == prefix {
			return market, true
		}
	}
	return "", false
}

func validMarket(m string) bool {
	_, ok := eastMoneyPrefix[m]
	return ok
}

func validCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
