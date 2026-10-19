package scheduler

import (
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// FR-00-03 cron 子语法与 Previous（漏跑补跑的回看窗）的包级钉死。
// scheduler_test.go 已钉仅交易日/依赖/重试/补跑去重；本文件钉 cron 五段语法本体：
// 段数与取值域、步进展开、dow 7≡0、dom/dow 双限取 OR、分钟截断与 48 小时回看界。

func TestCronSpecFieldCount(t *testing.T) {
	for _, spec := range []string{"", "* * * *", "* * * * * *", "0 9"} {
		if _, err := parseSpec(spec); err == nil || !strings.Contains(err.Error(), "5 cron fields") {
			t.Fatalf("spec %q: %v", spec, err)
		}
	}
}

func TestCronFieldRanges(t *testing.T) {
	// 五段取值域：分 0-59、时 0-23、日 1-31、月 1-12、周 0-7（cron.go :20）。
	for _, spec := range []string{
		"60 * * * *", "-1 * * * *",
		"* 24 * * *",
		"* * 0 * *", "* * 32 * *",
		"* * * 0 *", "* * * 13 *",
		"* * * * 8",
		"10-5 * * * *", // 反向区间
	} {
		if _, err := parseSpec(spec); err == nil || !strings.Contains(err.Error(), "bad field") {
			t.Fatalf("spec %q: %v", spec, err)
		}
	}
	// 步进必须是正整数。
	for _, spec := range []string{"*/0 * * * *", "*/x * * * *", "*/-2 * * * *"} {
		if _, err := parseSpec(spec); err == nil || !strings.Contains(err.Error(), "bad step") {
			t.Fatalf("spec %q: %v", spec, err)
		}
	}
	// 不支持英文名字（JAN/MON），纯数字口径。
	if _, err := parseSpec("0 9 * * MON"); err == nil || !strings.Contains(err.Error(), "bad field") {
		t.Fatalf("names unsupported: %v", err)
	}
}

func TestCronStepExpansion(t *testing.T) {
	has := func(f field, vals ...int) bool {
		if len(f.vals) != len(vals) {
			return false
		}
		for _, v := range vals {
			if !f.vals[v] {
				return false
			}
		}
		return true
	}
	// */N 从段下限起步（分：0-59）。
	p, err := parseSpec("*/15 * * * *")
	if err != nil || !has(p[0], 0, 15, 30, 45) {
		t.Fatalf("*/15: %v %v", err, p[0].vals)
	}
	// M-N/S 从 M 起步、不越 N。
	p2, err := parseSpec("1-10/2 * * * *")
	if err != nil || !has(p2[0], 1, 3, 5, 7, 9) {
		t.Fatalf("1-10/2: %v %v", err, p2[0].vals)
	}
	// 单值带步进只命中自身（5 被当 5-5，步进无从展开——代码口径，POSIX 未定义此形）。
	p3, err := parseSpec("5/15 * * * *")
	if err != nil || !has(p3[0], 5) {
		t.Fatalf("5/15: %v %v", err, p3[0].vals)
	}
	// 逗号列表。
	p4, err := parseSpec("0 9 1,15,31 * *")
	if err != nil || !has(p4[2], 1, 15, 31) {
		t.Fatalf("list: %v %v", err, p4[2].vals)
	}
}

func TestCronDow7FoldsTo0(t *testing.T) {
	// 周字段 7 与 0 同义（周日）；折叠进集合，不保留 7。
	// 锁的是解析后的内部表示（重一种合法实现）；行为合同（0/7 都命中周日）
	// 由 TestCronSundayZeroAndSeven 钉，两处各司其职。
	p, err := parseSpec("0 9 * * 7")
	if err != nil || len(p[4].vals) != 1 || !p[4].vals[0] {
		t.Fatalf("dow 7 must fold to 0: %v %v", err, p[4].vals)
	}
	p2, err := parseSpec("0 9 * * 0,7")
	if err != nil || len(p2[4].vals) != 1 || !p2[4].vals[0] {
		t.Fatalf("0,7 must dedupe to {0}: %v %v", err, p2[4].vals)
	}
}

func mustMatch(t *testing.T, spec string, at string, want bool) {
	t.Helper()
	p, err := parseSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	if got := match(p, sh(at)); got != want {
		t.Fatalf("match(%q, %s) = %v, want %v", spec, at, got, want)
	}
}

func TestCronDomDowDoubleRestrictedIsOr(t *testing.T) {
	// POSIX cron：日与周都受限时取 OR（cron.go :80-85）。
	// 2026-10-01 周四（1 号）、10-09 周五（9 号）、10-08 周四（8 号）。
	mustMatch(t, "0 9 1 * 5", "2026-10-01 09:00:00", true)  // 只有日命中
	mustMatch(t, "0 9 1 * 5", "2026-10-09 09:00:00", true)  // 只有周命中
	mustMatch(t, "0 9 1 * 5", "2026-10-08 09:00:00", false) // 都不命中
	// 只限一段时是严格 AND：日段受限、周段不限 → 只看日。
	mustMatch(t, "0 9 1 * *", "2026-10-01 09:00:00", true)
	mustMatch(t, "0 9 1 * *", "2026-10-15 09:00:00", false)
	// 时、分、月段也进 AND 链。
	mustMatch(t, "0 9 * 2 *", "2026-02-02 09:00:00", true)
	mustMatch(t, "0 9 * 2 *", "2026-03-02 09:00:00", false)
	mustMatch(t, "0 9 * * *", "2026-02-02 09:01:00", false)
	mustMatch(t, "0 9 * * *", "2026-02-02 10:00:00", false)
}

func TestCronSundayZeroAndSeven(t *testing.T) {
	// 2026-10-11 周日、10-17 周六。
	mustMatch(t, "0 9 * * 0", "2026-10-11 09:00:00", true)
	mustMatch(t, "0 9 * * 7", "2026-10-11 09:00:00", true)
	mustMatch(t, "0 9 * * 6", "2026-10-11 09:00:00", false)
	mustMatch(t, "0 9 * * 6", "2026-10-17 09:00:00", true)
}

func TestPreviousTruncatesToMinute(t *testing.T) {
	// 精度到分钟：秒被截掉，返回分钟边界（cron.go :94）；now 正落槽位时返回 now 自身。
	got, err := Previous("5 10 * * *", sh("2026-10-19 10:05:37"))
	if err != nil || !got.Equal(sh("2026-10-19 10:05:00")) {
		t.Fatalf("truncate: %v %v", got, err)
	}
	got2, err := Previous("5 10 * * *", sh("2026-10-19 10:05:00"))
	if err != nil || !got2.Equal(sh("2026-10-19 10:05:00")) {
		t.Fatalf("on the slot returns now itself: %v %v", got2, err)
	}
	// 跨午夜回看。
	got3, err := Previous("58 23 * * *", sh("2026-10-19 00:03:00"))
	if err != nil || !got3.Equal(sh("2026-10-18 23:58:00")) {
		t.Fatalf("across midnight: %v %v", got3, err)
	}
}

func TestPreviousFortyEightHourBound(t *testing.T) {
	// 最多回看 48 小时（cron.go :95-102），回看窗含恰在 48h 前的那个分钟。
	now := sh("2026-10-19 12:34:56") // 截到 12:34；回看界 = 10-17 12:34
	got, err := Previous("34 12 17 10 *", now)
	if err != nil || !got.Equal(sh("2026-10-17 12:34:00")) {
		t.Fatalf("limit minute is in window: %v %v", got, err)
	}
	// 早一分钟出窗 → 报错。
	if _, err := Previous("33 12 17 10 *", now); err == nil || !strings.Contains(err.Error(), "no slot within 48h") {
		t.Fatalf("one minute past the bound: %v", err)
	}
	// 稀有槽位 48h 内永远不在 → 报错（漏跑补跑窗口的天然上限）。
	if _, err := Previous("0 0 29 2 *", now); err == nil {
		t.Fatal("Feb 29 not within 48h must error")
	}
}

// Previous 不自行转时区：按调用方给的时钟字段匹配（调度主循环以上海本地时刻喂入）。
func TestPreviousUsesCallerClock(t *testing.T) {
	utc := time.Date(2026, 10, 19, 1, 5, 0, 0, time.UTC) // 上海 09:05
	got, err := Previous("5 1 * * *", utc)
	if err != nil || !got.Equal(utc) {
		t.Fatalf("caller clock semantics: %v %v", got, err)
	}
}

// 本实现与 Vixie cron 的方言差异（cron.go 解析处也有注释）：钉死现状口径，
// 换解析器或重写 parseField 时这里必须红，不能静默改变运维配置的命中集合。
func TestCronCommaStepDialect(t *testing.T) {
	vals := func(f field) []int {
		var out []int
		for v, on := range f.vals {
			if on {
				out = append(out, v)
			}
		}
		sort.Ints(out)
		return out
	}
	// 步进写在逗号清单末尾、施于清单的每一段（第一个 "/" 处切分）：
	// Vixie 逐段绑定会得到 {1..10,15,17,19}，本实现是每一段都按 2 步进。
	p, err := parseSpec("1-10,15-20/2 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	if got := vals(p[0]); !reflect.DeepEqual(got, []int{1, 3, 5, 7, 9, 15, 17, 19}) {
		t.Fatalf("comma-step applies to every segment: %v", got)
	}
	// 反向形态在 Vixie 合法，本实现把「2,15-20」整个当步进串 → bad step 拒绝。
	if _, err := parseSpec("1-10/2,15-20 * * * *"); err == nil || !strings.Contains(err.Error(), "bad step") {
		t.Fatalf("step-with-tail must be rejected: %v", err)
	}
	// "*/N" 落在 dom/dow 时记为受限段（any=false），参与双限取 OR；
	// Vixie 视 star 带步进仍为非受限、走 AND——口径分歧处钉本实现的行为。
	// 2026-10-02 周五（dow 命中）、2 号不在 {1,3,…,31}（dom 不命中）→ OR 真（Vixie AND 会假）。
	mustMatch(t, "0 9 */2 * 5", "2026-10-02 09:00:00", true)
	// 双边不命中：2026-10-06 周二、6 号偶数 → false。
	mustMatch(t, "0 9 */2 * 5", "2026-10-06 09:00:00", false)
	// 单边 dom 命中照常放行（正向对照，防止全部误判）。
	mustMatch(t, "0 9 */2 * 5", "2026-10-07 09:00:00", true)
}
