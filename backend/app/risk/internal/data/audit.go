package data

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"server/app/risk/internal/biz"
	"server/conf"
	"server/ent"

	"github.com/go-kratos/kratos/v2/log"
)

const (
	defaultAuditQueue = 4096
	auditBatch        = 200
	auditFlushEvery   = 200 * time.Millisecond
)

// Auditor 把校验记录放进有界队列，后台批量写 risk_check。队列满时 Submit 返回 false，调用方拒单。
type Auditor struct {
	client *ent.Client
	ch     chan biz.AuditRecord
	log    *log.Helper

	once sync.Once
	done chan struct{}
}

func NewAuditor(c *conf.Risk, client *ent.Client, logger log.Logger) *Auditor {
	n := int(c.GetAuditQueue())
	if n <= 0 {
		n = defaultAuditQueue
	}
	return &Auditor{
		client: client,
		ch:     make(chan biz.AuditRecord, n),
		log:    log.NewHelper(log.With(logger, "module", "risk/audit")),
		done:   make(chan struct{}),
	}
}

func (a *Auditor) Submit(rec biz.AuditRecord) bool {
	select {
	case a.ch <- rec:
		return true
	default:
		return false
	}
}

// Run 一直写到 ctx 结束，然后把队列里剩下的写完再返回。
func (a *Auditor) Run(ctx context.Context) {
	defer a.once.Do(func() { close(a.done) })
	t := time.NewTicker(auditFlushEvery)
	defer t.Stop()
	buf := make([]biz.AuditRecord, 0, auditBatch)
	flush := func() {
		if len(buf) == 0 {
			return
		}
		wctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.write(wctx, buf); err != nil {
			a.log.Errorf("write %d audit records: %v", len(buf), err)
		}
		buf = buf[:0]
	}
	for {
		select {
		case rec := <-a.ch:
			buf = append(buf, rec)
			if len(buf) >= auditBatch {
				flush()
			}
		case <-t.C:
			flush()
		case <-ctx.Done():
			for {
				select {
				case rec := <-a.ch:
					buf = append(buf, rec)
					if len(buf) >= auditBatch {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

// Done 在 Run 写完剩余记录后关闭。
func (a *Auditor) Done() <-chan struct{} { return a.done }

func (a *Auditor) write(ctx context.Context, recs []biz.AuditRecord) error {
	bulk := make([]*ent.RiskCheckCreate, 0, len(recs))
	for _, r := range recs {
		results, err := json.Marshal(r.Decision.Results)
		if err != nil {
			return err
		}
		input, err := json.Marshal(r.Input)
		if err != nil {
			return err
		}
		o := r.Input.Order
		id := clip(o.ClientID, 64)
		if id == "" {
			id = "-"
		}
		bulk = append(bulk, a.client.RiskCheck.Create().
			SetClientOrderID(id).
			SetAccountType(clip(string(o.Account), 8)).
			SetSymbol(clip(o.Symbol, 16)).
			SetSide(clip(string(o.Side), 8)).
			SetSource(clip(string(o.Source), 8)).
			SetOperator(clip(o.Operator, 64)).
			SetPrice(o.Price).
			SetVolumeReq(o.Volume).
			SetVolumeFinal(r.Decision.Volume).
			SetApproved(r.Decision.Approved).
			SetRuleID(r.Decision.Rule).
			SetReason(r.Decision.Reason).
			SetResults(results).
			SetInput(input).
			SetLatencyUs(r.Latency.Microseconds()).
			SetCreatedAt(r.Input.Now))
	}
	return a.client.RiskCheck.CreateBulk(bulk...).Exec(ctx)
}

// clip 按字符截断，保证非法请求也能写进审计。
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
