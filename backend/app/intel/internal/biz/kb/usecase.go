package kb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"server/app/intel/internal/biz/kb/extract"
	"server/conf"
	"server/pkg/errcode"
	"server/pkg/pgext"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
)

const (
	defaultTopK       = 8
	maxTopK           = 50
	pathLimit         = 50
	maxQueryTerms     = 64
	defaultCaseTopK   = 5
	caseCandidates    = 2000
	defaultUploadMB   = 20
	defaultEmbedBatch = 32
	pendingBatch      = 64
	pendingRounds     = 20
	// PurgeAfter 是失效文档保留多久后才删除。
	PurgeAfter = 30 * 24 * time.Hour
)

// KnowledgeUsecase 是知识库的入库、检索和案例用例。
type KnowledgeUsecase struct {
	repo       KnowledgeRepo
	embed      Embedder
	maxUpload  int64
	embedBatch int
	chunkOpt   ChunkOptions
	log        *log.Helper
	now        func() time.Time
}

func NewKnowledgeUsecase(repo KnowledgeRepo, embed Embedder, c *conf.Kb, logger log.Logger) *KnowledgeUsecase {
	mb := int64(c.GetMaxUploadMb())
	if mb <= 0 {
		mb = defaultUploadMB
	}
	batch := int(c.GetEmbedBatch())
	if batch <= 0 {
		batch = defaultEmbedBatch
	}
	return &KnowledgeUsecase{
		repo:       repo,
		embed:      embed,
		maxUpload:  mb << 20,
		embedBatch: batch,
		chunkOpt:   DefaultChunkOptions,
		log:        log.NewHelper(log.With(logger, "module", "kb")),
		now:        time.Now,
	}
}

func invalid(format string, args ...any) error {
	return errcode.New(errcode.Invalid, fmt.Sprintf(format, args...))
}

// IngestInput 是一次入库请求。Content 是原始文件字节。
type IngestInput struct {
	Category   string
	Title      string
	Source     string
	SourceType string
	Author     string
	FileName   string
	Content    []byte
	StockCodes []string
	Tags       []string
	ExpiresAt  *time.Time
}

type IngestResult struct {
	Document  *Document
	Duplicate bool
	Embedded  bool
}

// Ingest 校验来源后抽文字、分块、入库，再尝试写向量。嵌入失败不影响入库。
func (uc *KnowledgeUsecase) Ingest(ctx context.Context, in IngestInput) (*IngestResult, error) {
	in.Source = strings.TrimSpace(in.Source)
	in.Author = strings.TrimSpace(in.Author)
	in.FileName = filepath.Base(strings.TrimSpace(in.FileName))
	if in.FileName == "." {
		in.FileName = ""
	}
	if !contains(Categories, in.Category) {
		return nil, invalid("category 只能是 %s", strings.Join(Categories, "、"))
	}
	if err := checkSource(in.Source, in.SourceType, in.Author); err != nil {
		return nil, err
	}
	format := extract.Format(in.FileName)
	if format == "" {
		return nil, invalid("无法从文件名 %q 判断格式，只收 md、txt、docx、xlsx、pdf", in.FileName)
	}
	if len(in.Content) == 0 {
		return nil, invalid("内容为空")
	}
	if int64(len(in.Content)) > uc.maxUpload {
		return nil, invalid("文件超过 %d MB", uc.maxUpload>>20)
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = strings.TrimSuffix(in.FileName, filepath.Ext(in.FileName))
	}
	if title == "" {
		return nil, invalid("title 为空")
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(uc.now()) {
		return nil, invalid("expires_at 已经过去")
	}

	hash := sha256Hex(in.Content)
	if res, err := uc.existing(ctx, hash); err != nil || res != nil {
		return res, err
	}
	sections, err := extract.Extract(format, in.Content)
	if err != nil {
		return nil, extractError(err)
	}
	chunks := SplitChunks(sections, uc.chunkOpt)
	if len(chunks) == 0 {
		return nil, invalid("文件里没有可用文字")
	}
	doc, ids, err := uc.repo.CreateDocument(ctx, NewDocument{
		Category:    in.Category,
		Title:       truncate(title, 256),
		Source:      in.Source,
		SourceType:  in.SourceType,
		Author:      in.Author,
		Format:      format,
		FileName:    truncate(in.FileName, 256),
		SizeBytes:   int64(len(in.Content)),
		ContentHash: hash,
		StockCodes:  cleanList(in.StockCodes),
		Tags:        cleanList(in.Tags),
		ExpiresAt:   in.ExpiresAt,
	}, chunkRows(chunks))
	if err != nil {
		return nil, err
	}
	if ids == nil {
		// 并发上传同一份内容，另一条先写进去了。
		return &IngestResult{Document: doc, Duplicate: true, Embedded: doc.EmbedStatus == "done"}, nil
	}
	embedded := uc.embedNew(ctx, ids, chunks)
	if embedded {
		doc.EmbedStatus = "done"
	}
	return &IngestResult{Document: doc, Embedded: embedded}, nil
}

func (uc *KnowledgeUsecase) existing(ctx context.Context, hash string) (*IngestResult, error) {
	old, err := uc.repo.FindDocumentByHash(ctx, hash)
	if err != nil || old == nil {
		return nil, err
	}
	if old.Status != "active" {
		if old, err = uc.repo.ReactivateDocument(ctx, old.ID); err != nil {
			return nil, err
		}
	}
	return &IngestResult{Document: old, Duplicate: true, Embedded: old.EmbedStatus == "done"}, nil
}

// checkSource 实现 FR-04-08：来源为空或类型不在白名单就拒收。signal 只由系统生成案例时使用。
func checkSource(source, sourceType, author string) error {
	if source == "" {
		return invalid("source 为空，来源不明的文档不入库")
	}
	switch sourceType {
	case "file":
	case "url":
		u, err := url.Parse(source)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return invalid("url 来源必须是 http(s) 地址")
		}
	case "manual":
		if author == "" {
			return invalid("手工录入必须写 author")
		}
	case "signal":
		return invalid("signal 来源只由系统生成案例时使用")
	default:
		return invalid("source_type 只能是 file、url、manual")
	}
	return nil
}

