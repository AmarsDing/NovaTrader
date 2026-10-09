package data

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"server/ent"
	"server/pkg/dbinit"
	"server/pkg/market"
	"server/pkg/migrate"
	"server/pkg/tradecal"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// 集成测试用独立的 novatrader_m02test 库，本机没有 PostgreSQL 时跳过。
func openTestDB(t *testing.T) *ent.Client {
	t.Helper()
	ctx := context.Background()
	cfg := dbinit.Config{
		Host: "127.0.0.1", Port: 5432, User: "postgres", Password: "postgres",
		Database: "novatrader_m02test", SSLMode: "disable", Timeout: 3 * time.Second,
	}
	if err := dbinit.EnsureDatabase(ctx, cfg); err != nil {
		t.Skip(err)
	}
	if err := dbinit.EnsureExtension(ctx, cfg, "vector"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := migrate.Schema(ctx, cfg); err != nil {
			t.Fatalf("migrate run %d: %v", i+1, err)
		}
	}
	dsn, err := dbinit.BusinessDSN(cfg)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	// 会话时区设成 UTC，验证 date 列不随连接时区偏移一天。
	if _, err := db.ExecContext(ctx, "SET TIME ZONE 'UTC'"); err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { client.Close() })
	for _, del := range []func() (int, error){
		func() (int, error) { return client.SeatTag.Delete().Exec(ctx) },
		func() (int, error) { return client.OverseasMapping.Delete().Exec(ctx) },
		func() (int, error) { return client.StockFactor.Delete().Exec(ctx) },
		func() (int, error) { return client.LimitBoard.Delete().Exec(ctx) },
		func() (int, error) { return client.LimitPool.Delete().Exec(ctx) },
		func() (int, error) { return client.MarketSentiment.Delete().Exec(ctx) },
		func() (int, error) { return client.MarketData.Delete().Exec(ctx) },
		func() (int, error) { return client.SectorMember.Delete().Exec(ctx) },
		func() (int, error) { return client.Sector.Delete().Exec(ctx) },
		func() (int, error) { return client.SectorHeat.Delete().Exec(ctx) },
		func() (int, error) { return client.MoneyFlow.Delete().Exec(ctx) },
	} {
		if _, err := del(); err != nil {
			t.Fatal(err)
		}
	}
	return client
}

func at(day time.Time, hh, mm int) time.Time {
	return day.Add(time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute)
}

var testDay = time.Date(2026, 10, 9, 0, 0, 0, 0, tradecal.Shanghai())

