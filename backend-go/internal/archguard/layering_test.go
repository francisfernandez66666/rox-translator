// ============ internal/archguard/layering_test.go · 职责说明 ============
// ★ 后端分层守卫（AST 闸门，2026-09-22 建立）。
//
// 为什么必须有闸门：AGENTS.md 一.1「store 冻结规则」与包依赖方向此前**只有文字约定、零机器校验**。
// 文字约定在多人 + AI 助手并行改动的仓库里等于没有约束——违例不会报错、不会进 code review 视野，
// 只会在下一次编译或下一次「起个单测却要连库」时才被察觉，而那时账已经记在别人头上。
//
// 三条规则各自的**真实故障后果**（不是「为了规范而规范」）：
//
//	R1 基础包禁止 import internal/store
//	   store 是 74 文件 / ~16.8k 行的单一数据访问包，自身依赖 config/secret/db/observability。
//	   反向 import 一旦成立：① 与 store 的既有依赖直接构成**循环依赖**（编译不过，报错现场在
//	   毫不知情的第三方提交身上）；② 即使暂时不成环，也让「读一个配置」「解一段密文」这种
//	   纯函数操作被迫拉起整个存储层——单测要建库、cmd 小工具二进制体积暴涨、store 冻结规则失效。
//
//	R2 internal/fileproc（纯文件/文本解析）禁止 import engine / orchestrator / api
//	   fileproc 是「字节进、结构出」的算法层，可离线单测是它的全部价值。
//	   反向依赖一旦进来：解析逻辑与模型调用/编排耦合，**没有任何文档能再说明输入输出边界**，
//	   回归只能靠真发一次翻译请求复现，fileproc 的单测全部退化为集成测试。
//
//	R3 internal/engine（模型调用内核）禁止 import orchestrator / api / service
//	   编排层是「组合 engine」的一方，反向依赖即循环；同时 engine 被 cmd 批处理工具、
//	   评测（evals）、离线脚本复用，一旦引用 service/api 就要连带拖进 HTTP 上下文与计费链路。
//
//	R4 internal/api 是唯一 HTTP 边界：除 cmd/ 外任何包不得 import
//	   越过它意味着鉴权、审计（LogAudit）、计费扣减、限流这些**挂在 HTTP 入口上的横切责任**
//	   被绕开——别的包直接调 handler 内部函数即可「不记账、不审计」地干活，
//	   这是资金与合规层面的外溢，不是代码风格问题。
//
// 实现口径：go/parser + parser.ImportsOnly（只读 import 声明，不解析函数体，成本与文件数线性），
//
//	遍历模块根（判据：目录含 go.mod）下全部**非 _test.go** 源码，按规则表判违例；
//	存量违例进 legacyAllow 显式豁免（带中文「为何豁免 / 何时摘除」），只减不增，
//	且豁免项若已不存在即「僵尸项」红灯——与 internal/observability/logratchet_test.go、
//	internal/db/guard_test.go 同一手法。TestRuleNotAlwaysTrue 反向自检，证明判定函数不是恒真。
//
// 运行：cd backend-go && go test -count=1 ./internal/archguard/
// 本包不读 config、不连库，无需 AGENTS.md 一.4 的方言自钉。
// =============================================
package archguard

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// modulePath 读 go.mod 的 module 声明（本仓为 translator）。规则里的 import 前缀由它派生，
// 而不是硬编码字符串——模块改名时闸门会整体红灯（扫不到任何内部 import），不会静默失效。
func modulePath(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("读 go.mod 失败: %v", err)
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if s, ok := strings.CutPrefix(strings.TrimSpace(ln), "module "); ok {
			return strings.Trim(strings.TrimSpace(s), `"`)
		}
	}
	t.Fatal("go.mod 里未找到 module 声明")
	return ""
}

