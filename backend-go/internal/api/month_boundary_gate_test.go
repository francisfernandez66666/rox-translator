// ============================================================================
// month_boundary_gate_test.go · 职责说明
// 「日历边界只许有一份」的**反重复闸门**（★ 2026-10-01 〇-AF 补丁三建立）。
//
// 这条锁要挡的东西不是某个数字算错，而是"同一个边界被抄成两份"这个形态：
// usage_ledger.created_at 在本仓一律以 UTC 写入（TEXT 列），谓词 created_at>=?
// 是**字典序**比较 ⇒ 边界必须与写入口径同区。本仓曾经同时存在两份"本月起点"：
// store 里的 monthStart()（供部门/组织预算墙）与 api 层内联的一份（供收银台/订阅页出数）。
// 两处都带同一个"本地时区渲染"的洞，于是修 store 那一份、页面那一份照旧错：
// 每月 1 号偏移量那几个小时里，客户的「今日已用／本月已用」恒读 0，
// 主矩阵 A7s 与前端 E2E TF2 当场判红（本批真踩）。
//
// 与 AGENTS §一·11「谓词只许引用 store 里那一份常量」同一条纪律，对象换成了日历。
// 判据是**派生式**的（扫全部非测试 .go），不是写死文件清单——新文件里抄一份立刻进射程。
//
// 豁免档（写清楚，别靠人记）：internal/infra/ratelimit/daily.go 现算的是
// 「本地零点 + 24h」这个**时长终点**（滑动窗口的重置时刻），不拿去和 TEXT 戳记做字典序比较，
// 不在本条射程内；该包若哪天改成渲染成字符串去比 created_at，由它自己的 daily_test.go 钉。
//
// 反向验证（本批实做过，不是声称）：
//  1. 在 internal/api 临时放一份内联月边界 ⇒ 判红（复制形态被抓）；
//  2. 把 store 那一份的 .UTC() 剥掉 ⇒ 判红（单一事实源自己退化也被抓）；
//  3. 把单一事实源整个删掉 ⇒ 判红（正控：闸门不允许靠"没有命中"恒绿）。
//
// ============================================================================
package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// 内联月/日边界的构造形态。只看代码行：**注释行按口径跳过**——
// 说明注释里写「禁止现算边界」是合法的，把它算进命中数就是计数锁自伤
// （AGENTS §一·2 日志棘轮同族：跳过 // 行）。
var (
	reInlineMonthBoundary = regexp.MustCompile(`Month\(\)\s*,\s*1\s*,\s*0\s*,\s*0\s*,\s*0\s*,\s*0`)
	reInlineDayBoundary   = regexp.MustCompile(`Day\(\)\s*,\s*0\s*,\s*0\s*,\s*0\s*,\s*0`)
)

const (
	boundaryOwnerFile  = "quota_org.go" // 唯一被允许的落点（文件名）
	boundaryOwnerDir   = "store"        // 唯一被允许的目录
	boundaryOwnerCount = 2              // 月 + 日，两个出口
	// ratelimitExemptPrefix 见文件头「豁免档」：算 TTL 终点，不拿去比 TEXT 戳记。
	ratelimitExemptPrefix = "infra" + string(filepath.Separator) + "ratelimit" + string(filepath.Separator)
)

// readNonTestGoCodeLines 从包目录（backend-go/internal/api）往上定位 internal 根，
// 扫全部非测试 .go 的代码行。找不到根 / 扫到 0 个文件一律 t.Fatal：
// 「静默扫不到＝恒绿空转闸门」是 AGENTS §二 那条"喂目录得 0"的同族坑。
func readNonTestGoCodeLines(t *testing.T) map[string][]string {
	t.Helper()
	root := ""
	for p := "."; ; {
		if st, err := os.Stat(filepath.Join(p, "api")); err == nil && st.IsDir() {
			if st2, err2 := os.Stat(filepath.Join(p, "store")); err2 == nil && st2.IsDir() {
				root = p
				break
			}
		}
		abs, err := filepath.Abs(p)
		if err != nil || abs == filepath.Dir(abs) {
			t.Fatal("找不到 internal 根目录（须同时含 api/ 与 store/）⇒ 扫描面为空，闸门不许恒绿")
		}
		p = filepath.Join(abs, "..")
	}
	out := map[string][]string{}
	scanned := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		scanned++
		var code []string
		for _, ln := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(ln), "//") {
				continue
			}
			code = append(code, ln)
		}
		rel, _ := filepath.Rel(root, path)
		out[rel] = code
		return nil
	})
	if err != nil {
		t.Fatalf("遍历 internal 源码失败: %v", err)
	}
	// 扫描面自证（不钉总数——总数是会随拆包漂移的脆锚）：三个一定存在的落点必须被看到，
	// 否则"零命中"是扫不到文件造成的假绿，而不是"确实没有第二份边界"。
	for _, must := range []string{
		filepath.Join("api", "plans_api.go"),
		filepath.Join("store", "quota_org.go"),
		filepath.Join("store", "billing.go"),
	} {
		if _, ok := out[must]; !ok {
			t.Fatalf("扫描面缺 %s（共扫到 %d 个非测试 .go）⇒ 本闸门的「零命中」没有意义", must, scanned)
		}
	}
	if scanned < 100 {
		t.Fatalf("扫描文件数异常偏少（%d）⇒ 扫描面没落到仓库，闸门无效", scanned)
	}
	return out
}

