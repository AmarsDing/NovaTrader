package biz

import (
	"testing"
	"time"

	"server/pkg/tradecal"
)

func TestSealOfClampsTo1500Shanghai(t *testing.T) {
	day := time.Date(2026, 10, 19, 9, 30, 0, 0, tradecal.Shanghai())
	seal := sealOf(day)
	if seal.Hour() != 15 || seal.Minute() != 0 || seal.Location() != tradecal.Shanghai() {
		t.Fatalf("sealOf 应为上海 15:00，得到 %s", seal)
	}
	if seal.Format("2006-01-02") != "2026-10-19" {
		t.Fatalf("sealOf 不应跨日，得到 %s", seal)
	}
}

func TestBeforeSeal(t *testing.T) {
	past := time.Date(2026, 10, 16, 0, 0, 0, 0, tradecal.Shanghai())
	now := time.Date(2026, 10, 19, 8, 0, 0, 0, tradecal.Shanghai())
	if got := beforeSeal(past, now); !got.Equal(sealOf(past)) {
		t.Fatalf("历史日复盘应裁到当日封盘 15:00，得到 %s", got)
	}
	today := dayOf(now)
	early := time.Date(2026, 10, 19, 10, 0, 0, 0, tradecal.Shanghai())
	if got := beforeSeal(today, early); !got.Equal(early) {
		t.Fatalf("封盘前生成应原样返回 now，得到 %s", got)
	}
	late := time.Date(2026, 10, 19, 16, 0, 0, 0, tradecal.Shanghai())
	if got := beforeSeal(today, late); !got.Equal(sealOf(today)) {
		t.Fatalf("封盘后生成应裁到当日封盘，得到 %s", got)
	}
}
