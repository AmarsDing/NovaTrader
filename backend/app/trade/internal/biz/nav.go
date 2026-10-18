package biz

import (
	"context"
	"fmt"
	"server/pkg/accountnav"
	"server/pkg/tradecal"
	"time"
)

type NAVReader interface {
	ReadNAVEvidence(context.Context, accountnav.Query) (accountnav.Evidence, error)
}

func (e *Engine) ExportNAV(ctx context.Context, req accountnav.Request) (accountnav.Result, error) {
	query, err := accountnav.Normalize(req, time.Now(), tradecal.Default)
	if err != nil {
		return accountnav.Result{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	port, ok := e.store.(NAVReader)
	if !ok {
		return accountnav.Result{}, fmt.Errorf("NAV evidence repository unavailable")
	}
	evidence, err := port.ReadNAVEvidence(ctx, query)
	if err != nil {
		return accountnav.Result{}, err
	}
	return accountnav.Build(query, evidence), nil
}
