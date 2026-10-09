package backtest

import "strconv"

func pct(v float64) string  { return strconv.FormatFloat(v*100, 'f', -1, 64) + "%" }
func bp(v float64) string   { return "万" + strconv.FormatFloat(v*10000, 'f', -1, 64) }
func ftoa(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
func itoa(v int) string     { return strconv.Itoa(v) }