func extractError(err error) error {
	if errors.Is(err, extract.ErrUnsupported) || errors.Is(err, extract.ErrNoText) {
		msg := err.Error()
		if i := strings.Index(msg, ": "); i >= 0 {
			msg = msg[i+2:]
		}
		return invalid("%s", msg)
	}
	return err
}

func chunkRows(chunks []Chunk) []ChunkRow {
	rows := make([]ChunkRow, len(chunks))
	for i, c := range chunks {
		rows[i] = ChunkRow{
			Seq:     c.Seq,
			Heading: c.Heading,
			Content: c.Content,
			Tokens:  c.Tokens,
			TSV:     pgext.FormatTSVector(Lexemes(c.EmbedText())),
		}
	}
	return rows
}

// embedNew 给刚写入的分块算向量。失败只记日志，留给补嵌入任务。
func (uc *KnowledgeUsecase) embedNew(ctx context.Context, ids []int, chunks []Chunk) bool {
	if uc.embed == nil || len(ids) != len(chunks) {
		return false
	}
	items := make([]ChunkText, len(ids))
	for i, id := range ids {
		items[i] = ChunkText{ID: id, Text: chunks[i].EmbedText()}
	}
	if err := uc.embedItems(ctx, items); err != nil {
		uc.log.WithContext(ctx).Warnf("embed later: %v", err)
		return false
	}
	return true
}

func (uc *KnowledgeUsecase) embedItems(ctx context.Context, items []ChunkText) error {
	for start := 0; start < len(items); start += uc.embedBatch {
		end := min(start+uc.embedBatch, len(items))
		batch := items[start:end]
		texts := make([]string, len(batch))
		for i, it := range batch {
			texts[i] = it.Text
		}
		vecs, err := uc.embed.EmbedDocuments(ctx, texts)
		if err != nil {
			return err
		}
		if len(vecs) != len(batch) {
			return fmt.Errorf("kb: embedder returned %d vectors for %d texts", len(vecs), len(batch))
		}
		m := make(map[int][]float32, len(batch))
		for i, it := range batch {
			m[it.ID] = vecs[i]
		}
		if err := uc.repo.SaveEmbeddings(ctx, uc.embed.Model(), m); err != nil {
			return err
		}
	}
	return nil
}

// EmbedPending 补齐缺向量或模型不一致的分块。嵌入服务没配置时什么也不做。
func (uc *KnowledgeUsecase) EmbedPending(ctx context.Context) (int, error) {
	if uc.embed == nil {
		return 0, nil
	}
	total := 0
	for round := 0; round < pendingRounds; round++ {
		pending, err := uc.repo.PendingEmbeddings(ctx, uc.embed.Model(), pendingBatch)
		if err != nil || len(pending) == 0 {
			return total, err
		}
		items := make([]ChunkText, len(pending))
		for i, c := range pending {
			items[i] = ChunkText{ID: c.ID, Text: Chunk{Heading: c.Heading, Content: c.Content}.EmbedText()}
		}
		if err := uc.embedItems(ctx, items); err != nil {
			return total, err
		}
		total += len(items)
	}
	return total, nil
}

