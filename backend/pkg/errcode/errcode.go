// Package errcode 是各服务共用的错误码。
package errcode

import "fmt"

type Code int

const (
	OK           Code = 0
	Invalid      Code = 40001
	NotTrading   Code = 40002
	RiskRejected Code = 40003
	NotFound     Code = 40004
	Internal     Code = 50000
)

type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%d %s", e.Code, e.Msg)
}

func New(code Code, msg string) error {
	return &Error{Code: code, Msg: msg}
}
