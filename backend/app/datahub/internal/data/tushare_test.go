package data

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"server/app/datahub/internal/biz"
)

func TestTushareDailyUnits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"msg":"","data":{"fields":["ts_code","trade_date","open","high","low","close","pre_close","vol","amount"],
			"items":[["600519.SH","20260930",1250.0,1260.0,1240.0,1255.0,1248.0,25168.37,3145413.157]]}}`))
	}))
	defer srv.Close()
	ts := NewTushare(srv.URL, "t", HTTPOptions{})
	d := time.Date(2026, 9, 30, 0, 0, 0, 0, shanghaiLoc())
	b, err := ts.DailyBars(context.Background(), biz.BarQuery{Start: d, End: d, Expect: d})
	if err != nil || len(b.Bars) != 1 {
		t.Fatalf("bars=%v err=%v", b.Bars, err)
	}
	bar := b.Bars[0]
	if bar.Symbol != "600519.SH" || bar.Volume != 2516837 || bar.Amount != 3145413157 || *bar.PreClose != 1248 || b.Stale {
		t.Fatalf("bad units %+v stale=%v", bar, b.Stale)
	}
	if !bar.Time.Equal(d) {
		t.Fatalf("bar time %v, want %v", bar.Time, d)
	}
}

func TestTushareErrors(t *testing.T) {
	if _, err := NewTushare("", "", HTTPOptions{}).Securities(context.Background()); !isUnavailable(err) {
		t.Fatalf("missing token should be unavailable, got %v", err)
	}
	if _, err := parseTushare("daily", []byte(`{"code":40203,"msg":"抱歉，您没有访问该接口的权限"}`)); !isUnavailable(err) {
		t.Fatalf("40203 should be unavailable, got %v", err)
	}
	if _, err := parseTushare("daily", []byte(`{"code":-2001,"msg":"参数错误"}`)); err == nil || isUnavailable(err) {
		t.Fatalf("other codes are plain failures, got %v", err)
	}
}

func isUnavailable(err error) bool {
	var u *biz.UnavailableError
	return errors.As(err, &u)
}