// SearchInput 是混合检索请求。
type SearchInput struct {
	Query      string
	TopK       int
	Categories []string
	StockCode  string
}

type SearchResult struct {
	Hits []Hit
	// Degraded 为真表示向量路不可用，只用了关键词。
	Degraded bool
}

func (uc *KnowledgeUsecase) Search(ctx context.Context, in SearchInput) (*SearchResult, error) {
	q := strings.TrimSpace(in.Query)
	if q == "" {
		return nil, invalid("query 为空")
	}
	for _, c := range in.Categories {
		if !contains(Categories, c) {
			return nil, invalid("未知分类 %q", c)
		}
	}
	f := SearchFilter{Categories: in.Categories, StockCode: strings.TrimSpace(in.StockCode), Now: uc.now()}
	return uc.hybrid(ctx, q, f, clampTopK(in.TopK, defaultTopK))
}

func clampTopK(k, def int) int {
	if k <= 0 {
		return def
	}
	return min(k, maxTopK)
}

// hybrid 两路各取前 50，倒数排名融合后取 topK。
func (uc *KnowledgeUsecase) hybrid(ctx context.Context, query string, f SearchFilter, topK int) (*SearchResult, error) {
	var kw, vec []int
	terms := QueryTerms(query)
	if len(terms) > maxQueryTerms {
		terms = terms[:maxQueryTerms]
	}
	if len(terms) > 0 {
		ids, err := uc.repo.KeywordSearch(ctx, pgext.FormatTSQueryOr(terms), f, pathLimit)
		if err != nil {
			return nil, err
		}
		kw = ids
	}
	degraded := uc.embed == nil
	if !degraded {
		qv, err := uc.embed.EmbedQuery(ctx, query)
		if err != nil {
			uc.log.WithContext(ctx).Warnf("query embedding failed, keyword only: %v", err)
			degraded = true
		} else {
			ids, err := uc.repo.VectorSearch(ctx, uc.embed.Model(), qv, f, pathLimit)
			if err != nil {
				return nil, err
			}
			vec = ids
		}
	}
	fused := FuseRRF(RRFK, kw, vec)
	if len(fused) > topK {
		fused = fused[:topK]
	}
	ids := make([]int, len(fused))
	for i, x := range fused {
		ids[i] = x.ID
	}
	loaded, err := uc.repo.LoadHits(ctx, ids)
	if err != nil {
		return nil, err
	}
	res := &SearchResult{Degraded: degraded, Hits: make([]Hit, 0, len(fused))}
	for _, x := range fused {
		h, ok := loaded[x.ID]
		if !ok {
			continue
		}
		h.Score = x.Score
		h.KeywordRank, h.VectorRank = x.Ranks[0], x.Ranks[1]
		res.Hits = append(res.Hits, h)
	}
	return res, nil
}

// SimilarInput 是相似案例请求。AsOf 为零时取当前时间。
type SimilarInput struct {
	Situation    string
	Pattern      string
	EmotionPhase string
	StockCode    string
	Strategy     string
	Book         string
	AsOf         time.Time
	TopK         int
}

type CaseHit struct {
	Case    Case
	ChunkID int
	Score   float64
}

type SimilarResult struct {
	Cases    []CaseHit
	Degraded bool
}

