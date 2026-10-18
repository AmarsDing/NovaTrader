package biz

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"server/pkg/backtest"
	"server/pkg/dataset"
)

func (uc *Usecase) FreezeDataset(ctx context.Context, raw json.RawMessage) (*dataset.Manifest, error) {
	cfg := backtest.DefaultConfig()
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("%w: config: %v", ErrBadRequest, err)
		}
	}
	if _, _, err := cfg.Normalize(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	port, ok := uc.src.(DatasetSource)
	if !ok {
		return nil, fmt.Errorf("%w: 数据源不支持冻结", ErrConflict)
	}
	return port.FreezeDataset(ctx, cfg, uc.now())
}

func (uc *Usecase) Dataset(ctx context.Context, id string) (*dataset.Manifest, error) {
	if len(id) != 71 || !strings.HasPrefix(id, "sha256:") {
		return nil, fmt.Errorf("%w: invalid manifest identity", ErrBadRequest)
	}
	port, ok := uc.src.(DatasetSource)
	if !ok {
		return nil, fmt.Errorf("%w: 数据源不支持冻结", ErrConflict)
	}
	return port.GetDataset(ctx, id)
}
