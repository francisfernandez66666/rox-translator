// ============================================================================
// residue_report_gate_test.go — ★ 10-01（〇-AF 收尾）：把「中文残片例行读数」这条跨语言接线进闸
//
// 射程与职责（本文件只管"读数这条链"，不管正字表内容本身，表内容见 ja_residue_fixup_test.go）：
//
//	这批把指标从「补翻拒绝率」换成**「分档分布 ＋ 正字表覆盖率」**这一对，
//	落点是两个文件：读数实现的《scripts/assist_residue_report.py》（只读、只出 stdout），
//	以及读数依赖的三份 Go 常量（han_residue.go 七个 reject*、canned_guard.go 三个 cannedReject*、
//	localize_async.go 八个 canned_sync_*/canned_bg_*）。
//	跨语言接线没有编译期保证，**没有本文件的话它会以三种方式烂掉**，而且每一种都是静默的：
//	  ① 有人新增一档（比如将来给后台腿加 canned_bg_locked），脚本名单没跟上
//	     ⇒ 那一档的失败在例行指标里永久隐身（现网日志照打，没人读），下一批该修哪一边全靠猜；
//	  ② 有人把 jaResidueFixups 改名／把文件挪走
//	     ⇒ 脚本读不出表，覆盖率要么恒 0 要么直接崩，看起来像"表全不管用"这种假结论；
//	  ③ 整个脚本文件被删（"这工具又没人跑"）⇒ 指标静默消失，交接文档里那句 coverage_x= 成了孤本。
//	所以本文件三组判据：名单双向等值（①）、脚本还在且确实只读（③＋写操作面）、
//	真跑一次夹具并钉住关键读数（②＋"脚本从没跑过"这一形态——本仓点过名无数次的死形态）。
//
// 反证形态（纪律要求"每条新锁配一条故意破坏⇒当场红"，且不许在跑测试期间就地改源）：
//
//	本文件所有反证都在 **t.TempDir 的副本**上做——把脚本源码读出来、在内存/临时目录里改坏，
//	再喂给同一个判据函数或真跑一次，断言它必须报红；每条反证还配一条"正向对照"
//	（判据对合法输入必须放行），防止判据本身是恒真的空锁。逐条列在各用例的行内注释里。
//
// ⚠️ 方言口径（AGENTS §一·4）：本文件**不起库、不起引擎**，只读文本与执行 python3，
//
//	不碰 config.C 也不写任何全局状态，因此不存在 DB_DRIVER 泄漏问题；
//	跑本包时仍按约定钉 `env DB_DRIVER=sqlite`（同包其它用例要起库）。
//
// ============================================================================
package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// 读数链上的三个仓库内文件（一律相对仓库根，测试里再拼绝对路径）。
const (
	residueReportRelPath  = "scripts/assist_residue_report.py"
	residueFixtureRelPath = "scripts/fixtures/assist_residue_fixture.jsonl"
	residueGoTableRelPath = "backend-go/internal/assist/engine/han_residue.go"
)

// residueDefaultLogMarker 脚本里那份"生产日志默认路径"的字面量。
// 锁它是因为**默认值就是这份指标在生产上的输入面**：有人把默认值改成仓库内夹具，
// 例行读数就会天天读一份不会增长的本地文件，症状是"覆盖率永远不动"，很难往这上面想。
const residueDefaultLogMarker = "/opt/ai-assist/data/assist.log"

// residueTierCountWant 档名总数（7 reject* ＋ 3 cannedReject* ＋ 8 canned_sync_*/canned_bg_*）。
// 钉一个数字是刻意的：只比"两边等值"的话，同时删掉一档（Go 与脚本各删一行）是查不出来的，
// 而那种"成对删除"恰好是重构里最容易顺手做的事。
const residueTierCountWant = 18

