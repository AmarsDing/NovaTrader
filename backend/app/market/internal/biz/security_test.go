package biz

import (
	"context"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
)

// securitySpy fakeRepo 之上只覆写 SecurityBasics，记录收到的 codes。
type securitySpy struct {
	fakeRepo
	gotCodes [][]string
	maxUp    time.Time
}

func (s *securitySpy) SecurityBasics(_ context.Context, codes []string) ([]SecurityBasicRow, time.Time, error) {
	s.gotCodes = append(s.gotCodes, append([]string(nil), codes...))
	return nil, s.maxUp, nil
}

// 名称字典 N0：codes trim+大写规整+保序去重；空 codes 原样直通（数据层全量口径）；
// maxUpdate 版本戳原样透传。
func TestSecurityBasicsUsecase(t *testing.T) {
	spy := &securitySpy{maxUp: time.Date(2026, 10, 20, 15, 0, 0, 0, time.Local)}
	u := NewUsecase(spy, nil, nil, NewConfig(nil), log.DefaultLogger)

	if _, got, err := u.SecurityBasics(context.Background(),
		[]string{" 600519.sh ", "600519.SH", "000001.SZ", "", "000001.sz"}); err != nil || !got.Equal(spy.maxUp) {
		t.Fatalf("err=%v maxUp=%v want %v", err, got, spy.maxUp)
	}
	codes := spy.gotCodes[0]
	if len(codes) != 2 || codes[0] != "600519.SH" || codes[1] != "000001.SZ" {
		t.Fatalf("codes=%v want [600519.SH 000001.SZ]（trim+大写+保序去重）", codes)
	}

	if _, _, err := u.SecurityBasics(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if spy.gotCodes[1] != nil {
		t.Fatalf("nil codes 应原样直通（=数据层全量），got %v", spy.gotCodes[1])
	}
}