// SimilarCases 先按结构化条件和 as_of 筛案例，再在这些案例文档上做混合检索。
func (uc *KnowledgeUsecase) SimilarCases(ctx context.Context, in SimilarInput) (*SimilarResult, error) {
	asOf := in.AsOf
	if asOf.IsZero() {
		asOf = uc.now()
	}
	topK := clampTopK(in.TopK, defaultCaseTopK)
	f := CaseFilter{
		Pattern:      strings.TrimSpace(in.Pattern),
		EmotionPhase: strings.TrimSpace(in.EmotionPhase),
		StockCode:    strings.TrimSpace(in.StockCode),
		Strategy:     strings.TrimSpace(in.Strategy),
		Book:         strings.TrimSpace(in.Book),
		AsOf:         asOf,
	}
	situation := strings.TrimSpace(in.Situation)
	if situation == "" {
		cases, err := uc.repo.FindCases(ctx, f, topK)
		if err != nil {
			return nil, err
		}
		out := &SimilarResult{}
		for _, c := range cases {
			out.Cases = append(out.Cases, CaseHit{Case: c})
		}
		return out, nil
	}
	cases, err := uc.repo.FindCases(ctx, f, caseCandidates)
	if err != nil {
		return nil, err
	}
	byDoc := map[int]Case{}
	var docIDs []int
	for _, c := range cases {
		if c.DocID != nil {
			byDoc[*c.DocID] = c
			docIDs = append(docIDs, *c.DocID)
		}
	}
	out := &SimilarResult{Degraded: uc.embed == nil}
	if len(docIDs) == 0 {
		return out, nil
	}
	res, err := uc.hybrid(ctx, situation, SearchFilter{
		Categories: []string{CategoryCase},
		DocIDs:     docIDs,
		Now:        uc.now(),
	}, maxTopK)
	if err != nil {
		return nil, err
	}
	out.Degraded = res.Degraded
	seen := map[int]bool{}
	for _, h := range res.Hits {
		c, ok := byDoc[h.DocID]
		if !ok || seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		out.Cases = append(out.Cases, CaseHit{Case: c, ChunkID: h.ChunkID, Score: h.Score})
		if len(out.Cases) == topK {
			break
		}
	}
	return out, nil
}

// SignalCase 是走完的信号，字段对齐 M04 设计第 6 节的事件载荷。
type SignalCase struct {
	SignalID     int64
	Book         string
	Status       string
	StockCode    string
	StockName    string
	Strategy     string
	SignalType   string
	SignalTime   time.Time
	ClosedAt     time.Time
	EntryPrice   *float64
	ExitPrice    *float64
	PnlPct       *float64
	HoldingDays  *int
	Pattern      string
	EmotionPhase string
	Attribution  string
	Reasoning    string
	Review       string
}

type CaseResult struct {
	CaseID  int
	DocID   int
	Created bool
}

// IsTerminal 判断信号状态是否已走完。
func IsTerminal(status string) bool { return contains(TerminalStatuses, status) }

// RecordCase 把走完的信号写成案例。(book, signal_id) 已有案例时直接返回原案例。
func (uc *KnowledgeUsecase) RecordCase(ctx context.Context, s SignalCase) (*CaseResult, error) {
	s.Book = strings.TrimSpace(s.Book)
	s.StockCode = strings.TrimSpace(s.StockCode)
	switch {
	case s.SignalID <= 0:
		return nil, invalid("signal_id 必须大于 0")
	case s.Book != "paper" && s.Book != "live":
		return nil, invalid("book 只能是 paper 或 live")
	case !IsTerminal(s.Status):
		return nil, invalid("status %q 不是终态", s.Status)
	case s.StockCode == "":
		return nil, invalid("stock_code 为空")
	case s.SignalTime.IsZero():
		return nil, invalid("signal_time 为空")
	}
	if s.ClosedAt.IsZero() {
		s.ClosedAt = uc.now()
	}
	rec := CaseRecord{
		Book:         s.Book,
		SignalID:     s.SignalID,
		StockCode:    s.StockCode,
		StockName:    truncate(s.StockName, 32),
		Strategy:     truncate(s.Strategy, 64),
		Pattern:      truncate(s.Pattern, 64),
		EmotionPhase: truncate(s.EmotionPhase, 32),
		SignalType:   truncate(s.SignalType, 16),
		FinalStatus:  s.Status,
		SignalTime:   s.SignalTime,
		ClosedAt:     s.ClosedAt,
		EntryPrice:   s.EntryPrice,
		ExitPrice:    s.ExitPrice,
		PnlPct:       s.PnlPct,
		HoldingDays:  s.HoldingDays,
		Outcome:      outcomeOf(s.Status, s.PnlPct),
	}
	if contains(Attributions, s.Attribution) {
		rec.Attribution = s.Attribution
	}
	body := []byte(caseMarkdown(s, rec))
	sections, err := extract.Extract("md", body)
	if err != nil {
		return nil, err
	}
	chunks := SplitChunks(sections, uc.chunkOpt)
	doc := NewDocument{
		Category:    CategoryCase,
		Title:       truncate(caseTitle(s), 256),
		Source:      fmt.Sprintf("signal:%s:%d", s.Book, s.SignalID),
		SourceType:  "signal",
		Author:      "system",
		Format:      "md",
		SizeBytes:   int64(len(body)),
		ContentHash: sha256Hex(body),
		StockCodes:  []string{s.StockCode},
		Tags:        cleanList([]string{s.Strategy, s.Pattern, s.EmotionPhase}),
	}
	caseID, docID, ids, created, err := uc.repo.CreateCase(ctx, rec, doc, chunkRows(chunks))
	if err != nil {
		return nil, err
	}
	if created {
		uc.embedNew(ctx, ids, chunks)
	}
	return &CaseResult{CaseID: caseID, DocID: docID, Created: created}, nil
}

