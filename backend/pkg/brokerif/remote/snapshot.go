package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// M13-T04 实盘回报归并的只读解析面。
// Snapshot 是 broker-gw `/v1/broker/snapshot` 载荷的强形状映射（reader.normalize
// 之后的语义：symbol 已规范为 SHSE.600519、枚举已译为 state/kind、金额保留十进制
// 字符串）。解析纪律与 client.request 一致：
//   - 协议/账户/隔离模式不回声 → 报身份不符，绝不静默降级；
//   - 行级形状与数值非法 → 整帧报错（「没取到≠0」：坏行不能被当成缺席，
//     也不能被当成 0 入账；每类回报的新鲜度门在 biz 归并侧按节判）；
//   - 追加流（execution_report/order_status_change）fresh 恒为 null，
//     其可用性只看 available；全量流（cash/position/order_status）必须
//     available ∧ fresh=true 才可用。

type SnapshotReport[T any] struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Fresh     *bool  `json:"fresh"`
	Reason    string `json:"reason"`
	Records   []T    `json:"records"`
}

// Ready 判定某一类回报是否可用于入账/对账：追加流只要 available，全量流
// 还必须新鲜（mtime 在 max_age 内）。没取到/过期一律 false，调用方跳过该节。
func (r SnapshotReport[T]) Ready() bool {
	return r.Available && (r.Fresh == nil || *r.Fresh)
}

type OrderRow struct {
	SID          string `json:"sid"`
	Symbol       string `json:"symbol"`
	State        string `json:"state"` // PENDING/ACKNOWLEDGED/PARTIAL/FILLED/CANCELLED/REJECTED/EXPIRED
	Business     string `json:"order_business"`
	Price        string `json:"price"` // 十进制字符串，传输不抹精度
	Volume       int    `json:"volume"`
	FilledVolume int    `json:"filled_volume"`
	RejectReason string `json:"ord_rej_reason"`
	RejectDetail string `json:"ord_rej_reason_detail"`
}

// Side 把 1/2 业务枚举翻成 buy/sell；其他值报错（reader 已验枚举，双闸）。
func (r OrderRow) Side() (string, error) { return businessSide(r.Business) }

type ExecutionRow struct {
	SID      string `json:"sid"`
	ExecID   string `json:"exec_id"`
	Symbol   string `json:"symbol"`
	Kind     string `json:"kind"` // FILL / CANCEL_REJECTED
	Business string `json:"order_business"`
	Price    string `json:"price"`
	Amount   string `json:"amount"`
	Volume   int    `json:"volume"`
}

func (r ExecutionRow) Side() (string, error) { return businessSide(r.Business) }

type PositionRow struct {
	Symbol       string `json:"symbol"`
	Volume       int    `json:"volume"`
	VolumeToday  int    `json:"volume_today"`
	AvailableNow int    `json:"available_now"`
}

type CashRow struct {
	Balance   string `json:"balance"`
	Available string `json:"available"`
	Frozen    string `json:"frozen"`
}

type Snapshot struct {
	Protocol  string    `json:"protocol"`
	Mode      string    `json:"mode"`
	AccountID string    `json:"account_id"`
	Ready     bool      `json:"reports_ready"`
	Reasons   []string  `json:"reasons"`
	Observed  time.Time `json:"observed_at"`

	Orders     SnapshotReport[OrderRow]
	Executions SnapshotReport[ExecutionRow]
	Positions  SnapshotReport[PositionRow]
	Cash       SnapshotReport[CashRow]
}