// layerRule 一条分层规则：relDir 落在 scopes 下（含其子包）时，禁止 import banned 里的包（含子包）。
type layerRule struct {
	id     string   // 规则编号，出现在红灯输出与豁免清单里
	scopes []string // 受约束的包目录前缀（相对模块根、slash）；空串代表全模块
	banned []string // 禁止依赖的内部包目录（相对模块根），命中其后缀 /... 亦算违例
	exempt []string // 豁免目录前缀（如 cmd：唯一合法的 api 组装入口）
	why    string   // 故障后果一句话，红灯时直接打给提交者看
}

// basePackages 基础包集合（AGENTS.md 一.1 第 3 条「密文/配置/连接一律下沉基础包，禁止反向 import store」）。
// 建闸时逐包实测 store import 为 0 的才放进来（iam/infra/queue 属同一层基础设施，一并纳入）；
// billing/crawler/culture/evals/notify/openapi 等**域包**合理依赖 store，不在集合内——
// 扩大集合前要先确认该包存量干净，否则闸门一上线就是几十条红灯，反而逼人去放宽判据。
var basePackages = []string{"secret", "db", "config", "observability", "errors", "tenant", "auth", "iam", "infra", "queue"}

// layerRules 规则表。R1 由 basePackages 展开（每个基础包一条，便于红灯精确到包名）。
func layerRules() []layerRule {
	rules := make([]layerRule, 0, len(basePackages)+3)
	for _, p := range basePackages {
		rules = append(rules, layerRule{
			id:     "R1-基础包不得依赖store",
			scopes: []string{"internal/" + p},
			banned: []string{"internal/store"},
			why:    "循环依赖 / 「读一个配置」被迫拉起整个存储层（见 AGENTS.md 一.1）",
		})
	}
	rules = append(rules,
		layerRule{
			id:     "R2-fileproc保持纯算法层",
			scopes: []string{"internal/fileproc"},
			banned: []string{"internal/engine", "internal/orchestrator", "internal/api"},
			why:    "解析逻辑与模型调用耦合后，fileproc 单测全部退化为集成测试，输入输出边界失守",
		},
		layerRule{
			id:     "R3-engine不得反向依赖",
			scopes: []string{"internal/engine"},
			banned: []string{"internal/orchestrator", "internal/api", "internal/service"},
			why:    "编排层是组合 engine 的一方，反向 import 即循环；且 engine 被 cmd/evals 复用时会被拖进 HTTP 与计费链路",
		},
		layerRule{
			id:     "R4-api是唯一HTTP边界",
			scopes: []string{""}, // 全模块
			banned: []string{"internal/api"},
			exempt: []string{"cmd"},
			why:    "绕过 api 即绕过鉴权/审计/计费扣减，属资金与合规层面的边界外溢，不是风格问题",
		},
	)
	return rules
}

// importEdge 一条实测 import 关系。
type importEdge struct {
	file     string // 相对模块根、slash 的源文件
	line     int    // import 声明所在行
	dir      string // 源文件所在目录（相对模块根）
	imported string // 被 import 的完整路径
}

// violation 判定后的违例：边 + 命中的规则。
type violation struct {
	edge importEdge
	rule *layerRule
}

// id 豁免清单与红灯输出用的稳定标识：规则 + 文件:行号 + 谁 import 了谁。
func (v violation) id() string {
	return v.rule.id + " | " + v.edge.file + ":" + strconv.Itoa(v.edge.line) + " | " +
		v.edge.dir + " -> " + v.edge.imported
}

// classify 判定函数（纯函数，供自检用例直接喂数据）：返回命中的规则，未违例返回 nil。
func classify(mod, rulesPrefixDir, importPath string, rules []layerRule) *layerRule {
	for i := range rules {
		r := &rules[i]
		if !inScopes(rulesPrefixDir, r.scopes) {
			continue
		}
		if inPrefixes(rulesPrefixDir, r.exempt) {
			continue
		}
		for _, b := range r.banned {
			// 命中包本身或其子包（internal/engine 违例不该靠只写 internal/engine/foo 的精确匹配躲过）
			want := mod + "/" + b
			if importPath == want || strings.HasPrefix(importPath, want+"/") {
				return r
			}
		}
	}
	return nil
}