func outcomeOf(status string, pnl *float64) string {
	switch status {
	case "expired":
		return "unfilled"
	case "cancelled":
		return "cancelled"
	}
	switch {
	case pnl == nil || *pnl == 0:
		return "flat"
	case *pnl > 0:
		return "win"
	default:
		return "loss"
	}
}

var outcomeNames = map[string]string{
	"win": "盈利", "loss": "亏损", "flat": "持平", "unfilled": "未成交", "cancelled": "撤销",
}

var attributionNames = map[string]string{
	"indicator_failed": "指标失效", "news_misread": "消息误判",
	"unfilled": "买不进", "emotion_mismatch": "情绪周期不匹配",
}

func caseTitle(s SignalCase) string {
	parts := []string{"案例", s.StockCode}
	if s.StockName != "" {
		parts = append(parts, s.StockName)
	}
	if s.Strategy != "" {
		parts = append(parts, s.Strategy)
	}
	parts = append(parts, s.SignalTime.In(tradecal.Shanghai()).Format("2006-01-02"))
	return strings.Join(parts, " ")
}

// caseMarkdown 是案例正文。形态、情绪阶段、结果、归因都写进去，便于关键词和向量检索。
func caseMarkdown(s SignalCase, r CaseRecord) string {
	var b strings.Builder
	line := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "- %s：%s\n", k, v)
		}
	}
	fmt.Fprintf(&b, "# %s\n\n", caseTitle(s))
	line("账本", map[string]string{"paper": "模拟盘", "live": "实盘"}[s.Book])
	// 正文按内容哈希去重，写上信号编号，字段全相同的两条信号才不会撞成一篇。
	line("信号编号", strconv.FormatInt(s.SignalID, 10))
	line("股票", strings.TrimSpace(s.StockCode+" "+s.StockName))
	line("策略", s.Strategy)
	line("形态", s.Pattern)
	line("情绪阶段", s.EmotionPhase)
	line("方向", s.SignalType)
	line("信号时间", s.SignalTime.In(tradecal.Shanghai()).Format("2006-01-02 15:04"))
	line("结束时间", s.ClosedAt.In(tradecal.Shanghai()).Format("2006-01-02 15:04"))
	line("入场价", fmtFloat(s.EntryPrice, ""))
	line("出场价", fmtFloat(s.ExitPrice, ""))
	line("收益率", fmtFloat(s.PnlPct, "%"))
	if s.HoldingDays != nil {
		line("持仓天数", strconv.Itoa(*s.HoldingDays))
	}
	line("结果", outcomeNames[r.Outcome])
	attr := attributionNames[r.Attribution]
	if attr == "" {
		attr = strings.TrimSpace(s.Attribution)
	}
	line("归因", attr)
	if t := strings.TrimSpace(s.Reasoning); t != "" {
		fmt.Fprintf(&b, "\n## 信号理由\n\n%s\n", t)
	}
	if t := strings.TrimSpace(s.Review); t != "" {
		fmt.Fprintf(&b, "\n## 复盘\n\n%s\n", t)
	}
	return b.String()
}

func fmtFloat(v *float64, unit string) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', -1, 64) + unit
}

// Purge 是月度清理：失效超过 30 天的文档连同分块删除。
func (uc *KnowledgeUsecase) Purge(ctx context.Context) (int, error) {
	now := uc.now()
	return uc.repo.Purge(ctx, now, now.Add(-PurgeAfter))
}

func (uc *KnowledgeUsecase) GetDocument(ctx context.Context, id int) (*Document, []StoredChunk, error) {
	doc, err := uc.repo.GetDocument(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	chunks, err := uc.repo.DocumentChunks(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return doc, chunks, nil
}

func (uc *KnowledgeUsecase) ListDocuments(ctx context.Context, f ListFilter) ([]Document, int, error) {
	if f.Category != "" && !contains(Categories, f.Category) {
		return nil, 0, invalid("未知分类 %q", f.Category)
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	return uc.repo.ListDocuments(ctx, f)
}

func (uc *KnowledgeUsecase) DeleteDocument(ctx context.Context, id int) error {
	return uc.repo.DeleteDocument(ctx, id)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func cleanList(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
