package pinyin

import "testing"

func TestInitials(t *testing.T) {
	cases := map[string]string{
		"贵州茅台":   "gzmt",
		"万科A":    "wka",
		"中芯国际":   "zxgj",
		"ST曙光":   "stsg",
		"宁德时代":   "ndsd",
		"600519": "",
		"ab":     "ab",
		"":       "",
		// 多音字取一级字库主排序位：「银行」的行落 xíng 区——命中错位是已声明口径，
		// 钉死防未来被误“修正”回另一读音；名称子串通道仍可搜到。
		"平安银行": "payx",
	}
	for in, want := range cases {
		if got := Initials(in); got != want {
			t.Errorf("Initials(%q)=%q want %q", in, got, want)
		}
	}
}

// 二级字库之外的生僻字不猜读音：返回空串由调用方跳过，不影响其他通道。
func TestInitialsUnknownHanziSkipped(t *testing.T) {
	got := Initials("𠮷垚")
	if got != "" {
		t.Fatalf("rare hanzi should not be guessed: %q", got)
	}
}