// inScopes 目录是否落在任一 scope 下；scope 为空串表示全模块。
func inScopes(dir string, scopes []string) bool {
	for _, s := range scopes {
		if s == "" || dir == s || strings.HasPrefix(dir, s+"/") {
			return true
		}
	}
	return false
}

// inPrefixes 目录是否落在任一豁免前缀下。
func inPrefixes(dir string, prefixes []string) bool {
	for _, s := range prefixes {
		if s == "" {
			continue
		}
		if dir == s || strings.HasPrefix(dir, s+"/") {
			return true
		}
	}
	return false
}

// collectImports 遍历模块根下全部非测试 .go，抽取 import 边。
// 用 parser.ImportsOnly：本闸门只关心 import 声明，不解析函数体，全仓扫描成本可控。
// 先按目录聚合再逐目录解析一次（同目录多文件不重复解析）。
func collectImports(t *testing.T, root string) []importEdge {
	t.Helper()
	// ① 收集含生产 .go 的目录
	dirs := map[string]bool{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 个别目录不可读不阻断整仓扫描（漏扫由「扫描有效性」断言兜底）
		}
		if info.IsDir() {
			switch info.Name() {
			case "vendor", ".git", "node_modules", "testdata", "dist", "build":
				return filepath.SkipDir
			}
			return nil
		}
		// 测试文件不算：为构造场景跨层引用（fake store、集成测试起 api）是正常手法，不代表生产依赖方向
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		dirs[filepath.Dir(path)] = true
		return nil
	})
	if err != nil {
		t.Fatalf("遍历模块失败: %v", err)
	}

	// ② 逐目录解析 import 声明
	fset := token.NewFileSet()
	var edges []importEdge
	for dir := range dirs {
		pkgs, perr := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
			return !strings.HasSuffix(fi.Name(), "_test.go")
		}, parser.ImportsOnly)
		if perr != nil {
			// 解析不过的包本身就是编译期问题，这里只记录不 fatalf，避免掩盖分层结论
			t.Errorf("解析目录失败 %s: %v", dir, perr)
			continue
		}
		relDir, rerr := filepath.Rel(root, dir)
		if rerr != nil {
			continue
		}
		for _, pkg := range pkgs {
			for fname, f := range pkg.Files {
				relFile, ferr := filepath.Rel(root, fname)
				if ferr != nil {
					continue
				}
				for _, spec := range f.Imports {
					p, uerr := strconv.Unquote(spec.Path.Value)
					if uerr != nil {
						continue
					}
					edges = append(edges, importEdge{
						file:     filepath.ToSlash(relFile),
						line:     fset.Position(spec.Pos()).Line,
						dir:      filepath.ToSlash(relDir),
						imported: p,
					})
				}
			}
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].file != edges[j].file {
			return edges[i].file < edges[j].file
		}
		return edges[i].line < edges[j].line
	})
	return edges
}

// legacyAllow 存量违例豁免（只减不增）。每项必须写清「为什么历史豁免」与「什么条件下该摘除」，
// 否则评审不予通过；豁免项对应的依赖一旦消失，TestLayeringNoZombieExemption 会红灯逼你删行。
var legacyAllow = []struct {
	id    string // 违例标识，格式同 violation.id()
	why   string // 为何豁免（历史成因）
	until string // 摘除条件
}{
	{
		id: "R1-基础包不得依赖store | internal/auth/auth.go:13 | internal/auth -> translator/internal/store",
		why: "auth 是 IAM 子系统拆分时保留的**向后兼容薄委托壳**（见该文件头注释），签名沿用拆分前的 *store.User，" +
			"内部实为一行 (*iam.User)(u) 的指针类型转换后转调 iam，不产生任何数据访问；" +
			"当时为不改动 api/cmd 十余个调用点而保留旧导入路径。",
		until: "把 internal/auth 的调用点（internal/api/*.go 与 cmd/*）整体迁到 translator/internal/iam " +
			"并删除本薄委托包后摘除；或把 *store.User 别名改为 *iam.User（store 只被别名定义处引用，替换即解依赖）。" +
			"新增任何 auth -> store 的其他 import 一律红灯，不得并入本豁免。",
	},
}

