package data

import (
	"context"

	"server/ent"
	"server/ent/overseasmapping"
	"server/ent/seattag"
	"server/pkg/market"
)

// 默认席位只收录交易所榜单上的固定名称。营业部席位名称经常变，不在这里猜。
var defaultSeats = []struct {
	name, tag, alias string
}{
	{"机构专用", market.SeatInstitution, ""},
	{"沪股通专用", market.SeatNorth, "沪股通"},
	{"深股通专用", market.SeatNorth, "深股通"},
}

// 板块代码是通达信行业（tdxhy.cfg 第三列，本机 incon.dat 的名称，2026-10-24 逐项对本地 incon.dat 核实。
// M01 外围代码已落地：CL（WTI）、XAU（COMEX黄金）、SOX（费城半导体）；航空以油价反向映射承载
// （油价上行压航空利润，权重取负）。
var defaultMappings = []struct {
	asset, sector string
	weight        float64
	note          string
}{
	{"CL", "T010301", 1, "WTI → 石油开采"},
	{"CL", "T010302", 0.5, "WTI → 石油加工"},
	{"CL", "T0701", -1, "WTI 上行压航空利润（反向映射）"},
	{"XAU", "T020204", 1, "COMEX黄金 → 黄金"},
	{"SOX", "T1203", 1, "费城半导体 → 半导体"},
}

// EnsureDefaults 补默认行。已有行不更新，人工改过的标签和权重保留。
func (r *Repo) EnsureDefaults(ctx context.Context) error {
	have, err := r.db.SeatTag.Query().Select(seattag.FieldSeatName).Strings(ctx)
	if err != nil {
		return err
	}
	known := map[string]struct{}{}
	for _, name := range have {
		known[name] = struct{}{}
	}
	var seats []*ent.SeatTagCreate
	for _, s := range defaultSeats {
		if _, ok := known[s.name]; ok {
			continue
		}
		seats = append(seats, r.db.SeatTag.Create().SetSeatName(s.name).SetTag(s.tag).SetAlias(s.alias))
	}
	if len(seats) > 0 {
		if err := r.db.SeatTag.CreateBulk(seats...).
			OnConflictColumns(seattag.FieldSeatName).DoNothing().Exec(ctx); err != nil {
			return err
		}
	}
	rows, err := r.db.OverseasMapping.Query().All(ctx)
	if err != nil {
		return err
	}
	haveMap := map[string]struct{}{}
	for _, row := range rows {
		haveMap[row.AssetCode+"|"+row.SectorCode] = struct{}{}
	}
	var maps []*ent.OverseasMappingCreate
	for _, m := range defaultMappings {
		if _, ok := haveMap[m.asset+"|"+m.sector]; ok {
			continue
		}
		maps = append(maps, r.db.OverseasMapping.Create().
			SetAssetCode(m.asset).SetSectorCode(m.sector).SetWeight(m.weight).SetNote(m.note))
	}
	if len(maps) == 0 {
		return nil
	}
	return r.db.OverseasMapping.CreateBulk(maps...).
		OnConflictColumns(overseasmapping.FieldAssetCode, overseasmapping.FieldSectorCode).
		DoNothing().Exec(ctx)
}