// goResidueReasonTiers 汇总 Go 侧三份常量表里的**全部档名字面量**。
//
// 这里必须手写枚举而不是"从日志文本里扫"：扫出来的东西会跟着注释一起长，
// 而注释里提到某个档名不代表代码真会返回它。手写一遍的代价是新增常量时必须在这里点名，
// 这正是本锁想要的效果——**加档的人必须同时被脚本名单与本函数点名两次**。
func goResidueReasonTiers() []string {
	return []string{
		// han_residue.go：补翻底座的七个出口
		rejectNoUpstream,
		rejectNoLeaks,
		rejectUpstreamError,
		rejectTruncated,
		rejectEmptyOutput,
		rejectLineCount,
		rejectNotImproved,
		// canned_guard.go：canned 出栈闸的三个分档
		cannedRejectEmpty,
		cannedRejectResidue,
		cannedRejectBrandAdded,
		// localize_async.go：有界同步腿＋后台腿的八个分档
		cannedSyncTimeout,
		cannedSyncCanceled,
		cannedSyncUpstream,
		cannedBgTimeout,
		cannedBgUpstream,
		cannedBgGated,
		cannedBgStale,
		cannedBgPanic,
	}
}

// residueRepoRoot 从测试工作目录（backend-go/internal/assist/engine）向上找仓库根。
// 判据取"同时有读数脚本与正字表源文件"两条，只问其一会在半套 checkout 里认错根；
// 找不到即 Fatal——静默 skip 等于闸门空转（恒真假绿）。
func residueRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("取当前目录失败：%v", err)
	}
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, residueReportRelPath)); err == nil {
			if _, err := os.Stat(filepath.Join(d, residueGoTableRelPath)); err == nil {
				return d
			}
		}
		parent := filepath.Dir(d)
		if parent == d {
			t.Fatalf("从 %s 向上找不到仓库根（需要同时有 %s 与 %s）——本文件不许静默跳过",
				dir, residueReportRelPath, residueGoTableRelPath)
		}
		d = parent
	}
}

// pythonReasonTiersBlockRe 抓脚本里 `REASON_TIERS = [ ... ]` 那一段。
// 刻意按"块"抓而不是全文 findall 所有引号串：全文抓会把脚本里其它字符串
// （字段名 leaks／fixed／detail、正则片段）一起当成档名，于是等值锁永远对不上，
// 而有人为了"让它对上"会去放宽判据——那就是把锁拆了。
var pythonReasonTiersBlockRe = regexp.MustCompile(`(?s)REASON_TIERS\s*=\s*\[(.*?)\n\]`)

// pythonStringLitRe 抓块内的字符串字面量（名单只用双引号写法，见脚本里的 REASON_TIERS）。
var pythonStringLitRe = regexp.MustCompile(`"([^"\n]+)"`)

// parsePythonReasonTiers 从脚本源码里解出 REASON_TIERS 的档名清单（按源码顺序）。
// 解不出块／块里没有字符串字面量 → 返回 error，调用方判红（不许当成"名单为空然后继续比"）。
func parsePythonReasonTiers(src string) ([]string, error) {
	m := pythonReasonTiersBlockRe.FindStringSubmatch(src)
	if m == nil {
		return nil, os.ErrInvalid // 块被改名／被写成 tuple：跨语言接线的第一条腿断了
	}
	var out []string
	for _, s := range pythonStringLitRe.FindAllStringSubmatch(m[1], -1) {
		out = append(out, s[1])
	}
	if len(out) == 0 {
		return nil, os.ErrInvalid // 块空了也"等值"就是空锁
	}
	return out, nil
}

// diffReasonTiers 双向差集：goOnly＝Go 有而脚本没有，scriptOnly＝脚本有而 Go 没有。
// 两个方向都必须为空——只比一个方向的话，"脚本名单漏一档"与"脚本名单多一档"就只有一个能红。
func diffReasonTiers(goTiers, scriptTiers []string) (goOnly, scriptOnly []string) {
	inScript := map[string]bool{}
	for _, s := range scriptTiers {
		inScript[s] = true
	}
	inGo := map[string]bool{}
	for _, s := range goTiers {
		inGo[s] = true
	}
	for _, s := range goTiers {
		if !inScript[s] {
			goOnly = append(goOnly, s)
		}
	}
	for _, s := range scriptTiers {
		if !inGo[s] {
			scriptOnly = append(scriptOnly, s)
		}
	}
	sort.Strings(goOnly)
	sort.Strings(scriptOnly)
	return goOnly, scriptOnly
}