// ParseSnapshot 解析并校验一帧快照。account 是客户端白名单账号：与信封
// 回声校验同口径，账号不符即拒绝，防止把隔壁账户的回报进自己的账。
func ParseSnapshot(raw json.RawMessage, account string) (Snapshot, error) {
	var payload struct {
		Protocol  string          `json:"protocol"`
		Mode      string          `json:"mode"`
		AccountID string          `json:"account_id"`
		Ready     bool            `json:"reports_ready"`
		Reasons   []string        `json:"reasons"`
		CanOpen   bool            `json:"can_open_live"`
		Observed  float64         `json:"observed_at"`
		Reports   json.RawMessage `json:"reports"`
	}
	var result Snapshot
	if json.Unmarshal(raw, &payload) != nil {
		return result, errors.New("broker remote: invalid snapshot shape")
	}
	if payload.Protocol != Protocol || payload.Mode != "ISOLATED" || payload.AccountID != account || payload.CanOpen {
		return result, errors.New("broker remote: snapshot identity mismatch")
	}
	result = Snapshot{Protocol: payload.Protocol, Mode: payload.Mode, AccountID: payload.AccountID, Ready: payload.Ready, Reasons: payload.Reasons,
		Observed: time.UnixMilli(int64(payload.Observed * 1000)).UTC()}
	var reports map[string]json.RawMessage
	if json.Unmarshal(payload.Reports, &reports) != nil || len(reports) == 0 {
		return result, errors.New("broker remote: snapshot reports missing")
	}
	if err := decodeReport(reports, "order_status.csv", "order_status_change.csv", &result.Orders); err != nil {
		return result, err
	}
	if err := decodeReport(reports, "execution_report.csv", "", &result.Executions); err != nil {
		return result, err
	}
	if err := decodeReport(reports, "position.csv", "", &result.Positions); err != nil {
		return result, err
	}
	if err := decodeReport(reports, "cash.csv", "", &result.Cash); err != nil {
		return result, err
	}
	if err := validate(result); err != nil {
		return result, err
	}
	return result, nil
}

// decodeReport 取一份回报：primary 缺失时退回 merge（order_status 与变更流并集；
// reader 侧冲突（CONFLICTING_REPORT_IDENTITY）已在网关判死，这里只做并集合）。
func decodeReport[T any](reports map[string]json.RawMessage, primary, merge string, out *SnapshotReport[T]) error {
	raw, ok := reports[primary]
	if !ok {
		return fmt.Errorf("broker remote: snapshot report %s missing", primary)
	}
	if err := json.Unmarshal(raw, out); err != nil || out.Name != primary {
		return fmt.Errorf("broker remote: snapshot report %s malformed", primary)
	}
	if merge != "" {
		var extra SnapshotReport[T]
		if m, ok := reports[merge]; ok && json.Unmarshal(m, &extra) == nil && extra.Available {
			out.Records = append(out.Records, extra.Records...)
		}
	}
	return nil
}

func validate(in Snapshot) error {
	for _, row := range valueOr(in.Orders.Records) {
		if !safeSID.MatchString(row.SID) || row.Symbol == "" || !queryState(row.State) || row.Volume <= 0 || row.FilledVolume < 0 || row.FilledVolume > row.Volume {
			return fmt.Errorf("broker remote: invalid order row for %q", row.SID)
		}
		if _, err := row.Side(); err != nil {
			return err
		}
		if price(row.Price) <= 0 {
			return fmt.Errorf("broker remote: invalid order price for %q", row.SID)
		}
	}
	for _, row := range valueOr(in.Executions.Records) {
		var err error
		switch row.Kind {
		case "FILL":
			if !safeSID.MatchString(row.SID) || !safeSID.MatchString(row.ExecID) || row.Symbol == "" || row.Volume <= 0 {
				return fmt.Errorf("broker remote: invalid fill row for %q", row.SID)
			}
			if price(row.Price) <= 0 || price(row.Amount) <= 0 {
				return fmt.Errorf("broker remote: invalid fill amount for %q", row.ExecID)
			}
			_, err = row.Side()
		case "CANCEL_REJECTED":
			if !safeSID.MatchString(row.SID) {
				return fmt.Errorf("broker remote: invalid cancel rejection for %q", row.SID)
			}
		default:
			err = fmt.Errorf("broker remote: unknown execution kind %q", row.Kind)
		}
		if err != nil {
			return err
		}
	}
	for _, row := range valueOr(in.Positions.Records) {
		if row.Symbol == "" || row.Volume < 0 || row.AvailableNow < 0 || row.VolumeToday < 0 || row.AvailableNow > row.Volume || row.VolumeToday > row.Volume {
			return fmt.Errorf("broker remote: invalid position row for %q", row.Symbol)
		}
	}
	if len(in.Cash.Records) > 1 {
		return errors.New("broker remote: cash row is not unique")
	}
	for _, row := range valueOr(in.Cash.Records) {
		if price(row.Balance) < 0 || price(row.Available) < 0 || price(row.Frozen) < 0 {
			return errors.New("broker remote: invalid cash row")
		}
	}
	return nil
}

func valueOr[T any](rows []T) []T {
	if rows == nil {
		return []T{}
	}
	return rows
}

func businessSide(business string) (string, error) {
	switch business {
	case "1":
		return "buy", nil
	case "2":
		return "sell", nil
	default:
		return "", fmt.Errorf("broker remote: unknown order business %q", business)
	}
}

func price(raw string) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return -1
	}
	return value
}
