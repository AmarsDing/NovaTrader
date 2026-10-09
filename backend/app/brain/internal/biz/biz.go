// Package biz 是 M05 AI 智脑的业务编排：事实包、四维研判、校验、晨报、异动解读、追问。
// 模型只给评分、理由和证据编号；价格和是否下单不在这里决定。
package biz

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"server/pkg/llm"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(NewSettings, NewUsecase)

// ErrBadRequest 包裹调用方参数错误，service 层据此返回 400。
var ErrBadRequest = errors.New("bad request")

func badRequest(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrBadRequest, fmt.Sprintf(format, args...))
}

type Usecase struct {
	cfg       Settings
	llm       LLM
	market    MarketRepo
	decisions DecisionRepo
	briefings BriefingRepo
	progress  Progress
	cal       *tradecal.Calendar
	log       *log.Helper
	now       func() time.Time
}

func NewUsecase(cfg Settings, l LLM, market MarketRepo, decisions DecisionRepo, briefings BriefingRepo, progress Progress, logger log.Logger) *Usecase {
	return &Usecase{
		cfg: cfg, llm: l, market: market, decisions: decisions, briefings: briefings, progress: progress,
		cal: tradecal.Default, log: log.NewHelper(log.With(logger, "module", "brain/biz")), now: time.Now,
	}
}

func (uc *Usecase) Settings() Settings { return uc.cfg }

func (uc *Usecase) Stats(ctx context.Context) []llm.TierStats { return uc.llm.Stats(ctx) }

func (uc *Usecase) emit(ctx context.Context, ev ProgressEvent) {
	if uc.progress != nil {
		uc.progress.Publish(ctx, ev)
	}
}

func newTrace(id string) string {
	if strings.TrimSpace(id) != "" {
		return id
	}
	return uuid.NewString()
}

type callSpec struct {
	task     string
	prompt   Prompt
	prio     llm.Priority
	trace    string
	decision int
	user     string
}

const retryHint = "上一次输出不合格：%s。请修正后只输出 JSON。"

// callJSON 调一次模型并校验；不合格时把原因附在对话末尾再调一次（FR-05-03）。
// 返回的错误包裹 llm.ErrInvalid（两次都不合格）或 llm.ErrUnavailable / 其他错误（模型不可用）。
func callJSON[T any](ctx context.Context, uc *Usecase, spec callSpec, check func(*T) error) (T, llm.Response, error) {
	msgs := []llm.Message{{Role: "system", Content: spec.prompt.System}, {Role: "user", Content: spec.user}}
	var decision *int
	if spec.decision > 0 {
		decision = &spec.decision
	}
	var zero T
	for attempt := 1; ; attempt++ {
		var cur T
		resp, err := uc.llm.Chat(ctx, llm.Request{
			Task: spec.task, Tier: uc.cfg.tier(spec.task), Priority: spec.prio, Messages: msgs,
			TraceID: spec.trace, PromptVersion: spec.prompt.Tag(), PromptHash: spec.prompt.Hash(),
			Attempt: attempt, DecisionID: decision,
			Validate: func(content string) error {
				cur = zero
				if err := decodeJSON(content, &cur); err != nil {
					return err
				}
				return check(&cur)
			},
		})
		if err == nil {
			return cur, resp, nil
		}
		if !errors.Is(err, llm.ErrInvalid) || attempt >= 2 {
			return zero, resp, err
		}
		msgs = append(msgs,
			llm.Message{Role: "assistant", Content: resp.Content},
			llm.Message{Role: "user", Content: fmt.Sprintf(retryHint, invalidReason(err))},
		)
	}
}

func invalidReason(err error) string {
	return strings.TrimPrefix(err.Error(), llm.ErrInvalid.Error()+": ")
}

// persistCtx 让审计在请求超时后仍能写完。
func persistCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
}
