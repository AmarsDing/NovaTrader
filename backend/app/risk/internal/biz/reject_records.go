package biz

import (
	"context"
	"strings"
	"time"

	"server/pkg/tradecal"
)

// FE-R2 拒绝明细窗口：默认当天，上限 31 天；分页默认 50 条，上限 200。
const (
	defaultRecordDays  = 1
	maxRecordDays      = 31
	defaultRecordLimit = 50
	maxRecordLimit     = 200
)

// RejectRecordFilter 是明细查询条件（窗口与分页已由 biz 规范化，过滤串已 TrimSpace）。
type RejectRecordFilter struct {
	From, To    time.Time
	Rule        string
	Symbol      string
	Operator    string
	AccountType string
	Reason      string
	Offset      int
	Limit       int
}

// RejectRecord 是一条被拒的风控校验记录（risk_check 中 approved=false 的行）。
type RejectRecord struct {
	ClientOrderID string
	AccountType   string
	Symbol        string
	Side          string
	Source        string
	Operator      string
	Price         float64
	VolumeReq     int
	VolumeFinal   int
	Rule          string
	Reason        string
	LatencyUs     int64
	CreatedAt     time.Time
}

// RejectRecords 是 data 层返回的明细页：Total 是过滤条件下的总数，Items 是当前页。
type RejectRecords struct {
	Total int64
	Items []RejectRecord
}

// RejectRecordsQuery 是明细查询的原始参数（service 层透传请求字段，规范化在本层做）。
type RejectRecordsQuery struct {
	End         string
	Days        int
	Rule        string
	Symbol      string
	Operator    string
	AccountType string
	Reason      string
	Offset      int
	Limit       int
}

// RejectRecordsResult 是规范化后的查询口径与结果：End 为 YYYY-MM-DD，Items 按时间降序。
type RejectRecordsResult struct {
	End    string
	Days   int
	Offset int
	Limit  int
	Total  int64
	Items  []RejectRecord
}

// RejectRecords 返回 end 起回看 days 天（含 end）内被风控拒绝的逐条记录。
// end 为空取今天（上海时区）；days<=0 取 1，>31 截断；limit<=0 取 50，>200 截断；offset<0 归 0。
func (e *Engine) RejectRecords(ctx context.Context, q RejectRecordsQuery) (*RejectRecordsResult, error) {
	day := e.now().In(tradecal.Shanghai())
	if q.End != "" {
		d, err := time.ParseInLocation("2006-01-02", q.End, tradecal.Shanghai())
		if err != nil {
			return nil, invalid("end must be YYYY-MM-DD")
		}
		day = d
	}
	days := q.Days
	if days <= 0 {
		days = defaultRecordDays
	}
	if days > maxRecordDays {
		days = maxRecordDays
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultRecordLimit
	}
	if limit > maxRecordLimit {
		limit = maxRecordLimit
	}
	offset := q.Offset
	if offset < 0 {
		offset = 0
	}
	endDay := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, tradecal.Shanghai())
	recs, err := e.repo.RejectRecords(ctx, RejectRecordFilter{
		From:        endDay.AddDate(0, 0, -(days - 1)),
		To:          endDay.AddDate(0, 0, 1),
		Rule:        strings.TrimSpace(q.Rule),
		Symbol:      strings.TrimSpace(q.Symbol),
		Operator:    strings.TrimSpace(q.Operator),
		AccountType: strings.TrimSpace(q.AccountType),
		Reason:      strings.TrimSpace(q.Reason),
		Offset:      offset,
		Limit:       limit,
	})
	if err != nil {
		return nil, err
	}
	if recs == nil {
		recs = &RejectRecords{}
	}
	return &RejectRecordsResult{
		End:    endDay.Format("2006-01-02"),
		Days:   days,
		Offset: offset,
		Limit:  limit,
		Total:  recs.Total,
		Items:  recs.Items,
	}, nil
}
