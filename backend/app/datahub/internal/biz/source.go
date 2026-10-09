package biz

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Source 是一个数据源。一个源可以实现多个数据域的接口，注册时按域检查。
type Source interface {
	Name() string
}

type SecuritySource interface {
	Securities(ctx context.Context) ([]Security, error)
}

type DailyBarSource interface {
	DailyBars(ctx context.Context, q BarQuery) (BarBatch, error)
}

type MinuteBarSource interface {
	MinuteBars(ctx context.Context, q BarQuery) (BarBatch, error)
}

type AdjFactorSource interface {
	AdjFactors(ctx context.Context, day time.Time, symbols []string) ([]AdjFactor, error)
}

// SnapshotSource 拉全市场快照。symbols 为空表示全市场。
type SnapshotSource interface {
	Snapshots(ctx context.Context, symbols []string) (SnapshotBatch, error)
}

type LimitPoolSource interface {
	LimitPool(ctx context.Context, day time.Time) ([]LimitEntry, error)
}

// MoneyFlowSource 盘后个股资金流向。
type MoneyFlowSource interface {
	MoneyFlows(ctx context.Context, day time.Time) ([]MoneyFlow, error)
}

// IntradayFlowSource 盘中个股主力资金。
type IntradayFlowSource interface {
	IntradayFlows(ctx context.Context) ([]IntradayFlow, error)
}

type LhbSource interface {
	LhbSeats(ctx context.Context, day time.Time) ([]LhbSeat, error)
}

type MarginSource interface {
	Margins(ctx context.Context, day time.Time) ([]Margin, error)
}

type HsgtSource interface {
	HsgtTop10(ctx context.Context, day time.Time) ([]HsgtTop, error)
}

type SectorSource interface {
	Sectors(ctx context.Context) ([]Sector, error)
}

type SectorQuoteSource interface {
	SectorQuotes(ctx context.Context) ([]SectorQuote, error)
}

type FinanceSource interface {
	Finance(ctx context.Context, since time.Time) ([]FinanceItem, error)
}

// IntelSource 按类别拉增量情报：flash、news、announcement、report。
type IntelSource interface {
	Intel(ctx context.Context, kind string, q IntelQuery) ([]Intel, error)
}

type OverseasSource interface {
	Overseas(ctx context.Context) ([]OverseasQuote, error)
}

type MacroSource interface {
	Macro(ctx context.Context) ([]MacroPoint, error)
}

type HotRankSource interface {
	HotRank(ctx context.Context) ([]HotItem, error)
}

// Supports 判断源是否实现了数据域需要的接口。
func Supports(domain string, s Source) bool {
	switch domain {
	case DomainSecurity:
		_, ok := s.(SecuritySource)
		return ok
	case DomainDailyBar:
		_, ok := s.(DailyBarSource)
		return ok
	case DomainMinuteBar:
		_, ok := s.(MinuteBarSource)
		return ok
	case DomainAdjFactor:
		_, ok := s.(AdjFactorSource)
		return ok
	case DomainSnapshot:
		_, ok := s.(SnapshotSource)
		return ok
	case DomainLimitPool:
		_, ok := s.(LimitPoolSource)
		return ok
	case DomainMoneyFlow:
		_, a := s.(MoneyFlowSource)
		_, b := s.(IntradayFlowSource)
		return a || b
	case DomainLhb:
		_, ok := s.(LhbSource)
		return ok
	case DomainMargin:
		_, ok := s.(MarginSource)
		return ok
	case DomainHsgtTop10:
		_, ok := s.(HsgtSource)
		return ok
	case DomainSector:
		_, ok := s.(SectorSource)
		return ok
	case DomainSectorQuote:
		_, ok := s.(SectorQuoteSource)
		return ok
	case DomainFinance:
		_, ok := s.(FinanceSource)
		return ok
	case DomainFlash, DomainNews, DomainAnnouncement, DomainReport:
		_, ok := s.(IntelSource)
		return ok
	case DomainOverseas:
		_, ok := s.(OverseasSource)
		return ok
	case DomainMacro:
		_, ok := s.(MacroSource)
		return ok
	case DomainHotRank:
		_, ok := s.(HotRankSource)
		return ok
	}
	return false
}

// ErrUnsupported 表示该源不提供这个数据域或这种请求，流水线直接跳过，不计失败。
var ErrUnsupported = errors.New("datahub: source does not support this request")

// UnavailableError 表示源的依赖没就绪：插件缺失、客户端未启动、未配置密钥。
// 计入失败，健康状态显示为 unavailable。
type UnavailableError struct {
	Source string
	Reason string
}

func (e *UnavailableError) Error() string {
	return fmt.Sprintf("%s unavailable: %s", e.Source, e.Reason)
}

// Unavailable 构造一个 UnavailableError。
func Unavailable(source, format string, args ...any) error {
	return &UnavailableError{Source: source, Reason: fmt.Sprintf(format, args...)}
}

// StaleError 表示源头数据不是最新：行情时间落后，或本地文件没到应有交易日。FR-01-14。
type StaleError struct {
	Source string
	Reason string
}

func (e *StaleError) Error() string {
	return fmt.Sprintf("%s 数据不是最新：%s", e.Source, e.Reason)
}

// NoSourceError 表示一个数据域的所有源都失败或不可用。
type NoSourceError struct {
	Domain string
	Errors map[string]string
}

func (e *NoSourceError) Error() string {
	if len(e.Errors) == 0 {
		return fmt.Sprintf("datahub: %s 无可用源", e.Domain)
	}
	return fmt.Sprintf("datahub: %s 无可用源：%v", e.Domain, e.Errors)
}
