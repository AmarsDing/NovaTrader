// Package kbstore 是 M04 知识库的数据层：ent 仓储和嵌入服务客户端。
// 向量距离和全文匹配只能写成 sql.Selector 表达式（M04 设计第 0 节第 9 条），参数一律走占位符。
// 带参数的排序必须用 sql.ExprFunc：Selector.OrderExprFunc 会先渲染成字符串，丢掉参数。
package kbstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"server/app/intel/internal/biz/kb"
	"server/ent"
	"server/ent/kbcase"
	"server/ent/kbchunk"
	"server/ent/kbdocument"
	"server/ent/predicate"
	"server/pkg/errcode"
	"server/pkg/pgext"

	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	"github.com/jackc/pgx/v5/pgconn"
)

// chunkBatch 控制批量插入的行数，远低于 PostgreSQL 单条语句 65535 个参数的上限。
const chunkBatch = 500

type repo struct {
	client *ent.Client
	now    func() time.Time
}

func NewKnowledgeRepo(client *ent.Client) kb.KnowledgeRepo {
	return &repo{client: client, now: time.Now}
}

var notFound = errcode.New(errcode.NotFound, "文档不存在")

func withTx(ctx context.Context, client *ent.Client, fn func(tx *ent.Tx) error) error {
	tx, err := client.Tx(ctx)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func toDocument(d *ent.KbDocument) *kb.Document {
	return &kb.Document{
		ID:          d.ID,
		Category:    string(d.Category),
		Title:       d.Title,
		Source:      d.Source,
		SourceType:  string(d.SourceType),
		Author:      d.Author,
		Format:      d.Format,
		FileName:    d.FileName,
		SizeBytes:   d.SizeBytes,
		ContentHash: d.ContentHash,
		Status:      string(d.Status),
		Version:     d.Version,
		StockCodes:  d.StockCodes,
		Tags:        d.Tags,
		ExpiresAt:   d.ExpiresAt,
		ChunkCount:  d.ChunkCount,
		EmbedStatus: string(d.EmbedStatus),
		CreatedAt:   d.CreatedAt,
		UpdatedAt:   d.UpdatedAt,
	}
}

func (r *repo) FindDocumentByHash(ctx context.Context, hash string) (*kb.Document, error) {
	d, err := r.client.KbDocument.Query().Where(kbdocument.ContentHashEQ(hash)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toDocument(d), nil
}

func (r *repo) CreateDocument(ctx context.Context, nd kb.NewDocument, chunks []kb.ChunkRow) (*kb.Document, []int, error) {
	var (
		doc *ent.KbDocument
		ids []int
	)
	err := withTx(ctx, r.client, func(tx *ent.Tx) error {
		var err error
		doc, ids, err = r.insertDocument(ctx, tx, nd, chunks)
		return err
	})
	if isUniqueViolation(err) {
		if old, ferr := r.FindDocumentByHash(ctx, nd.ContentHash); ferr == nil && old != nil {
			return old, nil, nil
		}
	}
	if err != nil {
		return nil, nil, err
	}
	return toDocument(doc), ids, nil
}

// insertDocument 写文档和分块，并把同 source 的在用旧版本标成 superseded。调用方负责事务。
func (r *repo) insertDocument(ctx context.Context, tx *ent.Tx, nd kb.NewDocument, chunks []kb.ChunkRow) (*ent.KbDocument, []int, error) {
	now := r.now()
	version := 1
	last, err := tx.KbDocument.Query().
		Where(kbdocument.SourceEQ(nd.Source)).
		Order(kbdocument.ByVersion(sql.OrderDesc())).
		First(ctx)
	switch {
	case err == nil:
		version = last.Version + 1
	case !ent.IsNotFound(err):
		return nil, nil, err
	}
	if _, err := tx.KbDocument.Update().
		Where(kbdocument.SourceEQ(nd.Source), kbdocument.StatusEQ(kbdocument.StatusActive)).
		SetStatus(kbdocument.StatusSuperseded).
		SetSupersededAt(now).
		Save(ctx); err != nil {
		return nil, nil, err
	}
	doc, err := tx.KbDocument.Create().
		SetCategory(kbdocument.Category(nd.Category)).
		SetTitle(nd.Title).
		SetSource(nd.Source).
		SetSourceType(kbdocument.SourceType(nd.SourceType)).
		SetAuthor(nd.Author).
		SetFormat(nd.Format).
		SetFileName(nd.FileName).
		SetSizeBytes(nd.SizeBytes).
		SetContentHash(nd.ContentHash).
		SetVersion(version).
		SetStockCodes(nd.StockCodes).
		SetTags(nd.Tags).
		SetNillableExpiresAt(nd.ExpiresAt).
		SetChunkCount(len(chunks)).
		Save(ctx)
	if err != nil {
		return nil, nil, err
	}
	ids := make([]int, 0, len(chunks))
	for start := 0; start < len(chunks); start += chunkBatch {
		end := min(start+chunkBatch, len(chunks))
		builders := make([]*ent.KbChunkCreate, 0, end-start)
		for _, c := range chunks[start:end] {
			builders = append(builders, tx.KbChunk.Create().
				SetDocID(doc.ID).
				SetSeq(c.Seq).
				SetHeading(c.Heading).
				SetContent(c.Content).
				SetTokenCount(c.Tokens).
				SetTsv(c.TSV))
		}
		created, err := tx.KbChunk.CreateBulk(builders...).Save(ctx)
		if err != nil {
			return nil, nil, err
		}
		for _, c := range created {
			ids = append(ids, c.ID)
		}
	}
	return doc, ids, nil
}

func (r *repo) ReactivateDocument(ctx context.Context, id int) (*kb.Document, error) {
	var doc *ent.KbDocument
	err := withTx(ctx, r.client, func(tx *ent.Tx) error {
		d, err := tx.KbDocument.Get(ctx, id)
		if err != nil {
			return err
		}
		now := r.now()
		if _, err := tx.KbDocument.Update().
			Where(kbdocument.SourceEQ(d.Source), kbdocument.StatusEQ(kbdocument.StatusActive), kbdocument.IDNEQ(id)).
			SetStatus(kbdocument.StatusSuperseded).
			SetSupersededAt(now).
			Save(ctx); err != nil {
			return err
		}
		upd := tx.KbDocument.UpdateOneID(id).
			SetStatus(kbdocument.StatusActive).
			ClearSupersededAt()
		if d.ExpiresAt != nil && !d.ExpiresAt.After(now) {
			upd.ClearExpiresAt()
		}
		doc, err = upd.Save(ctx)
		return err
	})
	if ent.IsNotFound(err) {
		return nil, notFound
	}
	if err != nil {
		return nil, err
	}
	return toDocument(doc), nil
}

func (r *repo) GetDocument(ctx context.Context, id int) (*kb.Document, error) {
	d, err := r.client.KbDocument.Get(ctx, id)
	if ent.IsNotFound(err) {
		return nil, notFound
	}
	if err != nil {
		return nil, err
	}
	return toDocument(d), nil
}

// chunkMeta 是不含向量和 tsv 的分块列，列表和命中都不需要这两列。
var chunkMeta = []string{
	kbchunk.FieldDocID, kbchunk.FieldSeq, kbchunk.FieldHeading, kbchunk.FieldContent,
	kbchunk.FieldTokenCount, kbchunk.FieldEmbedModel, kbchunk.FieldEmbeddedAt,
}

func toStored(c *ent.KbChunk) kb.StoredChunk {
	return kb.StoredChunk{
		ID:       c.ID,
		Seq:      c.Seq,
		Heading:  c.Heading,
		Content:  c.Content,
		Tokens:   c.TokenCount,
		Embedded: c.EmbeddedAt != nil,
	}
}

func (r *repo) DocumentChunks(ctx context.Context, docID int) ([]kb.StoredChunk, error) {
	rows, err := r.client.KbChunk.Query().
		Where(kbchunk.DocIDEQ(docID)).
		Order(kbchunk.BySeq()).
		Select(chunkMeta...).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]kb.StoredChunk, len(rows))
	for i, c := range rows {
		out[i] = toStored(c)
	}
	return out, nil
}