// TestLayeringNoNewViolations 主断言：实测违例集必须被 legacyAllow 完全覆盖。
func TestLayeringNoNewViolations(t *testing.T) {
	root := moduleRoot(t)
	mod := modulePath(t, root)
	rules := layerRules()
	edges := collectImports(t, root)

	violations := findViolations(mod, edges, rules)
	allow := map[string]bool{}
	for _, a := range legacyAllow {
		allow[a.id] = true
	}
	var fresh []string
	for _, v := range violations {
		if !allow[v.id()] {
			fresh = append(fresh, v.id()+"\n    后果："+v.rule.why)
		}
	}
	sort.Strings(fresh)
	if len(fresh) > 0 {
		t.Errorf("发现 %d 处**新增**分层违例（AGENTS.md 一.1 硬约定；违例修法见各条「后果」）：\n  %s",
			len(fresh), strings.Join(fresh, "\n  "))
	}
}

// findViolations 对 import 边集跑规则表（抽成独立函数供自检用例复用同一判定路径）。
func findViolations(mod string, edges []importEdge, rules []layerRule) []violation {
	var out []violation
	for _, e := range edges {
		if !strings.HasPrefix(e.imported, mod+"/") {
			continue // 三方标准库不在分层规则内
		}
		if r := classify(mod, e.dir, e.imported, rules); r != nil {
			out = append(out, violation{edge: e, rule: r})
		}
	}
	return out
}

