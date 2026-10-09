package biz

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeSnap struct {
	name   string
	items  []Snapshot
	err    error
	calls  int
	stale  bool
}

func (f *fakeSnap) Name() string { return f.name }
func (f *fakeSnap) Snapshots(context.Context, []string) (SnapshotBatch, error) {
	f.calls++
	if f.err != nil {
		return SnapshotBatch{}, f.err
	}
	return SnapshotBatch{Items: f.items}, nil
}

func testPipe(t *testing.T, srcs []Source, names ...string) *Pipeline {
	t.Helper()
	cfg := Config{
		AllowUnofficial: true,
		BreakerFailures: 3,
		BreakerCooldown: time.Minute,
		Sources:         map[string]SourceConfig{},
		Domains:         map[string]DomainConfig{DomainSnapshot: {Enabled: true, Sources: names}},
	}
	for _, n := range names {
		cfg.Sources[n] = SourceConfig{Enabled: true, Official: n != "eastmoney", Timeout: time.Second}
	}
	p, warn := NewPipeline(cfg, srcs, nil)
	if len(warn) > 0 {
		t.Fatalf("warnings %v", warn)
	}
	return p
}

func TestPipelineFailoverAndRecover(t *testing.T) {
	now := time.Date(2026, 10, 8, 10, 0, 0, 0, shanghai())
	good := Snapshot{Symbol: "600519.SH", Time: now, Last: 1500, High: 1510, Low: 1490, Volume: 1}
	a := &fakeSnap{name: SourceTdxLocal, err: errors.New("down")}
	b := &fakeSnap{name: SourceEastmoney, items: []Snapshot{good, good, good}}
	p := testPipe(t, []Source{a, b}, SourceTdxLocal, SourceEastmoney)
	p.now = func() time.Time { return now }

	got, src, err := Run(context.Background(), p, DomainSnapshot, RunOptions{},
		func(ctx context.Context, s Source) (*SnapshotBatch, error) {
			x, err := s.(SnapshotSource).Snapshots(ctx, nil)
			return &x, err
		},
		func(v *SnapshotBatch) error {
			if len(v.Items) == 0 {
				return errors.New("empty")
			}
			return nil
		})
	if err != nil || src != SourceEastmoney || len(got.Items) != 3 {
		t.Fatalf("got src=%s n=%d err=%v", src, len(got.Items), err)
	}

	// 主源连续失败 3 次后断路，第四次不再调用。
	a.err = errors.New("still down")
	for i := 0; i < 3; i++ {
		_, _, _ = Run(context.Background(), p, DomainSnapshot, RunOptions{},
			func(ctx context.Context, s Source) (*SnapshotBatch, error) {
				x, err := s.(SnapshotSource).Snapshots(ctx, nil)
				return &x, err
			}, nil)
	}
	calls := a.calls
	_, _, _ = Run(context.Background(), p, DomainSnapshot, RunOptions{},
		func(ctx context.Context, s Source) (*SnapshotBatch, error) {
			x, err := s.(SnapshotSource).Snapshots(ctx, nil)
			return &x, err
		}, nil)
	if a.calls != calls {
		t.Fatalf("open breaker still called primary, calls %d -> %d", calls, a.calls)
	}

	h := p.Health()
	var primary Health
	for _, row := range h {
		if row.Source == SourceTdxLocal {
			primary = row
		}
	}
	if primary.State != StateOpen {
		t.Fatalf("primary state %s", primary.State)
	}
}

func TestPipelineSkipUnofficial(t *testing.T) {
	now := time.Date(2026, 10, 8, 10, 0, 0, 0, shanghai())
	web := &fakeSnap{name: SourceEastmoney, items: []Snapshot{{Symbol: "600519.SH", Time: now, Last: 1}}}
	cfg := Config{
		AllowUnofficial: false,
		Sources:         map[string]SourceConfig{SourceEastmoney: {Enabled: true, Official: false, Timeout: time.Second}},
		Domains:         map[string]DomainConfig{DomainSnapshot: {Enabled: true, Sources: []string{SourceEastmoney}}},
	}
	p, _ := NewPipeline(cfg, []Source{web}, nil)
	_, _, err := Run(context.Background(), p, DomainSnapshot, RunOptions{},
		func(ctx context.Context, s Source) (*SnapshotBatch, error) {
			x, err := s.(SnapshotSource).Snapshots(ctx, nil)
			return &x, err
		}, nil)
	var ns *NoSourceError
	if !errors.As(err, &ns) {
		t.Fatalf("want NoSourceError, got %v", err)
	}
	if web.calls != 0 {
		t.Fatalf("unofficial source was called")
	}
}

func TestPipelineUnsupportedNotFailure(t *testing.T) {
	skip := &fakeSnap{name: SourceAkshare, err: ErrUnsupported}
	ok := &fakeSnap{name: SourceTdxFile, items: []Snapshot{{Symbol: "000001.SZ", Time: time.Now(), Last: 10}}}
	p := testPipe(t, []Source{skip, ok}, SourceAkshare, SourceTdxFile)
	_, src, err := Run(context.Background(), p, DomainSnapshot, RunOptions{},
		func(ctx context.Context, s Source) (*SnapshotBatch, error) {
			x, err := s.(SnapshotSource).Snapshots(ctx, nil)
			return &x, err
		}, nil)
	if err != nil || src != SourceTdxFile {
		t.Fatalf("src=%s err=%v", src, err)
	}
	h := p.Health()
	for _, row := range h {
		if row.Source == SourceAkshare && row.Failure != 0 {
			t.Fatalf("unsupported counted as failure: %+v", row)
		}
	}
}
