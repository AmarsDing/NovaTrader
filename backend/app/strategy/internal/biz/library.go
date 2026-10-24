// library.go 是 B4 声明式策略在扫描链的装载件：strategy_library 表
// status=active 且 code 非空的行，经 pkg/rules/decl 求值后追加进本轮
// 策略集（排在内置模板之后）。纪律：
//   - 行级解析/构造失败绝不半激活：整行弃用，warn + rep.Missing 记
//     library_invalid:<name>（数据纪律——该行定义缺失，不冒充内置模板）；
//   - 与在轮策略重名（内置或先行声明式行）拒绝整行：M09 出口按策略名取
//     ExitPlan，重名即静默换轨，宁可不装；
//   - 端口未装配或读表失败：本轮只用内置模板，失败显式标 library_invalid。
package biz

import (
	"context"
	"strings"

	"server/pkg/rules"
	"server/pkg/rules/decl"
)

// MissLibraryInvalid 是 strategy_library 在册声明式策略本轮不可用的缺失标记。
// 单条追加 ":<行名>"；整表读失败只标记本体。
const MissLibraryInvalid = "library_invalid"

// LibraryRow 是一行在册声明式策略（status=active + code 非空，由 data 层担保）。
type LibraryRow struct {
	Name string
	Code string
}

// LibraryRepo 读取 strategy_library。写方（策略编辑/晋级通道）不在本链内。
type LibraryRepo interface {
	ActiveLibrary(ctx context.Context) ([]LibraryRow, error)
}

// SetLibrary 装配在册声明式策略端口（可选；不装配则只用内置模板）。
func (uc *Usecase) SetLibrary(l LibraryRepo) { uc.library = l }

// mergeLibrary 把在册声明式策略追加到 base（内置模板）之后。rep 为 nil
// 时只做日志（exit 链无报告可记）。
func (uc *Usecase) mergeLibrary(ctx context.Context, base []rules.Strategy, rep *ScanReport) []rules.Strategy {
	if uc.library == nil {
		return base
	}
	miss := func(kind string) {
		uc.log.Warnf("strategy_library: %s（本轮按缺失处理，不落 0 不冒充模板）", kind)
		if rep != nil {
			rep.Missing = append(rep.Missing, kind)
		}
	}
	rows, err := uc.library.ActiveLibrary(ctx)
	if err != nil {
		miss(MissLibraryInvalid)
		return base
	}
	byName := map[string]bool{}
	for _, s := range base {
		byName[s.Name()] = true
	}
	for _, row := range rows {
		name := strings.TrimSpace(row.Name)
		bad := func(why error) {
			uc.log.Warnf("strategy_library row %q invalid: %v", name, why)
			miss(MissLibraryInvalid + ":" + name)
		}
		def, err := decl.Parse([]byte(row.Code))
		if err != nil {
			bad(err)
			continue
		}
		// 行名与 code 内 name 一致才算同一意图；不一致按行名记缺失，不猜哪边是对的。
		if name != "" && def.Name != name {
			bad(errNameMismatch(name, def.Name))
			continue
		}
		if byName[def.Name] {
			bad(errDupName(def.Name))
			continue
		}
		st, err := def.Strategy(rules.LoadPrice(nil))
		if err != nil {
			bad(err)
			continue
		}
		byName[def.Name] = true
		base = append(base, st)
	}
	return base
}

func errNameMismatch(row, code string) error {
	return &libraryError{kind: "row name " + row + " != code name " + code}
}
func errDupName(name string) error { return &libraryError{kind: "duplicate strategy name " + name} }

type libraryError struct{ kind string }

func (e *libraryError) Error() string { return "decl: " + e.kind }
