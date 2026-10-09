package biz

import (
	"context"
	"fmt"
	"strings"
	"time"

	"server/pkg/symbol"
)

// AddBlacklist 把股票加入黑名单。expires 为 nil 表示永久。
func (uc *Usecase) AddBlacklist(ctx context.Context, raw, reason, by string, expires *time.Time) (string, error) {
	sym, err := symbol.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	if strings.TrimSpace(reason) == "" {
		return "", fmt.Errorf("%w: blacklist reason is required", ErrBadRequest)
	}
	code := sym.Tongdaxin()
	return code, uc.blacklist.Add(ctx, code, reason, by, expires)
}

func (uc *Usecase) RemoveBlacklist(ctx context.Context, raw string) (string, error) {
	sym, err := symbol.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	code := sym.Tongdaxin()
	return code, uc.blacklist.Remove(ctx, code)
}
