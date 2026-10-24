// Package pinyin 提取汉字的拼音首字母，用于证券简称的拼音联想。
//
// 覆盖口径（诚实边界）：基于 x/text GBK 编码 + GB2312 一级字库的分区规律。
// 一级字库（B0A1-D7F9）按拼音排序，可直接映射首字母；二级字库（D8 起）
// 按部首排序无法定位读音，非 GBK 编码字符（生僻字）同样无法映射——返回空串
// 由调用方显式跳过（不影响 code/名称子串两条命中通道）。多音字取一级字库的
// 第一个读音，命中错位属已知口径。A 股简称用字以一级字库为主，实测覆盖良好。
package pinyin

import (
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// 一级字库分区表：pos = (高字节-0xB0)*94+(低字节-0xA1)，按拼音排序的经典分界
// （a 区起 B0A1=0，z 区起 D4D1=3432；一级字库共 3755 字，无 v 区）。
var zoneInitials = []struct {
	from    int
	initial byte
}{
	{0, 'a'},    // B0A1 啊
	{36, 'b'},   // B0C5 芭
	{220, 'c'},  // B2C1 擦
	{453, 'd'},  // B4EE 哒
	{637, 'e'},  // B6EA 妸
	{659, 'f'},  // B7A2 发
	{784, 'g'},  // B8C1 噶
	{939, 'h'},  // B9FE 哈
	{1119, 'j'}, // BBF6 讥
	{1410, 'k'}, // BFA1 咔
	{1515, 'l'}, // C0AC 垃
	{1763, 'm'}, // C2E8 唛
	{1914, 'n'}, // C4C3 拿
	{1995, 'o'}, // C5B6 哦
	{2003, 'p'}, // C5BE 啪
	{2125, 'q'}, // C6DA 期
	{2282, 'r'}, // C8BB 亽
	{2341, 's'}, // C8F6 卅
	{2627, 't'}, // CBFA 塌
	{2783, 'w'}, // CDDA 屲
	{2903, 'x'}, // CEF4 昔
	{3126, 'y'}, // D1B9 压
	{3432, 'z'}, // D4D1 匝
}

// Initials 返回字符串中各汉字字符的拼音首字母（小写），不可映射的字符跳过。
// ASCII 字母原样小写返回，数字与符号跳过。
func Initials(s string) string {
	out := make([]byte, 0, len(s))
	enc := simplifiedchinese.GBK.NewEncoder()
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			out = append(out, byte(r-'A'+'a'))
		case r >= 'a' && r <= 'z':
			out = append(out, byte(r))
		case r < utf8.RuneSelf:
			continue // 数字、标点
		default:
			if b := hanziInitial(r, enc); b != 0 {
				out = append(out, b)
			}
		}
	}
	return string(out)
}

func hanziInitial(r rune, enc *encoding.Encoder) byte {
	gbk, err := enc.String(string(r))
	if err != nil || len(gbk) != 2 {
		return 0
	}
	hi, lo := int(gbk[0]), int(gbk[1])
	// 一级字库 B0A1–D7F9；之外（二级字库 D8 起、用户区）不猜读音。
	if hi < 0xB0 || hi > 0xD7 || lo < 0xA1 || lo > 0xFE {
		return 0
	}
	pos := (hi-0xB0)*94 + (lo - 0xA1)
	initial := byte(0)
	for _, zf := range zoneInitials {
		if pos >= zf.from {
			initial = zf.initial
		} else {
			break
		}
	}
	return initial
}