// dropReasonTierLine 从脚本源码副本里删掉名单中点名某个档名的那一行（反证甲/庚用）。
//
// 匹配串带引号与逗号（`"line_count",`）是为了只命中名单里那一行：脚本头部的现网读数
// 写的是不带引号的 `line_count 1`，全文替换会把注释一起改掉、反证就改错了地方。
// 第二个返回值＝有没有真的删掉；**删不掉必须让用例红**（脚本结构变了要跟着改反证，
// 而不是留一条"永远命中不了、于是永远不会红"的空反证）。
func dropReasonTierLine(src, tier string) (string, bool) {
	needle := `"` + tier + `",`
	i := strings.Index(src, needle)
	if i < 0 {
		return src, false
	}
	j := strings.Index(src[i:], "\n")
	if j < 0 {
		return src, false
	}
	return src[:i] + src[i+j+1:], true
}

// addReasonTierLine 往名单里那一行后面插一个假想档名（反证乙用，只作用于副本）。
func addReasonTierLine(src, tier, fake string) (string, bool) {
	needle := `"` + tier + `",`
	i := strings.Index(src, needle)
	if i < 0 {
		return src, false
	}
	at := i + len(needle)
	return src[:at] + "\n    \"" + fake + "\", // 反证插入的假档名" + src[at:], true
}

// residueOpenCallRe / residueWriteModeRe 用于"脚本确实只读"这一条：
// 每个 open() 调用必须显式带只读模式，且参数里不许出现写模式字面量。
var (
	residueOpenCallRe  = regexp.MustCompile(`open\(([^)]*)\)`)
	residueWriteModeRe = regexp.MustCompile(`["'](w|a|x|r\+|w\+|a\+)["']`)
)

// residueWriteForbidden 写盘／起进程／联网／动服务的关键词黑名单（命中即违规）。
var residueWriteForbidden = []string{
	"os.remove", "os.rename", "os.replace", "os.makedirs", "shutil",
	"subprocess", "Popen", "socket", "urllib", "requests",
	"systemctl",
}

// residueMarkerKeys 脚本必须以这些字面量**留在源码里**（每条各锁一个不可退化点）。
//
// 注意这一组判据只锁"字样还在"，真正拦下写操作的是下面⑤⑥两条机械判据——
// 分层是刻意的：声明被删＝文档面失效（要红，因为它决定下一个人敢不敢在生产上直接跑），
// 真出现写模式＝行为面失效（更要红）。
var residueMarkerKeys = []struct{ what, needle string }{
	{"@@RES 前缀（读数行靠它被 grep 到）", "@@RES"},
	{"只读声明", "只读"},
	{"生产日志默认路径 " + residueDefaultLogMarker, residueDefaultLogMarker},
	{"从 Go 源现读正字表（jaResidueFixups）", "jaResidueFixups"},
}

// residueScriptViolations 静态判据：脚本存在性 ＋ 四个必留标记 ＋ 无写模式 ＋ 无联网/起进程。
// 返回违规列表（空＝合格）。参数 absPath 让反证能指到 t.TempDir 里的坏副本。
func residueScriptViolations(t *testing.T, absPath string) []string {
	t.Helper()
	raw, err := os.ReadFile(absPath)
	if err != nil {
		// ③ 那一形态的主判据：整个脚本被删掉＝指标静默消失，这里必须当场红。
		return []string{"读数脚本读不到（文件缺失或被挪走）：" + err.Error()}
	}
	src := string(raw)
	var bad []string
	for _, m := range residueMarkerKeys {
		if !strings.Contains(src, m.needle) {
			bad = append(bad, "脚本里已没有「"+m.what+"」")
		}
	}
	// 写模式：每个 open() 调用都必须只读。
	for _, m := range residueOpenCallRe.FindAllStringSubmatch(src, -1) {
		if residueWriteModeRe.MatchString(m[1]) {
			bad = append(bad, "脚本里出现写模式的 open("+m[1]+")")
		}
	}
	// 黑名单（写盘／起进程／联网／动服务）。
	for _, kw := range residueWriteForbidden {
		if strings.Contains(src, kw) {
			bad = append(bad, "脚本里出现被禁的可执行标识："+kw)
		}
	}
	return bad
}

