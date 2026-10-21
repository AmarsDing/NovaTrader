package biz

import (
	"time"

	"server/pkg/tradecal"
)

// seal.go —— P1-6 纪律（对标 vibe-astock「复盘日之后的数据不进 AI 资料包」）：
// 喂给 AI 的行情/资讯快照一律按复盘日 15:00（上海时区）封盘裁剪，防未来函数。
// 这是纪律不是功能：凡是「回看某日」的资料包，窗口终点必须传 beforeSeal 的返回值；
// 历史复盘 or D+1 补跑时落在封盘点，盘中生成则截到当下。

// sealOf 返回 day 的封盘点（上海时区 15:00）。A 股 15:00 之后的读数算下一周期。
func sealOf(day time.Time) time.Time {
	d := day.In(tradecal.Shanghai())
	return time.Date(d.Year(), d.Month(), d.Day(), 15, 0, 0, 0, tradecal.Shanghai())
}

// beforeSeal 资料包窗口终点：min(now, 复盘日封盘点)。
// 晚于封盘才生成（正常调度、隔夜补跑）→ 封盘点；盘中生成 → 此刻（本来就是当下，无未来数据）。
func beforeSeal(day, now time.Time) time.Time {
	if seal := sealOf(day); now.After(seal) {
		return seal
	}
	return now
}