// TestScanActuallyCoversCodebase 扫描有效性：闸门最容易的失效方式是「扫了个空集然后永远绿」。
func TestScanActuallyCoversCodebase(t *testing.T) {
	root := moduleRoot(t)
	mod := modulePath(t, root)
	if mod == "" {
		t.Fatal("模块路径为空")
	}
	edges := collectImports(t, root)
	if len(edges) < 500 {
		t.Fatalf("全仓 import 边仅 %d 条，明显低于实际规模——扫描根或过滤条件失效，本闸门当前不可信", len(edges))
	}
	internal := 0
	for _, e := range edges {
		if strings.HasPrefix(e.imported, mod+"/internal/") {
			internal++
		}
	}
	if internal < 300 {
		t.Fatalf("模块内部 import 边仅 %d 条（模块路径=%q），疑似 module 前缀与 import 口径不一致，规则永远不会命中", internal, mod)
	}
	// 抽样锚点：这些依赖是仓库现实，扫不到即遍历逻辑错了（imported 写完整路径，勿再拼模块前缀）
	for _, anchor := range []struct{ dir, imported string }{
		{"internal/api", "translator/internal/store"},
		{"internal/store", "translator/internal/db"},
	} {
		found := false
		for _, e := range edges {
			if (e.dir == anchor.dir || strings.HasPrefix(e.dir, anchor.dir+"/")) && e.imported == anchor.imported {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("抽样锚点 %s -> %s 未被扫到，扫描逻辑已失配", anchor.dir, anchor.imported)
		}
	}
}

// TestRuleNotAlwaysTrue 反影子自检（同 internal/db/guard_test.go 的 TestNoShadowMigrationRunner 思路）：
// 证明 classify 不是恒真也不是恒假——否则前面几条断言等于没跑。
// 违例全部在内存里构造，不依赖仓库现状，因此任何人把规则写松（或写爆）都会先在这里翻红。
func TestRuleNotAlwaysTrue(t *testing.T) {
	const mod = "translator"
	rules := layerRules()
	// ① 内存构造违例：三条规则各造若干条，必须全部命中且命中到预期规则
	//    （classify 按规则表顺序取首个命中，故 fileproc -> api 归 R2，其 banned 也含 internal/api）。
	mustHit := []struct{ dir, imported, wantRule string }{
		{"internal/secret", "translator/internal/store", "R1-基础包不得依赖store"},
		{"internal/config/sub", "translator/internal/store", "R1-基础包不得依赖store"}, // 子包同样受约束
		{"internal/db", "translator/internal/store", "R1-基础包不得依赖store"},
		{"internal/observability", "translator/internal/store", "R1-基础包不得依赖store"},
		{"internal/fileproc", "translator/internal/engine", "R2-fileproc保持纯算法层"},
		{"internal/fileproc", "translator/internal/orchestrator", "R2-fileproc保持纯算法层"},
		{"internal/fileproc/office", "translator/internal/api", "R2-fileproc保持纯算法层"},
		{"internal/engine", "translator/internal/orchestrator", "R3-engine不得反向依赖"},
		{"internal/engine", "translator/internal/service", "R3-engine不得反向依赖"},
		{"internal/engine", "translator/internal/api", "R3-engine不得反向依赖"},
		{"internal/kb", "translator/internal/api", "R4-api是唯一HTTP边界"},
		{"internal/store", "translator/internal/api", "R4-api是唯一HTTP边界"},
		{"internal/service", "translator/internal/api/handler", "R4-api是唯一HTTP边界"}, // 子包也算越界
	}
	for _, m := range mustHit {
		r := classify(mod, m.dir, m.imported, rules)
		if r == nil {
			t.Errorf("自检失败：内存构造的违例 %s -> %s 未被判定违例，规则已恒真失效（本闸门不可信）", m.dir, m.imported)
			continue
		}
		if r.id != m.wantRule {
			t.Errorf("自检失败：%s -> %s 命中 %s，预期 %s（规则表顺序/口径被改动）", m.dir, m.imported, r.id, m.wantRule)
		}
	}
	// ② 合法依赖必须放过，否则规则恒真会把后续所有人逼去放宽判据：
	//    上层组合下层（api -> service、orchestrator -> engine）、数据层用配置（store -> config）、
	//    纯算法用配置（fileproc -> config）、cmd 组装 HTTP 服务（R4 的唯一豁免位）。
	mustPass := []struct{ dir, imported string }{
		{"internal/api", "translator/internal/service"},
		{"internal/orchestrator", "translator/internal/engine"},
		{"internal/store", "translator/internal/config"},
		{"internal/fileproc", "translator/internal/config"},
		{"internal/api", "translator/internal/fileproc"},
		{"cmd/server", "translator/internal/api"},
		{"cmd/auto-approve", "translator/internal/api"},
		{"internal/billing", "translator/internal/store"}, // 域包访问数据层合法（不在 R1 基础包集合内）
	}
	for _, m := range mustPass {
		if r := classify(mod, m.dir, m.imported, rules); r != nil {
			t.Errorf("自检失败：合法依赖 %s -> %s 被误判为违例（%s），规则过宽", m.dir, m.imported, r.id)
		}
	}
}

// TestNoZombieExemption 僵尸豁免守卫：legacyAllow 每条都必须仍能对上实测违例。
// 修好了历史违例却留着豁免行，等于给下一个违例者预留了空位——这条断言把它堵死。
func TestNoZombieExemption(t *testing.T) {
	root := moduleRoot(t)
	mod := modulePath(t, root)
	rules := layerRules()
	hit := map[string]bool{}
	for _, v := range findViolations(mod, collectImports(t, root), rules) {
		hit[v.id()] = true
	}
	for _, a := range legacyAllow {
		if !hit[a.id] {
			t.Errorf("legacyAllow 僵尸豁免：实测已不存在该违例，请删除该行\n  %s\n  （原豁免理由：%s）", a.id, a.why)
		}
	}
	// 反向：实测违例总数不得高于豁免条目数（等价于「新增即红灯」的总量口径，防 id 格式漂移造成漏配）。
	if len(hit) > len(legacyAllow) {
		t.Errorf("实测违例 %d 处 > 豁免 %d 条：有未登记的分层违例，请修复或按格式登记（附理由与摘除条件）", len(hit), len(legacyAllow))
	}
}

// moduleRoot 从测试工作目录（internal/archguard）上溯到模块根 backend-go（判据：含 go.mod）。
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("未找到 go.mod 所在模块根")
	return ""
}