// runResidueReport 真跑一次读数脚本，返回（合并输出, 退出码）。
// python3 缺失一律 Fatal 不 skip：本仓闸门（scripts/missing_comments.py）本来就要求 python3，
// 把它做成"这台机器上跳过"的软判据，等于让读数链在开发机上从来没有被验证过。
func runResidueReport(t *testing.T, scriptPath, logPath, repoRoot string, extra ...string) (string, int) {
	t.Helper()
	bin, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("本机没有 python3：读数脚本无法验证（AGENTS §二 的注释闸同样要求 python3，不许静默跳过）：%v", err)
	}
	args := append([]string{scriptPath, "--log", logPath, "--repo-root", repoRoot}, extra...)
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("执行 python3 %v 失败：%v\n%s", args, err, out)
		}
		code = ee.ExitCode()
	}
	return string(out), code
}

// parseResidueReadings 把 `@@RES k=v` 收成 map（同键重复保留首个：读数键设计上唯一，
// 逐片段的 ja_frag_N 那类靠下标区分，不参与本 map 的判据）。
func parseResidueReadings(out string) map[string]string {
	readings := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "@@RES ") {
			continue
		}
		kv := strings.SplitN(strings.TrimPrefix(line, "@@RES "), "=", 2)
		if len(kv) != 2 {
			continue
		}
		if _, dup := readings[kv[0]]; !dup {
			readings[kv[0]] = kv[1]
		}
	}
	return readings
}

// writeTempCopy 把改坏后的脚本源码写进临时目录（反证专用，绝不落在仓库里）。
func writeTempCopy(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("写临时副本 %s 失败：%v", name, err)
	}
	return p
}

// TestResidueReportReasonRosterMatchesGoConstants 名单**双向等值**（①那一形态的入口锁）。
func TestResidueReportReasonRosterMatchesGoConstants(t *testing.T) {
	root := residueRepoRoot(t)
	srcRaw, err := os.ReadFile(filepath.Join(root, residueReportRelPath))
	if err != nil {
		t.Fatalf("读数脚本读不到（%s）：%v——指标文件被人删掉就是这一条红，而不是让覆盖率静默消失", residueReportRelPath, err)
	}
	src := string(srcRaw)
	scriptTiers, err := parsePythonReasonTiers(src)
	if err != nil {
		t.Fatalf("脚本里的 REASON_TIERS 块解析失败：%v——块被改名／拆成多个列表，跨语言接线的第一条腿已经断了", err)
	}
	goTiers := goResidueReasonTiers()
	goOnly, scriptOnly := diffReasonTiers(goTiers, scriptTiers)
	if len(goOnly) > 0 || len(scriptOnly) > 0 {
		t.Fatalf("分档名单与 Go 常量不等值：Go 有而脚本没有=%v，脚本有而 Go 没有=%v"+
			"（前者＝新档在例行指标里永久隐身，后者＝指标在数一个代码里已经不存在的病）", goOnly, scriptOnly)
	}
	if len(scriptTiers) != len(goTiers) {
		t.Fatalf("名单条数不等：脚本 %d / Go %d——等值判据之外还要求两侧都无重复项", len(scriptTiers), len(goTiers))
	}

	t.Run("反证甲：删掉一档必须红（Go 有／脚本没有那一向）", func(t *testing.T) {
		mutated, ok := dropReasonTierLine(src, "line_count")
		if !ok {
			t.Fatalf("反证夹具没命中名单里 \"line_count\" 那一行——REASON_TIERS 的写法变了，反证要跟改")
		}
		removed, err := parsePythonReasonTiers(mutated)
		if err != nil {
			return // 解析失败同样是"报红"，判据目的达成
		}
		if onlyGo, _ := diffReasonTiers(goTiers, removed); len(onlyGo) == 0 {
			t.Fatalf("删掉一档却没报红——双向等值锁退化成单向")
		}
	})

	t.Run("反证乙：凭空多一档必须红（脚本有／Go 没有那一向）", func(t *testing.T) {
		mutated, ok := addReasonTierLine(src, "line_count", "canned_bg_locked")
		if !ok {
			t.Fatalf("反证夹具没命中名单里 \"line_count\" 那一行，无法插入假档名")
		}
		added, err := parsePythonReasonTiers(mutated)
		if err != nil {
			t.Fatalf("反证乙解析失败：%v", err)
		}
		if _, onlyScript := diffReasonTiers(goTiers, added); len(onlyScript) == 0 {
			t.Fatalf("脚本名单凭空多一档却没报红——反向锁不成立")
		}
	})

	t.Run("反证丙：名单块被改名必须报解析失败（不许退化成空名单继续比）", func(t *testing.T) {
		mutated := strings.Replace(src, "REASON_TIERS = [", "REASON_NAMES = [", 1)
		if mutated == src {
			t.Fatalf("反证夹具没命中 REASON_TIERS 块头")
		}
		if _, err := parsePythonReasonTiers(mutated); err == nil {
			t.Fatalf("名单块被改名却仍能解析——接线的第一条腿没有锁")
		}
		// 正向对照：同一段文本原样喂进去必须解析成功，否则上面那三条"必须红"可能只是恒红。
		if _, err := parsePythonReasonTiers(src); err != nil {
			t.Fatalf("正向对照失败：%v", err)
		}
	})

	t.Run("档名总数钉死 18（防止两侧成对删除）", func(t *testing.T) {
		if len(goTiers) != residueTierCountWant {
			t.Fatalf("Go 侧档名合计应为 %d，实际 %d——数字变了说明有一侧加了/删了档却没同步这里与脚本名单",
				residueTierCountWant, len(goTiers))
		}
		if len(scriptTiers) != residueTierCountWant {
			t.Fatalf("脚本名单条数应为 %d，实际 %d", residueTierCountWant, len(scriptTiers))
		}
	})
}