func TestFactorsPointInTimeAndUpsert(t *testing.T) {
	client := openTestDB(t)
	r := NewRepo(client)
	ctx := context.Background()
	t10, t1130 := at(testDay, 10, 0), at(testDay, 11, 31)
	save := func(asOf time.Time, v float64) {
		t.Helper()
		rows := []market.FactorRow{{Symbol: "600519.SH", AsOf: asOf, Values: market.Values{"ma5": v, "bad": nan()}}}
		if err := r.SaveFactors(ctx, testDay, "intraday", rows); err != nil {
			t.Fatal(err)
		}
	}
	save(t10, 1)
	save(t10, 2)
	save(t1130, 3)
	got, err := r.FactorsAt(ctx, testDay, "intraday", at(testDay, 11, 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Values["ma5"] != 2 || !got[0].AsOf.Equal(t10) {
		t.Fatalf("as of 11:00 = %+v, want the re-saved 10:00 row", got)
	}
	if _, ok := got[0].Values["bad"]; ok {
		t.Fatal("NaN must be dropped before JSON")
	}
	got, err = r.FactorsAt(ctx, testDay, "", at(testDay, 15, 0), []string{"600519.SH"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Values["ma5"] != 3 {
		t.Fatalf("as of 15:00 = %+v", got)
	}
	got, _ = r.FactorsAt(ctx, testDay.AddDate(0, 0, -1), "", at(testDay, 15, 0), nil)
	if len(got) != 0 {
		t.Fatalf("previous day must be empty, got %d rows", len(got))
	}
}

func nan() float64 {
	var z float64
	return z / z
}

func TestBoardsSentimentAndDates(t *testing.T) {
	client := openTestDB(t)
	r := NewRepo(client)
	ctx := context.Background()
	prev := testDay.AddDate(0, 0, -1)

	if err := client.LimitPool.Create().SetTradeDate(pgDay(prev)).SetSymbol("000001.SZ").SetPool("up").SetConsecutive(3).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	sealed, err := r.SealedOn(ctx, prev)
	if err != nil || sealed["000001.SZ"] != 3 {
		t.Fatalf("limit_pool fallback = %v, %v", sealed, err)
	}

	b := market.LimitBoard{
		Symbol: "600519.SH", TradeDate: prev, Direction: market.DirUp, Status: market.StatusSealed,
		LimitPrice: 11, FirstSealAt: at(prev, 9, 35), Consecutive: 2, AsOf: at(prev, 15, 0),
	}
	for i := 0; i < 2; i++ {
		if err := r.SaveBoards(ctx, []market.LimitBoard{b}); err != nil {
			t.Fatal(err)
		}
	}
	sealed, err = r.SealedOn(ctx, prev)
	if err != nil || len(sealed) != 1 || sealed["600519.SH"] != 2 {
		t.Fatalf("limit_board must win over limit_pool: %v, %v", sealed, err)
	}
	boards, err := r.Boards(ctx, prev, market.DirUp, "")
	if err != nil || len(boards) != 1 {
		t.Fatalf("boards = %v, %v", boards, err)
	}
	if !boards[0].TradeDate.Equal(prev) || !boards[0].FirstSealAt.Equal(b.FirstSealAt) {
		t.Fatalf("round trip: %+v", boards[0])
	}

	s := market.Sentiment{TradeDate: prev, AsOf: at(prev, 15, 0), UpCount: 50, Phase: market.PhaseHot, ScoreCoef: 1.05}
	for i := 0; i < 2; i++ {
		if err := r.SaveSentiment(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	phase, err := r.ClosePhase(ctx, prev)
	if err != nil || phase != market.PhaseHot {
		t.Fatalf("close phase = %q, %v", phase, err)
	}
	got, ok, err := r.SentimentAt(ctx, prev, at(prev, 14, 0))
	if err != nil || ok {
		t.Fatalf("sentiment before 15:00 must be absent: %+v %v %v", got, ok, err)
	}
	got, ok, err = r.SentimentAt(ctx, prev, at(prev, 23, 0))
	if err != nil || !ok || got.UpCount != 50 || !got.TradeDate.Equal(prev) {
		t.Fatalf("sentiment = %+v %v %v", got, ok, err)
	}
}

func TestDailyBarsKeepUpstream(t *testing.T) {
	client := openTestDB(t)
	r := NewRepo(client)
	ctx := context.Background()
	upstream := client.MarketData.Create().SetSymbol("600519.SH").SetBarTime(testDay).SetFreq("1d").
		SetOpen(10).SetHigh(11).SetLow(9).SetClose(10.5).SetVolume(100).SetAmount(1000).SetSource("tdx")
	if err := upstream.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	ours := []market.DayBar{
		{Symbol: "600519.SH", Time: testDay, Open: 1, High: 1, Low: 1, Close: 1, Adj: 1},
		{Symbol: "000001.SZ", Time: testDay, Open: 5, High: 6, Low: 4, Close: 5.5, PreClose: 5, Volume: 10, Amount: 55, Adj: 1},
	}
	if err := r.SaveDailyIfAbsent(ctx, ours); err != nil {
		t.Fatal(err)
	}
	amounts, err := r.DayAmounts(ctx, testDay)
	if err != nil {
		t.Fatal(err)
	}
	if amounts["600519.SH"] != 1000 || amounts["000001.SZ"] != 55 {
		t.Fatalf("amounts = %v", amounts)
	}
	hist, err := r.DailyHistory(ctx, testDay.AddDate(0, 0, 1), 10)
	if err != nil {
		t.Fatal(err)
	}
	_ = hist // stock_basic 为空时只验证不报错

	bars := []market.Bar{
		{Symbol: "000001.SZ", Time: at(testDay, 9, 30), Open: 5, High: 5, Low: 5, Close: 5, Volume: 1, Amount: 5},
		{Symbol: "000001.SZ", Time: at(testDay, 9, 31), Open: 5, High: 6, Low: 5, Close: 6, Volume: 1, Amount: 6},
		{Symbol: "000001.SZ", Time: at(testDay, 9, 32), Open: 6, High: 6, Low: 6, Close: 6, Volume: 1, Amount: 6},
	}
	for i := 0; i < 2; i++ {
		if err := r.SaveMinuteBars(ctx, bars, map[string]float64{"000001.SZ": 2}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := r.Bars(ctx, "000001.SZ", "1m", time.Time{}, time.Time{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || !rows[0].Time.Equal(bars[1].Time) || !rows[1].Time.Equal(bars[2].Time) || rows[0].Adj != 2 {
		t.Fatalf("bars = %+v", rows)
	}
}

func TestSectorsAndFlows(t *testing.T) {
	client := openTestDB(t)
	r := NewRepo(client)
	ctx := context.Background()
	if err := client.Sector.Create().SetCode("BK1").SetName("白酒").SetKind("industry").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	old := pgDay(testDay.AddDate(0, 0, -30))
	if err := client.SectorMember.Create().SetSectorCode("BK1").SetSymbol("600519.SH").SetInDate(old).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.SectorMember.Create().SetSectorCode("BK1").SetSymbol("000858.SZ").SetInDate(old).SetOutDate(pgDay(testDay)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	members, names, err := r.SectorMembers(ctx, testDay)
	if err != nil {
		t.Fatal(err)
	}
	if len(members["BK1"]) != 1 || members["BK1"][0] != "600519.SH" || names["BK1"] != "白酒" {
		t.Fatalf("members = %v names = %v", members, names)
	}

	heat := []market.SectorHeat{{SectorCode: "BK1", Rank: 1, Heat: 3}}
	if err := r.SaveSectorHeat(ctx, testDay, at(testDay, 10, 0), heat); err != nil {
		t.Fatal(err)
	}
	heat[0].Heat = 5
	if err := r.SaveSectorHeat(ctx, testDay, at(testDay, 14, 30), heat); err != nil {
		t.Fatal(err)
	}
	got, err := r.SectorHeatAt(ctx, testDay, at(testDay, 11, 0), 10)
	if err != nil || len(got) != 1 || got[0].Heat != 3 {
		t.Fatalf("heat as of 11:00 = %+v %v", got, err)
	}

	prev := pgDay(testDay.AddDate(0, 0, -1))
	for _, f := range []struct {
		sym string
		day time.Time
		net float64
	}{{"600519.SH", prev, 1}, {"600519.SH", pgDay(testDay), 2}, {"000001.SZ", prev, 9}} {
		if err := client.MoneyFlow.Create().SetSymbol(f.sym).SetTradeDate(f.day).SetMainNet(f.net).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	flows, err := r.MoneyFlows(ctx, testDay, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(flows) != 1 || len(flows["600519.SH"]) != 2 || flows["600519.SH"][1].MainNet != 2 {
		t.Fatalf("flows = %v", flows)
	}
}

func TestEnsureDefaultsKeepsEdits(t *testing.T) {
	client := openTestDB(t)
	r := NewRepo(client)
	ctx := context.Background()
	if err := r.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	tags, err := r.SeatTags(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tags["机构专用"] != market.SeatInstitution || tags["沪股通专用"] != market.SeatNorth || len(tags) != len(defaultSeats) {
		t.Fatalf("tags = %v", tags)
	}
	maps, err := r.OverseasMappings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(maps) != len(defaultMappings) {
		t.Fatalf("mappings = %+v", maps)
	}
	n, err := client.OverseasMapping.Update().
		SetWeight(9).
		Save(ctx)
	if err != nil || n != len(defaultMappings) {
		t.Fatalf("update weight: %d %v", n, err)
	}
	if err := r.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	maps, err = r.OverseasMappings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range maps {
		if m.Weight != 9 {
			t.Fatalf("edited weight was overwritten: %+v", m)
		}
	}
}

func TestMinuteBars5000Write(t *testing.T) {
	client := openTestDB(t)
	r := NewRepo(client)
	ctx := context.Background()
	bars := make([]market.Bar, 5000)
	adj := make(map[string]float64, len(bars))
	ts := at(testDay, 9, 30)
	for i := range bars {
		sym := fmt.Sprintf("%06d.SZ", i+1)
		bars[i] = market.Bar{Symbol: sym, Time: ts, Open: 10, High: 10, Low: 10, Close: 10, Volume: 100, Amount: 1000}
		adj[sym] = 1
	}
	start := time.Now()
	if err := r.SaveMinuteBars(ctx, bars, adj); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	t.Logf("5000 minute bars: %s", elapsed)
	if elapsed > 30*time.Second {
		t.Fatalf("write took %s", elapsed)
	}
}