func (r *repo) ListDocuments(ctx context.Context, f kb.ListFilter) ([]kb.Document, int, error) {
	q := r.client.KbDocument.Query()
	if f.Category != "" {
		q.Where(kbdocument.CategoryEQ(kbdocument.Category(f.Category)))
	}
	if f.Status != "" {
		q.Where(kbdocument.StatusEQ(kbdocument.Status(f.Status)))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Order(kbdocument.ByUpdatedAt(sql.OrderDesc()), kbdocument.ByID(sql.OrderDesc())).
		Offset(f.Offset).Limit(f.Limit).All(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]kb.Document, len(rows))
	for i, d := range rows {
		out[i] = *toDocument(d)
	}
	return out, total, nil
}

func (r *repo) DeleteDocument(ctx context.Context, id int) error {
	err := r.client.KbDocument.UpdateOneID(id).
		SetStatus(kbdocument.StatusDeleted).
		SetSupersededAt(r.now()).
		Exec(ctx)
	if ent.IsNotFound(err) {
		return notFound
	}
	return err
}

func (r *repo) SaveEmbeddings(ctx context.Context, model string, vecs map[int][]float32) error {
	if len(vecs) == 0 {
		return nil
	}
	now := r.now()
	return withTx(ctx, r.client, func(tx *ent.Tx) error {
		ids := make([]int, 0, len(vecs))
		for id, v := range vecs {
			if err := tx.KbChunk.UpdateOneID(id).
				SetEmbedding(pgext.Vector(v)).
				SetEmbedModel(model).
				SetEmbeddedAt(now).
				Exec(ctx); err != nil && !ent.IsNotFound(err) {
				return err
			}
			ids = append(ids, id)
		}
		docIDs, err := tx.KbChunk.Query().Where(kbchunk.IDIn(ids...)).
			Unique(true).Select(kbchunk.FieldDocID).Ints(ctx)
		if err != nil {
			return err
		}
		for _, docID := range docIDs {
			left, err := tx.KbChunk.Query().Where(
				kbchunk.DocIDEQ(docID),
				kbchunk.Or(kbchunk.EmbeddingIsNil(), kbchunk.EmbedModelNEQ(model)),
			).Exist(ctx)
			if err != nil {
				return err
			}
			if !left {
				if err := tx.KbDocument.UpdateOneID(docID).
					SetEmbedStatus(kbdocument.EmbedStatusDone).Exec(ctx); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (r *repo) PendingEmbeddings(ctx context.Context, model string, limit int) ([]kb.StoredChunk, error) {
	rows, err := r.client.KbChunk.Query().
		Where(
			kbchunk.Or(kbchunk.EmbeddingIsNil(), kbchunk.EmbedModelNEQ(model)),
			kbchunk.HasDocumentWith(kbdocument.StatusEQ(kbdocument.StatusActive)),
		).
		Order(kbchunk.ByID()).
		Limit(limit).
		Select(chunkMeta...).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]kb.StoredChunk, len(rows))
	for i, c := range rows {
		out[i] = toStored(c)
	}
	return out, nil
}

// docFilter 只留在用、未过期且符合分类、股票、文档范围的分块。
func docFilter(f kb.SearchFilter) []predicate.KbChunk {
	now := f.Now
	if now.IsZero() {
		now = time.Now()
	}
	docPreds := []predicate.KbDocument{
		kbdocument.StatusEQ(kbdocument.StatusActive),
		kbdocument.Or(kbdocument.ExpiresAtIsNil(), kbdocument.ExpiresAtGT(now)),
	}
	if len(f.Categories) > 0 {
		cats := make([]kbdocument.Category, len(f.Categories))
		for i, c := range f.Categories {
			cats[i] = kbdocument.Category(c)
		}
		docPreds = append(docPreds, kbdocument.CategoryIn(cats...))
	}
	if f.StockCode != "" {
		code := f.StockCode
		docPreds = append(docPreds, func(s *sql.Selector) {
			s.Where(sqljson.ValueContains(s.C(kbdocument.FieldStockCodes), code))
		})
	}
	preds := []predicate.KbChunk{kbchunk.HasDocumentWith(docPreds...)}
	if len(f.DocIDs) > 0 {
		preds = append(preds, kbchunk.DocIDIn(f.DocIDs...))
	}
	return preds
}

func (r *repo) KeywordSearch(ctx context.Context, tsquery string, f kb.SearchFilter, limit int) ([]int, error) {
	if tsquery == "" {
		return nil, nil
	}
	match := func(s *sql.Selector) {
		s.Where(sql.P(func(b *sql.Builder) {
			b.Ident(s.C(kbchunk.FieldTsv)).WriteString(" @@ ").Arg(tsquery).WriteString("::tsquery")
		}))
	}
	rank := func(s *sql.Selector) {
		s.OrderExpr(sql.ExprFunc(func(b *sql.Builder) {
			b.WriteString("ts_rank_cd(").Ident(s.C(kbchunk.FieldTsv)).WriteString(", ").
				Arg(tsquery).WriteString("::tsquery) DESC")
		}))
	}
	return r.client.KbChunk.Query().
		Where(append(docFilter(f), match)...).
		Order(rank, kbchunk.ByID()).
		Limit(limit).
		IDs(ctx)
}

func (r *repo) VectorSearch(ctx context.Context, model string, vec []float32, f kb.SearchFilter, limit int) ([]int, error) {
	if len(vec) == 0 {
		return nil, nil
	}
	v, err := pgext.Vector(vec).Value()
	if err != nil {
		return nil, err
	}
	distance := func(s *sql.Selector) {
		s.OrderExpr(sql.ExprFunc(func(b *sql.Builder) {
			b.Ident(s.C(kbchunk.FieldEmbedding)).WriteString(" <=> ").Arg(v).WriteString("::vector")
		}))
	}
	preds := append(docFilter(f), kbchunk.EmbeddingNotNil(), kbchunk.EmbedModelEQ(model))
	return r.client.KbChunk.Query().
		Where(preds...).
		Order(distance, kbchunk.ByID()).
		Limit(limit).
		IDs(ctx)
}

func (r *repo) LoadHits(ctx context.Context, ids []int) (map[int]kb.Hit, error) {
	out := make(map[int]kb.Hit, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.client.KbChunk.Query().
		Where(kbchunk.IDIn(ids...)).
		Select(chunkMeta...).
		WithDocument().
		All(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range rows {
		d := c.Edges.Document
		if d == nil {
			continue
		}
		out[c.ID] = kb.Hit{
			DocID:      d.ID,
			ChunkID:    c.ID,
			Title:      d.Title,
			Heading:    c.Heading,
			Content:    c.Content,
			Source:     d.Source,
			SourceType: string(d.SourceType),
			Category:   string(d.Category),
		}
	}
	return out, nil
}

func (r *repo) CreateCase(ctx context.Context, c kb.CaseRecord, nd kb.NewDocument, chunks []kb.ChunkRow) (int, int, []int, bool, error) {
	if row, err := r.findCase(ctx, c.Book, c.SignalID); err != nil || row != nil {
		if err != nil {
			return 0, 0, nil, false, err
		}
		return row.ID, derefInt(row.DocID), nil, false, nil
	}
	var (
		caseID, docID int
		ids           []int
	)
	err := withTx(ctx, r.client, func(tx *ent.Tx) error {
		doc, chunkIDs, err := r.insertDocument(ctx, tx, nd, chunks)
		if err != nil {
			return err
		}
		row, err := tx.KbCase.Create().
			SetBook(c.Book).
			SetSignalID(c.SignalID).
			SetStockCode(c.StockCode).
			SetStockName(c.StockName).
			SetStrategy(c.Strategy).
			SetPattern(c.Pattern).
			SetEmotionPhase(c.EmotionPhase).
			SetSignalType(c.SignalType).
			SetFinalStatus(kbcase.FinalStatus(c.FinalStatus)).
			SetSignalTime(c.SignalTime).
			SetClosedAt(c.ClosedAt).
			SetNillableEntryPrice(c.EntryPrice).
			SetNillableExitPrice(c.ExitPrice).
			SetNillablePnlPct(c.PnlPct).
			SetNillableHoldingDays(c.HoldingDays).
			SetOutcome(kbcase.Outcome(c.Outcome)).
			SetAttribution(c.Attribution).
			SetDocID(doc.ID).
			Save(ctx)
		if err != nil {
			return err
		}
		caseID, docID, ids = row.ID, doc.ID, chunkIDs
		return nil
	})
	if isUniqueViolation(err) {
		// 并发重复投递：另一条已写入，按已存在处理。
		row, ferr := r.findCase(ctx, c.Book, c.SignalID)
		if ferr == nil && row != nil {
			return row.ID, derefInt(row.DocID), nil, false, nil
		}
	}
	if err != nil {
		return 0, 0, nil, false, fmt.Errorf("kbstore: create case: %w", err)
	}
	return caseID, docID, ids, true, nil
}

func (r *repo) findCase(ctx context.Context, book string, signalID int64) (*ent.KbCase, error) {
	row, err := r.client.KbCase.Query().
		Where(kbcase.BookEQ(book), kbcase.SignalIDEQ(signalID)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	return row, err
}

// isUniqueViolation 按 SQLSTATE 判断。ent 自带的判断匹配英文报错文本，
// 服务端 lc_messages 为中文时认不出唯一约束冲突，并发重复会被当成普通错误重投。
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return ent.IsConstraintError(err) || errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func (r *repo) FindCases(ctx context.Context, f kb.CaseFilter, limit int) ([]kb.Case, error) {
	q := r.client.KbCase.Query().Where(kbcase.ClosedAtLT(f.AsOf))
	if f.Pattern != "" {
		q.Where(kbcase.PatternEQ(f.Pattern))
	}
	if f.EmotionPhase != "" {
		q.Where(kbcase.EmotionPhaseEQ(f.EmotionPhase))
	}
	if f.StockCode != "" {
		q.Where(kbcase.StockCodeEQ(f.StockCode))
	}
	if f.Strategy != "" {
		q.Where(kbcase.StrategyEQ(f.Strategy))
	}
	if f.Book != "" {
		q.Where(kbcase.BookEQ(f.Book))
	}
	rows, err := q.Order(kbcase.ByClosedAt(sql.OrderDesc()), kbcase.ByID(sql.OrderDesc())).
		Limit(limit).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]kb.Case, len(rows))
	for i, c := range rows {
		out[i] = kb.Case{
			ID: c.ID,
			CaseRecord: kb.CaseRecord{
				Book:         c.Book,
				SignalID:     c.SignalID,
				StockCode:    c.StockCode,
				StockName:    c.StockName,
				Strategy:     c.Strategy,
				Pattern:      c.Pattern,
				EmotionPhase: c.EmotionPhase,
				SignalType:   c.SignalType,
				FinalStatus:  string(c.FinalStatus),
				SignalTime:   c.SignalTime,
				ClosedAt:     c.ClosedAt,
				EntryPrice:   c.EntryPrice,
				ExitPrice:    c.ExitPrice,
				PnlPct:       c.PnlPct,
				HoldingDays:  c.HoldingDays,
				Outcome:      string(c.Outcome),
				Attribution:  c.Attribution,
			},
			DocID:     c.DocID,
			CreatedAt: c.CreatedAt,
		}
	}
	return out, nil
}

// Purge 先把过了 expires_at 的在用文档标成 expired（失效时间记在 superseded_at），
// 再删除失效早于 cutoff 的文档。分块由外键级联删除，案例的 doc_id 由外键置空。
func (r *repo) Purge(ctx context.Context, now, cutoff time.Time) (int, error) {
	if _, err := r.client.KbDocument.Update().
		Where(kbdocument.StatusEQ(kbdocument.StatusActive), kbdocument.ExpiresAtLTE(now)).
		SetStatus(kbdocument.StatusExpired).
		SetSupersededAt(now).
		Save(ctx); err != nil {
		return 0, err
	}
	return r.client.KbDocument.Delete().
		Where(
			kbdocument.StatusIn(kbdocument.StatusSuperseded, kbdocument.StatusExpired, kbdocument.StatusDeleted),
			kbdocument.SupersededAtLT(cutoff),
		).
		Exec(ctx)
}
