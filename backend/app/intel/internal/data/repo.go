package data

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"server/app/intel/internal/biz"
	"server/ent"
	"server/ent/datasetrecord"
	"server/ent/intelcluster"
	"server/ent/intelfact"
	"server/ent/intellink"
	"server/ent/newssentiment"
	"server/ent/stockalias"
	"server/pkg/dataset"
	"server/pkg/outbox"

	"entgo.io/ent/dialect/sql"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/jackc/pgx/v5/pgconn"
)

type repo struct {
	client *ent.Client
	log    *log.Helper
	shadow *newsShadow
}

func NewRepo(client *ent.Client, logger log.Logger) biz.Repo {
	return &repo{client: client, log: log.NewHelper(log.With(logger, "module", "intel/data")), shadow: newNewsShadow(client)}
}

// clip 按字节截断到 n，且不切断多字节字符。ent 的 MaxLen 按字节校验。
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// isUniqueViolation 按 SQLSTATE 判断。ent 自带的判断匹配英文报错文本，服务端 lc_messages 为中文时认不出来。
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return ent.IsConstraintError(err) || errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func rollback(tx *ent.Tx, err error) error {
	if rerr := tx.Rollback(); rerr != nil {
		return fmt.Errorf("%w (rollback: %v)", err, rerr)
	}
	return err
}

func (r *repo) Find(ctx context.Context, source, sourceID, hash string) (*biz.Item, string, error) {
	q := r.client.NewsSentiment.Query()
	switch {
	case sourceID != "":
		q.Where(newssentiment.Or(
			newssentiment.And(newssentiment.SourceEQ(source), newssentiment.SourceIDEQ(sourceID)),
			newssentiment.ContentHashEQ(hash),
		))
	case hash != "":
		q.Where(newssentiment.ContentHashEQ(hash))
	default:
		return nil, "", nil
	}
	row, err := q.First(ctx)
	if ent.IsNotFound(err) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	it := toItem(row)
	if it.ClusterID != 0 {
		heads, err := r.headsOf(ctx, []int{it.ClusterID})
		if err != nil {
			return nil, "", err
		}
		it.Head = heads[it.ClusterID] == it.ID
	}
	return it, row.Status, nil
}