// TestResidueReportScriptIsPresentAndStrictlyReadOnly 形态锁（③）：
// 文件在、四个必留标记在、没有任何写盘／起进程／联网的可执行标识，夹具也在。
func TestResidueReportScriptIsPresentAndStrictlyReadOnly(t *testing.T) {
	root := residueRepoRoot(t)
	abs := filepath.Join(root, residueReportRelPath)
	if bad := residueScriptViolations(t, abs); len(bad) > 0 {
		t.Fatalf("读数脚本不合格：%v", bad)
	}
	// 夹具也必须还在：脚本可跑但没有输入，"从没跑过"就退化成"跑给谁看都无所谓"。
	if _, err := os.Stat(filepath.Join(root, residueFixtureRelPath)); err != nil {
		t.Fatalf("读数夹具缺失（%s）：%v", residueFixtureRelPath, err)
	}

	t.Run("反证丁：临时副本上五种破坏都要红", func(t *testing.T) {
		srcRaw, err := os.ReadFile(abs)
		if err != nil {
			t.Fatalf("读脚本失败：%v", err)
		}
		src := string(srcRaw)
		dir := t.TempDir()
		// 每个破坏都用 ReplaceAll：脚本里这些字样在注释中也会复现，
		// 只删一处等于没删（判据问的是"字样还在"，那就要把字样整体抹掉才叫破坏）。
		cases := [][2]string{
			{"抹掉 @@RES 前缀", "@@RES|@@NORES"},
			{"抹掉只读声明", "只读|可写"},
			{"把默认路径换成仓库内夹具", residueDefaultLogMarker + "|scripts/fixtures/x.jsonl"},
			{"不再现读正字表（改成抄一份）", "jaResidueFixups|NOSUCHTABLE"},
			{"open 改成写模式", `open(go_path, "r"|open(go_path, "w"`},
		}
		for i, c := range cases {
			parts := strings.SplitN(c[1], "|", 2)
			if len(parts) != 2 {
				t.Fatalf("反证夹具 %q 写法不对（应为 old|new）", c[0])
			}
			mutated := strings.ReplaceAll(src, parts[0], parts[1])
			if mutated == src {
				t.Fatalf("反证夹具「%s」没命中被改的字符串——脚本结构变了，反证要跟改", c[0])
			}
			p := writeTempCopy(t, dir, fmt.Sprintf("mutated_%d.py", i), mutated)
			if bad := residueScriptViolations(t, p); len(bad) == 0 {
				t.Fatalf("破坏「%s」却没报红——只读/现读锁不成立", c[0])
			}
		}
		// 第六支：整个文件不存在（③ 的主形态）。
		if bad := residueScriptViolations(t, filepath.Join(dir, "根本不存在.py")); len(bad) == 0 {
			t.Fatalf("脚本文件不存在却没报红——③ 这一形态没锁住")
		}
		// 正向对照：原样副本必须放行（否则上面六支可能只是恒红）。
		if bad := residueScriptViolations(t, writeTempCopy(t, dir, "pristine.py", src)); len(bad) > 0 {
			t.Fatalf("未破坏的副本被判红（判据过头，会把合法改动一起拦掉）：%v", bad)
		}
	})
}

