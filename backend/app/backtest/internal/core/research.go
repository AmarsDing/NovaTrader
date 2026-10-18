package core

import (
	"context"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"server/app/backtest/internal/biz"
	"sync"
	"time"
)

var ResearchProviderSet = wire.NewSet(NewResearchRunner)

type ResearchRunner struct {
	uc     *biz.ResearchUsecase
	log    *log.Helper
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewResearchRunner(uc *biz.ResearchUsecase, logger log.Logger) *ResearchRunner {
	return &ResearchRunner{uc: uc, log: log.NewHelper(logger)}
}
func (r *ResearchRunner) Start(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		for ctx.Err() == nil {
			ran, err := r.uc.RunResearch(ctx)
			if err != nil && ctx.Err() == nil {
				r.log.Errorf("research lifecycle: %v", err)
			}
			if (!ran || err != nil) && !sleep(ctx, time.Second) {
				return
			}
		}
	}()
	return nil
}
func (r *ResearchRunner) Stop(context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	return nil
}
