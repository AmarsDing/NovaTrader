package data

import "testing"

func TestParseSina(t *testing.T) {
	body := `var hq_str_sh600519="贵州茅台,1252.900,1258.620,1255.790,1258.000,1242.000,1255.410,1255.790,2516757,3145413157.000,1000,1255.410,300,1255.360,300,1255.100,100,1255.080,1500,1255.000,177,1255.790,500,1255.800,100,1255.930,800,1255.960,2000,1255.980,2026-10-08,15:34:59,00,D|500|627895.00"; var hq_str_sh600002="";`
	items := parseSina(body)
	if len(items) != 1 {
		t.Fatalf("got %d items", len(items))
	}
	s := items[0]
	if s.Symbol != "600519.SH" || s.Last != 1255.79 || s.PreClose != 1258.62 || s.Volume != 2516757 || s.Amount != 3145413157 {
		t.Fatalf("bad snapshot %+v", s)
	}
	if s.Bid[0] != [2]float64{1255.41, 1000} || s.Ask[0] != [2]float64{1255.79, 177} {
		t.Fatalf("bad book bid=%v ask=%v", s.Bid[0], s.Ask[0])
	}
	if s.Time.Format("2006-01-02 15:04:05") != "2026-10-08 15:34:59" {
		t.Fatalf("bad time %v", s.Time)
	}
}

func TestParseTencent(t *testing.T) {
	body := `v_sh600519="1~贵州茅台~600519~1255.79~1258.62~1252.90~25168~12953~12215~1255.41~10~1255.36~3~1255.10~3~1255.08~1~1255.00~15~1255.79~2~1255.80~5~1255.93~1~1255.96~8~1255.98~20~~20261008161457~-2.83~-0.22~1258.00~1242.00~1255.79/25168/3145413157~25168~314541~0.20";`
	items, err := parseTencent(body)
	if err != nil || len(items) != 1 {
		t.Fatalf("got %d items err=%v", len(items), err)
	}
	s := items[0]
	if s.Symbol != "600519.SH" || s.Open != 1252.9 || s.High != 1258 || s.Low != 1242 || s.Volume != 2516800 || s.Amount != 3145413157 {
		t.Fatalf("bad snapshot %+v", s)
	}
	if s.Bid[0] != [2]float64{1255.41, 1000} || s.Ask[0] != [2]float64{1255.79, 200} {
		t.Fatalf("bad book bid=%v ask=%v", s.Bid[0], s.Ask[0])
	}
}