// TestResidueReportRunsAgainstFixture 真跑一次脚本（②＋"有脚本从没跑过"那一形态的解药）。
//
// 钉的读数不是"退码 0"这么薄：夹具里 18 档各至少一条、ja 那一族刻意复刻现网 10-01 那五个残片，
// 所以一次运行同时验到「每一档都真的进了指标」「覆盖率算法正确」「非日文不进分母」三件事。
func TestResidueReportRunsAgainstFixture(t *testing.T) {
	root := residueRepoRoot(t)
	script := filepath.Join(root, residueReportRelPath)
	fixture := filepath.Join(root, residueFixtureRelPath)

	out, code := runResidueReport(t, script, fixture, root)
	if code != 0 {
		t.Fatalf("读数脚本跑挂（退出码 %d）：\n%s", code, out)
	}
	readings := parseResidueReadings(out)
	if len(readings) == 0 {
		t.Fatalf("脚本退 0 却一行 @@RES 都没出——那等于没有读数：\n%s", out)
	}

	// ① 十八档每档都必须有一行读数（含零），这样"今天这档没出现"与"这档不在指标里"是两件事。
	for _, tier := range goResidueReasonTiers() {
		key := "reason_" + tier
		if _, ok := readings[key]; !ok {
			t.Fatalf("读数里缺 %s 这一行——名单等值在**运行面**上也必须成立：\n%s", key, out)
		}
	}
	if readings["tiers_known"] != strconv.Itoa(residueTierCountWant) {
		t.Fatalf("tiers_known 应为 %d，实际 %q", residueTierCountWant, readings["tiers_known"])
	}
	// 夹具里每一档都至少出现过一次，所以读数为零只可能是"接线断了"而不是"今天没发生"。
	for _, tier := range goResidueReasonTiers() {
		if readings["reason_"+tier] == "0" {
			t.Fatalf("夹具本该覆盖 %s，读数却是 0——字段名或统计口径被改坏（reason/gate_reason/sync_reason 三面都要数）", tier)
		}
	}
	if readings["unknown_reason_count"] != "0" {
		t.Fatalf("夹具不该产出名单外的档名，实际 unknown=%s", readings["unknown_reason"])
	}
	// 干净窗口里不许有 alert 行（alert 只在破坏支里验，见反证戊/己）。
	if strings.Contains(out, "@@RES alert=") {
		t.Fatalf("夹具窗口里出现了 alert：\n%s", out)
	}

	// ② 坏行必须被跳过而不是让脚本崩（脚本已退 0，这里验"确实算过"）。
	if readings["bad_lines"] != "2" {
		t.Fatalf("bad_lines 应为 2（夹具里那两条非 JSON 行），实际 %q", readings["bad_lines"])
	}
	if readings["lines_kept"] == "0" {
		t.Fatalf("夹具里有 23 条可解析记录，lines_kept=0 说明解析腿断了")
	}

	// ③ 覆盖率三条读数：ja 那一族五个残片、只有 文件 命中表内键（＝现网 10-01 的实读结论）。
	if readings["ja_distinct_fragments"] != "5" {
		t.Fatalf("ja 去重残片数应为 5，实际 %q", readings["ja_distinct_fragments"])
	}
	if readings["ja_table_covered"] != "1" || readings["ja_table_missed"] != "4" {
		t.Fatalf("覆盖率计数不对：covered=%s missed=%s（期望 1/4，命中者=文件）",
			readings["ja_table_covered"], readings["ja_table_missed"])
	}
	if readings["coverage_x"] != "0.20" {
		t.Fatalf("coverage_x 应为 0.20，实际 %q——正字表内容与读数算法必须一起对得上", readings["coverage_x"])
	}
	if readings["ja_covered_list"] != "文件" {
		t.Fatalf("ja_covered_list 应为 文件，实际 %q", readings["ja_covered_list"])
	}
	// 逐个片段的判词行：四个表外形态必须各有一条 verdict=missed（与正字表那批判词同一条账）。
	for _, f := range jaTenOfOneOutOfTable {
		if !strings.Contains(out, "fragment="+f+" verdict=missed") {
			t.Fatalf("读数里没有 %s 的 missed 判词行（表外四形态要逐个点名）：\n%s", f, out)
		}
	}
	// 正字表键集合必须真从 Go 源读出来（读的是**当前**这张表，不是抄的那份）。
	if readings["ja_table_keys"] != "20" {
		t.Fatalf("ja_table_keys 应为 20（当前 jaResidueFixups 的行数），实际 %q——"+
			"这一条与表行数同值：往表里加/减一行时必须同步这里，正是「改表要过两道点名」的效果", readings["ja_table_keys"])
	}

	// ④ 非日文语种：只出词频，绝不进覆盖率分母，且把这条政策原样打在读数里。
	if readings["nonja_policy"] != "不进正字表_只走补翻" {
		t.Fatalf("nonja_policy 读数不对：%q", readings["nonja_policy"])
	}
	for _, lang := range []string{"en", "th", "ar"} {
		if !strings.Contains(readings["nonja_langs"], lang) {
			t.Fatalf("nonja_langs 应含 %s，实际 %q", lang, readings["nonja_langs"])
		}
	}
	// 夹具里 th 带了「积分」（表内键），但它**不许**被算进 ja 覆盖率——
	// 混进去就是拿全站残片去除以只服务日文的表，读数会假高。
	if strings.Contains(readings["ja_covered_list"], "积分") || strings.Contains(readings["ja_missed_list"], "积分") {
		t.Fatalf("非日文的「积分」被混进了覆盖率分母：covered=%q missed=%q",
			readings["ja_covered_list"], readings["ja_missed_list"])
	}
	// 实际生效面（fixed 字段）也要有读数：覆盖率说"兜不兜得住"，这一条说"今天真兜住了谁"。
	if readings["fixup_applied_keys"] != "文件:1" {
		t.Fatalf("fixup_applied_keys 应为 文件:1，实际 %q", readings["fixup_applied_keys"])
	}

	t.Run("反证戊：四类输入故障全部响亮退 2，不伪装成零读数", func(t *testing.T) {
		dir := t.TempDir()

		// 破坏一：日志路径不存在。
		out1, code1 := runResidueReport(t, script, filepath.Join(dir, "没有这个.log"), root)
		if code1 != 2 || !strings.Contains(out1, "error=log_unreadable") {
			t.Fatalf("日志读不到时应退 2 并点名（退 0 等于把「取不到输入」读成「今天很干净」）：code=%d\n%s", code1, out1)
		}
		// 破坏二：窗口里一条可解析记录都没有。
		garbage := filepath.Join(dir, "garbage.jsonl")
		if err := os.WriteFile(garbage, []byte("这不是 json\n这也不是\n"), 0o600); err != nil {
			t.Fatalf("写夹具失败：%v", err)
		}
		out2, code2 := runResidueReport(t, script, garbage, root)
		if code2 != 2 || !strings.Contains(out2, "error=no_records") {
			t.Fatalf("全坏行日志应退 2（bad_lines 要如实报出，但不能当成读数）：code=%d\n%s", code2, out2)
		}
		if !strings.Contains(out2, "bad_lines=2") {
			t.Fatalf("全坏行时 bad_lines 仍要如实报出，实际：\n%s", out2)
		}
		// 破坏三：仓库根指错 ⇒ 正字表读不出 ⇒ 覆盖率不许按空表算。
		// ★ 这一支就是「将来有人把 jaResidueFixups 改名」的现形处：脚本必须拒绝出数，而不是报覆盖率 0。
		out3, code3 := runResidueReport(t, script, fixture, dir)
		if code3 != 2 || !strings.Contains(out3, "error=ja_table_unparsed") {
			t.Fatalf("正字表解析不出时应退 2：code=%d\n%s", code3, out3)
		}
		// 破坏四：--since 切到未来 ⇒ 空窗口也不许出一屏零读数。
		out4, code4 := runResidueReport(t, script, fixture, root, "--since", "2099-01-01T00")
		if code4 != 2 || !strings.Contains(out4, "error=no_records") {
			t.Fatalf("切空窗口应退 2 而不是产出看起来像结论的零读数：code=%d\n%s", code4, out4)
		}
	})

	t.Run("反证己：窗口里只剩非日文残片时 coverage_x 报 n/a 而不是 0.00", func(t *testing.T) {
		dir := t.TempDir()
		// 只留一条 en 残片行：没有 ja 样本，覆盖率必须说"没样本"——
		// 0.00 会被读成"表一个都没兜住"，那是 §一·7「静默 0 命中」陷阱的读数版本。
		only := filepath.Join(dir, "en_only.jsonl")
		if err := os.WriteFile(only, []byte(
			`{"time":"2026-10-01T04:00:04+08:00","level":"WARN","msg":"x","lang":"en","reason":"upstream_error","leaks":"充值"}`+"\n"), 0o600); err != nil {
			t.Fatalf("写夹具失败：%v", err)
		}
		out5, code5 := runResidueReport(t, script, only, root)
		if code5 != 0 {
			t.Fatalf("只有一条合法记录时应正常退 0，实际 %d：\n%s", code5, out5)
		}
		r5 := parseResidueReadings(out5)
		if r5["coverage_x"] != "n/a" {
			t.Fatalf("无 ja 样本时 coverage_x 应为 n/a，实际 %q", r5["coverage_x"])
		}
		if !strings.Contains(out5, "@@RES alert=") {
			t.Fatalf("无样本必须同时留一条 alert（否则读数看着像结论）：\n%s", out5)
		}
	})

	t.Run("反证庚：删掉一档 ⇒ 运行面的十八行读数当场缺一行", func(t *testing.T) {
		// 上面那条等值锁是**源码级**的；这一支证明"少一档"在真跑的输出上同样看得见
		// （两把刀各守一面：将来有人只放宽其中一面，另一面还在）。
		srcRaw, err := os.ReadFile(script)
		if err != nil {
			t.Fatalf("读脚本失败：%v", err)
		}
		mutated, ok := dropReasonTierLine(string(srcRaw), rejectLineCount)
		if !ok {
			t.Fatalf("反证夹具没命中 REASON_TIERS 里 line_count 那一行（名单写法变了，反证要跟改）")
		}
		dir := t.TempDir()
		p := writeTempCopy(t, dir, "report_minus_one.py", mutated)
		out6, code6 := runResidueReport(t, p, fixture, root)
		if code6 != 0 {
			t.Fatalf("删一档不至于让脚本挂（挂说明删掉的是结构行，反证要跟改）：code=%d\n%s", code6, out6)
		}
		r6 := parseResidueReadings(out6)
		if _, has := r6["reason_"+rejectLineCount]; has {
			t.Fatalf("脚本名单里删掉了 %s，输出里却还有这一行读数——等值判据是抄出来的？", rejectLineCount)
		}
		// 同一支的连带效果：被删的那一档在日志里变成 unknown ⇒ 必须报 alert（名单漏档不是静默漏，
		// 而是会自己喊出来——这一条锁的是"alert 这一腿真在"）。
		if !strings.Contains(out6, "@@RES alert=") || r6["unknown_reason_count"] == "0" {
			t.Fatalf("删档后必须报 unknown_reason＋alert，实际 unknown=%s：\n%s", r6["unknown_reason_count"], out6)
		}
	})
}