func (r *repo) CreateItem(ctx context.Context, it *biz.Item, status, reason string) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	clusterID, head := 0, false
	if status == biz.StatusPending {
		if it.ClusterID != 0 {
			n, err := tx.IntelCluster.Update().
				Where(intelcluster.ID(it.ClusterID)).
				AddItemCount(1).
				SetLastSeen(it.ReceivedAt).
				Save(ctx)
			if err != nil {
				return rollback(tx, err)
			}
			if n == 1 {
				clusterID = it.ClusterID
			}
		}
		if clusterID == 0 {
			cl, err := tx.IntelCluster.Create().
				SetTitle(it.Title).
				SetKind(it.Kind).
				SetFirstSeen(it.ReceivedAt).
				SetLastSeen(it.ReceivedAt).
				Save(ctx)
			if err != nil {
				return rollback(tx, err)
			}
			clusterID, head = cl.ID, true
		}
	}
	create := tx.NewsSentiment.Create().
		SetNewsTitle(it.Title).
		SetContent(it.Content).
		SetSource(clip(it.Source, 100)).
		SetSourceID(clip(it.SourceID, 128)).
		SetKind(it.Kind).
		SetURL(it.URL).
		SetPublishTime(it.PublishTime).
		SetPublishTimeGuessed(it.TimeGuessed).
		SetReceivedAt(it.ReceivedAt).
		SetContentHash(it.Hash).
		SetSourceCodes(it.Codes).
		SetSimhash(int64(it.SimHash)).
		SetStatus(status).
		SetReason(reason)
	if clusterID != 0 {
		create.SetClusterID(clusterID)
	}
	row, err := create.Save(ctx)
	if isUniqueViolation(err) {
		_ = tx.Rollback()
		return biz.ErrDuplicate
	}
	if err != nil {
		return rollback(tx, err)
	}
	if head {
		if err := tx.IntelCluster.UpdateOneID(clusterID).SetHeadNewsID(row.ID).Exec(ctx); err != nil {
			return rollback(tx, err)
		}
	}
	// 研报盈利预测随 news_sentiment 同一事务落 dataset_record；filtered 分支不写。
	if status == biz.StatusPending && it.Forecast != nil {
		if err := captureReportForecast(ctx, tx, it); err != nil {
			return rollback(tx, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	it.ID, it.ClusterID, it.Head = row.ID, clusterID, head
	return nil
}

func (r *repo) SaveScored(ctx context.Context, s *biz.Scored) error {
	it, sc := s.Item, s.Score
	now := time.Now()
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	upd := tx.NewsSentiment.Update().
		Where(newssentiment.ID(it.ID), newssentiment.StatusEQ(biz.StatusPending)).
		SetEventType(sc.EventType).
		SetSentimentScore(sc.Sentiment).
		SetImportance(sc.Importance).
		SetHalfLifeMinutes(sc.HalfLifeMinutes).
		SetScorer(sc.Scorer).
		SetDegraded(sc.Degraded).
		SetStatus(biz.StatusScored).
		SetReason(sc.Reason).
		SetScoredAt(now)
	if s.Stock != "" {
		upd.SetStockCode(s.Stock)
	} else {
		upd.ClearStockCode()
	}
	n, err := upd.Save(ctx)
	if err != nil {
		return rollback(tx, err)
	}
	if n == 0 {
		ok, err := tx.NewsSentiment.Query().Where(newssentiment.ID(it.ID)).Exist(ctx)
		if rerr := tx.Rollback(); rerr != nil && err == nil {
			err = rerr
		}
		if err != nil {
			return err
		}
		if !ok {
			return biz.ErrNotFound
		}
		return nil
	}
	if _, err := tx.IntelLink.Delete().Where(intellink.NewsID(it.ID)).Exec(ctx); err != nil {
		return rollback(tx, err)
	}
	if len(s.Links) > 0 {
		builders := make([]*ent.IntelLinkCreate, 0, len(s.Links))
		for _, l := range s.Links {
			builders = append(builders, tx.IntelLink.Create().
				SetNewsID(it.ID).
				SetClusterID(it.ClusterID).
				SetTargetType(l.TargetType).
				SetTarget(clip(l.Target, 64)).
				SetConfidence(l.Confidence).
				SetMethod(l.Method).
				SetMatched(clip(l.Matched, 64)).
				SetCreatedAt(now))
		}
		if err := tx.IntelLink.CreateBulk(builders...).Exec(ctx); err != nil {
			return rollback(tx, err)
		}
	}
	if _, err := tx.IntelFact.Delete().Where(intelfact.NewsID(it.ID)).Exec(ctx); err != nil {
		return rollback(tx, err)
	}
	if len(s.Facts) > 0 {
		builders := make([]*ent.IntelFactCreate, 0, len(s.Facts))
		for _, f := range s.Facts {
			b := tx.IntelFact.Create().
				SetNewsID(it.ID).
				SetFactType(f.Type).
				SetUnit(f.Unit).
				SetText(clip(f.Text, 128)).
				SetStart(f.Start).
				SetEnd(f.End)
			if f.Value != nil {
				b.SetValue(*f.Value)
			}
			builders = append(builders, b)
		}
		if err := tx.IntelFact.CreateBulk(builders...).Exec(ctx); err != nil {
			return rollback(tx, err)
		}
	}
	if it.Head {
		if err := tx.IntelCluster.UpdateOneID(it.ClusterID).
			SetEventType(sc.EventType).
			SetSentimentScore(sc.Sentiment).
			SetImportance(sc.Importance).
			Exec(ctx); err != nil {
			return rollback(tx, err)
		}
	}
	if s.Event != nil {
		if err := outbox.Insert(ctx, tx, *s.Event); err != nil {
			return rollback(tx, err)
		}
	}
	if s.Alert != nil {
		n, err := tx.IntelCluster.Update().
			Where(intelcluster.ID(it.ClusterID), intelcluster.AlertedAtIsNil()).
			SetAlertedAt(now).
			Save(ctx)
		if err != nil {
			return rollback(tx, err)
		}
		if n == 1 {
			if err := outbox.Insert(ctx, tx, *s.Alert); err != nil {
				return rollback(tx, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// The score and alert are already committed. Shadow output cannot change either.
	if r.shadow != nil && it.Head {
		r.shadow.observe(ctx, s)
	}
	return nil
}

func (r *repo) Fingerprints(ctx context.Context, since time.Time) ([]biz.Fingerprint, error) {
	rows, err := r.client.NewsSentiment.Query().
		Where(newssentiment.ReceivedAtGTE(since), newssentiment.ClusterIDNotNil()).
		Select(newssentiment.FieldSimhash, newssentiment.FieldClusterID, newssentiment.FieldNewsTitle,
			newssentiment.FieldContent, newssentiment.FieldSourceCodes, newssentiment.FieldReceivedAt).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.Fingerprint, 0, len(rows))
	for _, row := range rows {
		_, n := biz.SimHash(deref(row.NewsTitle) + deref(row.Content))
		out = append(out, biz.Fingerprint{
			SimHash: uint64(row.Simhash), ClusterID: *row.ClusterID, RuneLen: n,
			Codes: row.SourceCodes, At: row.ReceivedAt,
		})
	}
	return out, nil
}

func (r *repo) Pending(ctx context.Context, limit int) ([]*biz.Item, error) {
	rows, err := r.client.NewsSentiment.Query().
		Where(newssentiment.StatusEQ(biz.StatusPending)).
		Order(newssentiment.ByID()).
		Limit(limit).
		All(ctx)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	clusterIDs := make([]int, 0, len(rows))
	for _, row := range rows {
		if row.ClusterID != nil {
			clusterIDs = append(clusterIDs, *row.ClusterID)
		}
	}
	heads, err := r.headsOf(ctx, clusterIDs)
	if err != nil {
		return nil, err
	}
	out := make([]*biz.Item, 0, len(rows))
	for _, row := range rows {
		it := toItem(row)
		it.Head = heads[it.ClusterID] == row.ID
		out = append(out, it)
	}
	return out, nil
}

func toItem(row *ent.NewsSentiment) *biz.Item {
	it := &biz.Item{
		ID:          row.ID,
		Source:      deref(row.Source),
		SourceID:    row.SourceID,
		Kind:        row.Kind,
		Title:       deref(row.NewsTitle),
		Content:     deref(row.Content),
		URL:         row.URL,
		Codes:       row.SourceCodes,
		TimeGuessed: row.PublishTimeGuessed,
		ReceivedAt:  row.ReceivedAt,
		Hash:        deref(row.ContentHash),
		SimHash:     uint64(row.Simhash),
	}
	if row.ClusterID != nil {
		it.ClusterID = *row.ClusterID
	}
	it.PublishTime = publishTime(row)
	return it
}

func publishTime(row *ent.NewsSentiment) time.Time {
	if row.PublishTime != nil {
		return *row.PublishTime
	}
	return row.ReceivedAt
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// headsOf 返回簇编号到簇首条编号的映射。
func (r *repo) headsOf(ctx context.Context, clusterIDs []int) (map[int]int, error) {
	out := map[int]int{}
	if len(clusterIDs) == 0 {
		return out, nil
	}
	rows, err := r.client.IntelCluster.Query().Where(intelcluster.IDIn(clusterIDs...)).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range rows {
		if c.HeadNewsID != nil {
			out[c.ID] = *c.HeadNewsID
		}
	}
	return out, nil
}

// stockHeads 找关联到 code 的簇首条，返回首条编号到关联置信度的映射。
func (r *repo) stockHeads(ctx context.Context, code string, since time.Time) (map[int]float64, error) {
	links, err := r.client.IntelLink.Query().
		Where(intellink.TargetTypeEQ(biz.TargetStock), intellink.TargetEQ(code), intellink.CreatedAtGTE(since)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	conf := map[int]float64{}
	ids := make([]int, 0, len(links))
	for _, l := range links {
		if _, ok := conf[l.ClusterID]; !ok {
			ids = append(ids, l.ClusterID)
		}
		conf[l.ClusterID] = max(conf[l.ClusterID], l.Confidence)
	}
	heads, err := r.headsOf(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[int]float64, len(heads))
	for clusterID, newsID := range heads {
		out[newsID] = conf[clusterID]
	}
	return out, nil
}

func keys(m map[int]float64) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func (r *repo) SentimentInputs(ctx context.Context, code string, since time.Time) ([]biz.SentimentInput, error) {
	heads, err := r.stockHeads(ctx, code, since)
	if err != nil || len(heads) == 0 {
		return nil, err
	}
	rows, err := r.client.NewsSentiment.Query().
		Where(newssentiment.IDIn(keys(heads)...), newssentiment.StatusEQ(biz.StatusScored)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.SentimentInput, 0, len(rows))
	for _, row := range rows {
		pt := publishTime(row)
		if pt.Before(since) {
			continue
		}
		out = append(out, biz.SentimentInput{
			Sentiment: deref(row.SentimentScore), Importance: row.Importance,
			HalfLifeMinutes: row.HalfLifeMinutes, PublishTime: pt, Confidence: heads[row.ID],
		})
	}
	return out, nil
}

func (r *repo) Dictionary(ctx context.Context) ([]biz.StockEntry, []biz.AliasEntry, error) {
	stocks, err := r.client.StockBasic.Query().All(ctx)
	if err != nil {
		return nil, nil, err
	}
	aliases, err := r.client.StockAlias.Query().Where(stockalias.Enabled(true)).All(ctx)
	if err != nil {
		return nil, nil, err
	}
	se := make([]biz.StockEntry, 0, len(stocks))
	for _, s := range stocks {
		se = append(se, biz.StockEntry{Code: s.StockCode, Name: s.StockName, Concepts: splitConcepts(deref(s.Concept))})
	}
	ae := make([]biz.AliasEntry, 0, len(aliases))
	for _, a := range aliases {
		ae = append(ae, biz.AliasEntry{Alias: a.Alias, Code: a.StockCode, Confidence: a.Confidence})
	}
	return se, ae, nil
}

func splitConcepts(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '，' || r == '、' || r == ';' || r == '；'
	})
}

func (r *repo) UpsertAlias(ctx context.Context, a biz.AliasInput) (int, error) {
	alias := clip(a.Alias, 64)
	row, err := r.client.StockAlias.Query().
		Where(stockalias.AliasEQ(alias), stockalias.StockCodeEQ(a.StockCode)).
		Only(ctx)
	if ent.IsNotFound(err) {
		created, err := r.client.StockAlias.Create().
			SetAlias(alias).SetStockCode(a.StockCode).SetKind(clip(a.Kind, 16)).
			SetConfidence(a.Confidence).SetEnabled(a.Enabled).
			Save(ctx)
		if err != nil {
			return 0, err
		}
		return created.ID, nil
	}
	if err != nil {
		return 0, err
	}
	if err := r.client.StockAlias.UpdateOneID(row.ID).
		SetKind(clip(a.Kind, 16)).SetConfidence(a.Confidence).SetEnabled(a.Enabled).
		Exec(ctx); err != nil {
		return 0, err
	}
	return row.ID, nil
}

func (r *repo) linksOf(ctx context.Context, newsIDs []int) (map[int][]biz.Link, error) {
	rows, err := r.client.IntelLink.Query().
		Where(intellink.NewsIDIn(newsIDs...)).
		Order(intellink.ByConfidence(sql.OrderDesc())).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := map[int][]biz.Link{}
	for _, l := range rows {
		out[l.NewsID] = append(out[l.NewsID], biz.Link{
			TargetType: l.TargetType, Target: l.Target, Confidence: l.Confidence, Method: l.Method, Matched: l.Matched,
		})
	}
	return out, nil
}

func (r *repo) clusterSizes(ctx context.Context, ids []int) (map[int]int, error) {
	out := map[int]int{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.client.IntelCluster.Query().Where(intelcluster.IDIn(ids...)).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range rows {
		out[c.ID] = c.ItemCount
	}
	return out, nil
}

func toView(row *ent.NewsSentiment, withContent bool) *biz.ItemView {
	v := &biz.ItemView{
		ID: row.ID, Kind: row.Kind, Source: deref(row.Source), SourceID: row.SourceID, URL: row.URL,
		Title: deref(row.NewsTitle), PublishTime: publishTime(row), EventType: row.EventType,
		Sentiment: deref(row.SentimentScore), Importance: row.Importance, HalfLifeMinutes: row.HalfLifeMinutes,
		Scorer: row.Scorer, Degraded: row.Degraded, Status: row.Status, Reason: row.Reason,
	}
	if row.ClusterID != nil {
		v.ClusterID = *row.ClusterID
	}
	if withContent {
		v.Content = deref(row.Content)
	}
	return v
}

func (r *repo) GetItem(ctx context.Context, id int) (*biz.ItemView, error) {
	row, err := r.client.NewsSentiment.Get(ctx, id)
	if ent.IsNotFound(err) {
		return nil, biz.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	v := toView(row, true)
	links, err := r.linksOf(ctx, []int{id})
	if err != nil {
		return nil, err
	}
	v.Links = links[id]
	facts, err := r.client.IntelFact.Query().Where(intelfact.NewsID(id)).Order(intelfact.ByStart()).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, f := range facts {
		v.Facts = append(v.Facts, biz.Fact{Type: f.FactType, Value: f.Value, Unit: f.Unit, Text: f.Text, Start: f.Start, End: f.End})
	}
	if v.ClusterID != 0 {
		sizes, err := r.clusterSizes(ctx, []int{v.ClusterID})
		if err != nil {
			return nil, err
		}
		v.ClusterSize = sizes[v.ClusterID]
	}
	return v, nil
}

func (r *repo) Timeline(ctx context.Context, code string, since, until time.Time, limit int) ([]*biz.ItemView, error) {
	heads, err := r.stockHeads(ctx, code, since)
	if err != nil || len(heads) == 0 {
		return nil, err
	}
	rows, err := r.client.NewsSentiment.Query().
		Where(
			newssentiment.IDIn(keys(heads)...),
			newssentiment.StatusEQ(biz.StatusScored),
			newssentiment.PublishTimeGTE(since),
			newssentiment.PublishTimeLTE(until),
		).
		Order(newssentiment.ByPublishTime(sql.OrderDesc()), newssentiment.ByID(sql.OrderDesc())).
		Limit(limit).
		All(ctx)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	ids := make([]int, 0, len(rows))
	clusterIDs := make([]int, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
		if row.ClusterID != nil {
			clusterIDs = append(clusterIDs, *row.ClusterID)
		}
	}
	links, err := r.linksOf(ctx, ids)
	if err != nil {
		return nil, err
	}
	sizes, err := r.clusterSizes(ctx, clusterIDs)
	if err != nil {
		return nil, err
	}
	out := make([]*biz.ItemView, 0, len(rows))
	for _, row := range rows {
		v := toView(row, false)
		v.Links = links[row.ID]
		v.ClusterSize = sizes[v.ClusterID]
		out = append(out, v)
	}
	return out, nil
}

func (r *repo) HotCounts(ctx context.Context, from, to time.Time) (map[[2]string]int, error) {
	links, err := r.client.IntelLink.Query().
		Where(intellink.CreatedAtGTE(from), intellink.CreatedAtLT(to)).
		Select(intellink.FieldTargetType, intellink.FieldTarget, intellink.FieldClusterID).
		All(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[[2]string]map[int]bool{}
	for _, l := range links {
		k := [2]string{l.TargetType, l.Target}
		if seen[k] == nil {
			seen[k] = map[int]bool{}
		}
		seen[k][l.ClusterID] = true
	}
	out := make(map[[2]string]int, len(seen))
	for k, set := range seen {
		out[k] = len(set)
	}
	clusters, err := r.client.IntelCluster.Query().
		Where(intelcluster.FirstSeenGTE(from), intelcluster.FirstSeenLT(to), intelcluster.EventTypeNEQ(biz.EventOther)).
		Select(intelcluster.FieldEventType).
		All(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range clusters {
		out[[2]string{"event", c.EventType}]++
	}
	return out, nil
}

// domainReportForecast 是研报盈利预测在 dataset_record 的域。intel 写 M01 表属跨模块例外
// （W6 设计定论 A，见 M03 设计文档「研报预测字段落点」），以独立域值收敛，
// 二期可平移到 datahub 直采通道（payload 结构不变，消费端无感）。
const domainReportForecast = "report_forecast"

// reportForecastPayload 是 report_forecast 域的载荷：预测本体 + 检索键。
// info_code 即东财研报编号，datahub 发布时填进 source_id。
type reportForecastPayload struct {
	biz.ReportForecast
	InfoCode string   `json:"info_code,omitempty"`
	Title    string   `json:"title,omitempty"`
	Codes    []string `json:"codes,omitempty"`
}

// captureReportForecast 把研报盈利预测写进 dataset_record。
// business_key 取稳定研报身份（info_code=source_id，缺失降级 url，再缺即不写——缺失显式化）；
// EPS 双 nil 年度点整条丢弃（与解析层同则，没取到≠0）；全 nil 披露（无 EPS/目标价/评级任一）
// 只进原文，不写 dataset_record，不冒有。
func captureReportForecast(ctx context.Context, tx *ent.Tx, it *biz.Item) error {
	fc := scrubForecast(it.Forecast)
	if fc == nil {
		return nil
	}
	key := clip(it.SourceID, 128)
	if key == "" {
		key = clip(it.URL, 128)
	}
	if key == "" {
		return nil
	}
	payload := reportForecastPayload{ReportForecast: *fc, InfoCode: clip(it.SourceID, 128), Title: it.Title, Codes: it.Codes}
	observed := it.ReceivedAt
	return insertDatasetRecord(ctx, tx, domainReportForecast, key, clip(it.Source, 64), it.PublishTime,
		dataset.Availability{ObservedAt: &observed, Evidence: "reportapi.eastmoney.com/report/list publishDate 日级粒度（无时刻证据，available_at=unknown）"}, payload)
}

// scrubForecast 丢弃双 nil 的年度点；全 nil 披露返回 nil。
func scrubForecast(fc *biz.ReportForecast) *biz.ReportForecast {
	if fc == nil {
		return nil
	}
	out := *fc
	out.EPS = nil
	for _, p := range fc.EPS {
		if p.EPS == nil && p.PE == nil {
			continue
		}
		out.EPS = append(out.EPS, p)
	}
	if len(out.EPS) == 0 && out.AimPriceLow == nil && out.AimPriceHigh == nil && out.Rating == "" && out.LastRating == "" {
		return nil
	}
	return &out
}

// insertDatasetRecord 复刻 datahub data/dataset.go captureDataset 的写入模式
// （该函数 unexported 跨包不可调，此为等价的内联实现）：只有插入，没有改；
// 重复采集同 revision 靠 revision_hash + DoNothing 幂等吞掉。
func insertDatasetRecord(ctx context.Context, tx *ent.Tx, domain, key, source string, event time.Time, a dataset.Availability, payload any) error {
	if source == "" {
		source = "unknown"
	}
	observed := time.Now()
	if a.ObservedAt != nil {
		observed = *a.ObservedAt
	}
	r, err := dataset.NewRecord(domain, key, source, event, observed, a, payload)
	if err != nil {
		return err
	}
	err = tx.DatasetRecord.Create().SetDomain(r.Domain).SetBusinessKey(r.BusinessKey).SetSource(r.Source).
		SetEventTime(r.EventTime).SetNillableAvailableAt(r.AvailableAt).SetAvailability(r.Availability).
		SetAvailabilityEvidence(r.Evidence).SetObservedAt(r.ObservedAt).SetRawHash(r.RawHash).
		SetRevisionHash(r.RevisionHash).SetPayload(r.Payload).
		OnConflictColumns(datasetrecord.FieldRevisionHash).DoNothing().Exec(ctx)
	// PostgreSQL DO NOTHING returns no ID; ent's single-row create reports sql.ErrNoRows.
	if errors.Is(err, stdsql.ErrNoRows) {
		return nil
	}
	return err
}
