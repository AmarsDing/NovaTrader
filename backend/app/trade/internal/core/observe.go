package core

import (
	"server/pkg/observe"
)

func observedTradeCall(key string, fn func() error) error {
	finish := observe.ForService("trade").Op(key).Begin()
	err := fn()
	observe.FinishError(finish, err)
	return err
}
