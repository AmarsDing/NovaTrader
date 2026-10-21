package data

import (
	"context"

	"server/app/trade/internal/biz"
	"server/ent/eventdisposition"
	"server/ent/tradefill"
)

// M13-T04 实盘回报归并的持久端口（沿用决策 2/9：所有读写走 ent，禁裸 SQL）。

// HasFillExec 按 (book, exec_id) 判重——正常重放走这里；PG 唯一索引只兜并发与
// 修复脚本漏判（见 Commit 内 TradeFill upsert 的 DoNothing 条款）。
func (r *Repo) HasFillExec(ctx context.Context, book, execID string) (bool, error) {
	n, err := r.clientFor(ctx).TradeFill.Query().
		Where(tradefill.BookEQ(book), tradefill.ExecIDEQ(execID)).
		Limit(1).Count(ctx)
	return n > 0, err
}

// RecordDisposition 写 event_disposition 人工处置留痕：同 (event_id, action)
// 去重（created=false 表示此前已留痕）——账本事务内串行，归并轮询重启/重放
// 不会产生第二份留痕与第二条告警。留痕只标注、不回改已投事件（N2-02 纪律）。
func (r *Repo) RecordDisposition(ctx context.Context, d biz.Disposition) (bool, error) {
	existing, err := r.clientFor(ctx).EventDisposition.Query().
		Where(eventdisposition.EventIDEQ(d.EventID), eventdisposition.ActionEQ(d.Action)).
		Limit(1).Count(ctx)
	if err != nil || existing > 0 {
		return false, err
	}
	_, err = r.clientFor(ctx).EventDisposition.Create().
		SetEventID(d.EventID).SetTraceID(d.TraceID).SetAction(d.Action).
		SetNote(d.Note).SetActor(d.Actor).
		Save(ctx)
	return err == nil, err
}