// TestCalendarBoundariesHaveSingleSource 两个判据一起钉：
// A. 消费者侧零内联：除 store/quota_org.go 外任何非测试源文件都不许自己现算月/日边界；
// B. 单一事实源仍在且仍 UTC 渲染：store/quota_org.go 里恰 2 处，两处都按 UTC 出。
func TestCalendarBoundariesHaveSingleSource(t *testing.T) {
	src := readNonTestGoCodeLines(t)

	var consumerHits []string
	var exemptSeen []string
	ownerLines, ownerUTC := 0, 0
	for rel, lines := range src {
		base := filepath.Base(rel)
		inStore := filepath.Base(filepath.Dir(rel)) == boundaryOwnerDir ||
			strings.HasPrefix(rel, boundaryOwnerDir+string(filepath.Separator))
		inExempt := strings.HasPrefix(rel, ratelimitExemptPrefix)
		for i, ln := range lines {
			if !(reInlineMonthBoundary.MatchString(ln) || reInlineDayBoundary.MatchString(ln)) {
				continue
			}
			if inStore && base == boundaryOwnerFile {
				ownerLines++
				if strings.Contains(ln, ".UTC()") || strings.Contains(ln, ", time.UTC)") {
					ownerUTC++
				}
				continue
			}
			if inExempt && !strings.Contains(ln, ".Format(") {
				// 豁免档只豁免「算时长终点」这一种用法；一旦它把边界渲染成字符串，
				// 就是拿去和 TEXT 戳记比了 ⇒ 立刻回到消费者侧射程（豁免不许变成后门）。
				exemptSeen = append(exemptSeen, rel+":"+strconv.Itoa(i+1))
				continue
			}
			consumerHits = append(consumerHits, rel+":"+strconv.Itoa(i+1)+" ⇒ "+strings.TrimSpace(ln))
		}
	}

	// A. 反重复主判据
	if len(consumerHits) > 0 {
		t.Fatalf("发现 %d 处**内联自算**的月/日边界（每一份都会与 store 那把尺子长期漂移）：\n  %s\n"+
			"⇒ 需要边界就调 store.MonthStartBound() / store.DayStartBound()，别在这儿现算。\n"+
			"⚠️ 这类洞的表现是「每月/每日头几个小时读数恒 0」，在 UTC 主机上跑测永远复现不了——"+
			"预算墙/日额度墙会在那段窗口里形同虚设，客户面数字同时归零。",
			len(consumerHits), strings.Join(consumerHits, "\n  "))
	}

	// B. 正控（防"零命中"恒绿）：出口必须在、必须恰好两个、必须仍按 UTC 渲染
	if ownerLines != boundaryOwnerCount {
		t.Fatalf("store/%s 里的日历边界出口应恰为 %d 处（月 + 日），实得 %d"+
			" ⇒ 出口被改名/删除/拆散，上面那条「消费者侧零命中」就失去了参照物（负向锁必须配正向对照）",
			boundaryOwnerFile, boundaryOwnerCount, ownerLines)
	}
	if ownerUTC != boundaryOwnerCount {
		t.Fatalf("store/%s 里的 %d 条边界出口必须都按 UTC 渲染（与 created_at 的写入口径同轴），实得 %d"+
			" ⇒ 边界与戳记不同区，跨月/跨日那一天的行会被整体判给错误窗口（月初读 0＝预算墙失效）",
			boundaryOwnerFile, boundaryOwnerCount, ownerUTC)
	}
	// 豁免档自证：豁免必须**真的被走到一次**（当前唯一使用者是 ratelimit 的 TTL 计算）。
	// 若哪天那条线改成渲染字符串、或整个文件消失，这里的计数就会归零 ⇒ 判红，
	// 逼人来同步修订豁免口径——而不是让豁免档悄悄变成"整包免检"。
	if len(exemptSeen) != 1 {
		t.Fatalf("豁免档（%s 的时长终点计算）应恰好命中 1 处，实得 %d 处 %v"+
			" ⇒ 豁免口径与仓库脱节：要么它已退化成拿边界比戳记（该进射程），要么扫描面没落到那儿（该查扫描）",
			ratelimitExemptPrefix, len(exemptSeen), exemptSeen)
	}
}

// TestPlansApiConsumesSharedBoundaries 收银台/订阅页两条读数腿必须走共享出口（等值锁），
// 且「今日」这条腿不许再吃月边界（旧形态把整月累计报成"今天用的"）。
func TestPlansApiConsumesSharedBoundaries(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("plans_api.go"))
	if err != nil {
		t.Fatalf("读 plans_api.go 失败: %v", err)
	}
	s := string(b)
	for _, want := range []string{"store.MonthStartBound()", "store.DayStartBound()"} {
		if !strings.Contains(s, want) {
			t.Fatalf("plans_api.go 未引用 %s ⇒ 收银台/订阅页的读数又拿了一份私有边界（〇-AF 补丁三）", want)
		}
	}
	// 负向对照：「今日」这条腿不许再吃月边界。
	if strings.Contains(s, "tid, u.ID, store.MonthStartBound())") {
		t.Fatal("「今日已用」这条腿又吃回月边界了 ⇒ 页面会把本月累计报成今天用的量")
	}
}
