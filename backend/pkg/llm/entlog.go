package llm

import (
	"context"

	"server/ent"
)

// EntRecorder 把调用记录写进 llm_call_log。
type EntRecorder struct {
	Client *ent.Client
}

func (r EntRecorder) Record(ctx context.Context, rec CallRecord) error {
	return r.Client.LlmCallLog.Create().
		SetTraceID(rec.TraceID).
		SetTask(rec.Task).
		SetTier(string(rec.Tier)).
		SetModel(clip(rec.Model, 128)).
		SetPromptVersion(clip(rec.PromptVersion, 64)).
		SetPromptHash(rec.PromptHash).
		SetInputDigest(rec.InputDigest).
		SetInputExcerpt(rec.InputExcerpt).
		SetOutput(rec.Output).
		SetStatus(rec.Status).
		SetError(clip(rec.Error, 2000)).
		SetLatencyMs(rec.LatencyMS).
		SetQueueMs(rec.QueueMS).
		SetPromptTokens(rec.PromptTokens).
		SetCompletionTokens(rec.CompletionTokens).
		SetAttempt(rec.Attempt).
		SetNillableDecisionID(rec.DecisionID).
		Exec(ctx)
}
