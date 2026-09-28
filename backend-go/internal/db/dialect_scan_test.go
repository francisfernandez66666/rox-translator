// ============================================================================
// dialect_scan_test.go — ★ D-7（2026-09-29）方言翻译层「未覆盖构造」负向扫描闸
//
// 为什么要这条锁（因果链）：本包的 ToDialect（rewrite.go:28-44）只翻译 **5 类**构造
//
//	（INTEGER PRIMARY KEY AUTOINCREMENT / 独立 AUTOINCREMENT / BLOB / REAL / INSERT OR IGNORE），
//	其余 SQLite↔PG 方言差异全靠「写 SQL 的人自觉不写方言专属语法」这条纪律兜着。
//	自觉这一档是**有成本的**：P1-5 那次（见 guard_test.go 头）就是 engine 裸 `?` 占位符
//	在 SQLite 正常、生产 PG 恒报错又被调用方吞成「无数据」，整条文化闸静默失效。
//	D-7 核实过：本轮全仓 grep ILIKE / strftime / NOW() 在**非测试代码**里零命中——
//	也就是说现在很干净，风险全在「将来有人写进去」。零命中的纪律没有闸门就等于没有，
//	所以本文件把「未覆盖的方言专属构造」钉成负向扫描：命中即红，并点名走 CurrentDialect() 分支。
//
// 判据形态（三条缺一不可，都是历史踩过的假绿形态）：
//
//	① 真源扫描：全仓非测试 .go，命中且不在豁免表 → 红；
//	② 量级守卫：扫到的**文件数**必须 ≥ 阈值，防「目录跳错／根路径写错」扫了个空还报绿；
//	③ 反证：同一套扫描函数喂合成样本，逐个构造都必须被抓到，防正则写坏变成恒不命中。
//	另外豁免表按「文件×构造」记**等值**条数（与 rawSQLAllowlist 同口径）：
//	存量下降同样红灯——文件被改名或整段被跳过时会静默放行，那是比新增更隐蔽的失效。
//
// ============================================================================
package db

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// uncoveredDialect 一条「ToDialect 不翻译」的方言专属构造。
// why 一栏直接写进失败信息：报错的人不需要先读源码就知道该改成什么形态。
type uncoveredDialect struct {
	name string
	why  string
	re   *regexp.Regexp
}

// uncoveredDialects 未覆盖构造清单。
// 收录标准只有一条：**本方言下会直接语法错或语义漂移**，且 ToDialect 不会替调用方改写。
// 反过来，ToDialect 已翻译的五类构造（AUTOINCREMENT/BLOB/REAL/INSERT OR IGNORE）
// 与两种方言都成立的写法（LIKE、|| 拼接、COALESCE、ON CONFLICT、IN、子查询）都**不在射程内**，
// 把它们算成违规只会让人绕过闸门。
var uncoveredDialects = []uncoveredDialect{
	{"ILIKE", "ILIKE 只有 PostgreSQL 有，SQLite 下直接语法错；需按 CurrentDialect() 分支（PG 用 ILIKE，SQLite 用 LIKE 配合已归一的小写列）",
		regexp.MustCompile(`\bILIKE\b`)},
	{"GLOB", "GLOB 是 SQLite 专属运算符，PG 下语法错；跨方言用 LIKE 或两边各自分支",
		regexp.MustCompile(`\bGLOB\s+['"]`)},
	{"strftime/julianday/datetime/date 函数族", "SQLite 日期函数在 PG 下不存在（PG 用 now()/date_trunc()/::date）；跨方言请在 Go 侧算好时间再传参，或按方言分支",
		regexp.MustCompile(`\b(strftime|julianday|datetime|date|time)\s*\(\s*['"]`)},
	{"IFNULL", "IFNULL 是 SQLite 写法，PG 没有该函数；一律用两方言都支持的 COALESCE",
		regexp.MustCompile(`\bIFNULL\s*\(`)},
	{"group_concat", "group_concat 为 SQLite 专有聚合，PG 侧是 string_agg（且分隔符位置不同）；跨方言需分支",
		regexp.MustCompile(`\bgroup_concat\s*\(`)},
	{"json_extract/json_each", "SQLite 的 JSON1 扩展函数，PG 用 ->/->>/jsonb_array_elements；跨方言需分支",
		regexp.MustCompile(`\bjson_(extract|each)\s*\(`)},
	{"string_agg", "string_agg 为 PostgreSQL 专有聚合，SQLite 下函数不存在；本仓以 SQLite 为真源，出现即说明漏了分支",
		regexp.MustCompile(`\bstring_agg\s*\(`)},
	{"NOW()", "NOW() 是 PG 函数，SQLite 下未定义（SQLite 用 CURRENT_TIMESTAMP/datetime('now')）；跨方言一律由 Go 侧传时间",
		regexp.MustCompile(`\bNOW\s*\(\s*\)`)},
	{"UNNEST/ARRAY[]", "PG 数组构造与展开语法，SQLite 无对应物（本仓不用数组列）；出现即说明照抄了 PG 方言",
		regexp.MustCompile(`\b(UNNEST\s*\(|ARRAY\s*\[)`)},
	{":: 类型转换", "`x::type` 是 PG 专用转型语法，SQLite 下解析失败；跨方言用 CAST(x AS type)",
		regexp.MustCompile(`\w\s*::\s*(text|jsonb|json|integer|int|bigint|numeric|decimal|float|double|boolean|bool|date|timestamp|timestamptz|uuid|varchar|char|serial|bigserial)\b`)},
	{"INSERT OR REPLACE/ROLLBACK/ABORT/FAIL", "ToDialect 只译 INSERT OR IGNORE；其余 OR 冲突策略在 PG 下语法错（PG 用 ON CONFLICT DO UPDATE）",
		regexp.MustCompile(`(?i)INSERT\s+OR\s+(REPLACE|ROLLBACK|ABORT|FAIL)\b`)},
	{"SERIAL/BIGSERIAL 直写", "自增列一律写 SQLite 真源（INTEGER PRIMARY KEY AUTOINCREMENT）交给 ToDialect 译成 BIGSERIAL；业务侧直写 SERIAL 会绕过翻译层",
		regexp.MustCompile(`\bBIG?SERIAL\b`)},
	{"PRAGMA", "PRAGMA 为 SQLite 专属元数据语句，PG 下语法错；必须包在方言分支里（见 store.TicketQualityFlaggedMigrate），并按 D-7 豁免表逐条登记",
		regexp.MustCompile(`\bPRAGMA\b`)},
}

// dialectScanExempt 已点名的合法豁免：键为「相对路径#构造名」，值为等值条数。
// 每一档都必须是「已经按方言分支包好」或「该文件本身就是方言层／只跑单一方言的工具」，
// **禁止**为了过闸而加豁免——新增豁免要写清它被哪种方言保护着。
var dialectScanExempt = map[string]int{
	// PRAGMA 全部落在「PG 分支已提前 return」的迁移路径里，即 SQLite 专属段：
	//   四处调用点都写作 db.Query(..., db.CurrentDialect(), "PRAGMA ...")，
	//   但其上方都有 `if d == db.DialectPostgres { …; return }`（或 artifacts 的 `if d == DialectSQLite {`）包住，
	//   PG 下这些语句根本不会执行——所以它们是「按方言分支的合法 SQLite 侧探测」，不是漏分支。
	// ⚠️ 本表的等值锁同时是**复查提醒**：这些函数一旦被人挪出方言分支（PG 也会跑到），
	//   命中数会从 0 变成非 0 或键名变化，闸门立刻红灯，届时必须改走 db.HasColumn/db.UniqueColumnSets。
	"internal/store/packages.go#PRAGMA":       3, // PackagesTenantMigrate：PG 段（DROP CONSTRAINT/CREATE UNIQUE INDEX）已 return，此处仅 SQLite 探测列与索引
	"internal/store/artifacts.go#PRAGMA":      2, // ArtifactsMigrate 的 `if d == DialectSQLite {` 块内：index_list/index_info 探测
	"internal/kb/db.go#PRAGMA":                7, // ensurePackScopeUnique / rebuildTableWithTripleUnique / rebuildUniqueIndex / isSingleUniqueOnZhHash：均在 PG 早退之后的 SQLite 重建路径
	"cmd/migrate-sqlite-to-pg/main.go#PRAGMA": 1, // 一次性搬迁工具：源端**只读 SQLite**，用 PRAGMA 取列名是它的本职
}

// dialectScanSkipDirs 与 guard_test.go 同口径：vendor/构建产物/assist 子服务不参与双方言纪律。
var dialectScanSkipDirs = map[string]bool{"vendor": true, ".git": true, "node_modules": true, "assist": true}

// dialectHit 一次命中（供失败信息聚合与计数）。
type dialectHit struct {
	rel     string
	line    int
	pattern string
	text    string
}

// scanDialectSources 对「相对路径 → 文件内容」执行构造扫描。
// 单独抽出来是为了让③反证能喂合成样本——真仓扫描永远抓不到「正则写坏」这一类失效，
// 而正则写坏的形态恰恰是恒不命中（红灯永远不亮的那种绿）。
// 参数：sources=路径→源码；返回命中列表与扫过的文件数。
func scanDialectSources(sources map[string]string) ([]dialectHit, int) {
	var hits []dialectHit
	files := 0
	for rel, content := range sources {
		if rel == "internal/db/dialect_scan_test.go" {
			continue // 本文件里的正则字面量就是构造清单本身
		}
		files++
		// 行注释不算命中（历史说明里会写「PG 用 ILIKE」这类对比句），与本包 guard_test.go 同口径
		code := stripLineComments(content)
		for i, line := range strings.Split(code, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			for _, u := range uncoveredDialects {
				if u.re.MatchString(line) {
					hits = append(hits, dialectHit{rel: rel, line: i + 1, pattern: u.name, text: trimmed})
				}
			}
		}
	}
	return hits, files
}

// stripLineComments 逐行剥掉以 // 开头的整行注释（保留行号，命中位置才指向真实代码行）。
func stripLineComments(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n")
}

// collectNonTestGoSources 收集仓库内非测试 .go 源码（路径相对 module 根）。
// 本包（internal/db）整体豁免：dialect.go/rewrite.go/query.go 就是方言层，
// 它「写方言」正是它的职责，扫它等于要求自己翻译自己。
func collectNonTestGoSources(t *testing.T) map[string]string {
	t.Helper()
	root := findRepoGoRoot(t)
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 个别目录无权限直接跳过，不影响其余扫描（与 guard_test.go 一致）
		}
		if info.IsDir() {
			if dialectScanSkipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		if strings.HasPrefix(rel, "internal/db"+string(filepath.Separator)) {
			return nil // 方言层自身
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	return out
}

// TestNoUncoveredDialectConstructs D-7 主锁：非测试源码里不得出现「翻译层不覆盖」的方言构造，
// 已登记的豁免按「文件#构造」等值计数（多了＝新违规，少了＝基线该下调，两向都红）。
func TestNoUncoveredDialectConstructs(t *testing.T) {
	sources := collectNonTestGoSources(t)
	hits, files := scanDialectSources(sources)

	// ② 量级守卫：扫描面本身要是空的，后面所有判据都恒真（本仓历史踩过「目录当参数喂」的恒空假绿）
	if files < 200 {
		t.Fatalf("方言扫描只覆盖到 %d 个非测试 .go 文件（本仓量级应 ≥200）—— ⇒ 根路径/跳过目录写错，本闸门正在空转", files)
	}

	perKey := map[string]int{}
	var offenders []string
	for _, h := range hits {
		key := h.rel + "#" + h.pattern
		perKey[key]++
		if _, ok := dialectScanExempt[key]; !ok {
			offenders = append(offenders, fmt.Sprintf("%s:%d [%s]: %s", h.rel, h.line, h.pattern, h.text))
		}
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		var why []string
		for _, u := range uncoveredDialects {
			why = append(why, "  - "+u.name+": "+u.why)
		}
		t.Errorf("发现 %d 处「ToDialect 不翻译」的方言专属构造（SQLite/PG 必有一侧语法错或语义漂移）:\n%s\n修法口径：\n%s",
			len(offenders), strings.Join(offenders, "\n"), strings.Join(why, "\n"))
	}
	// 等值锁（非单向）：豁免条目存量减少也必须显式改基线，防止文件改名/被跳过导致静默放行
	for key, want := range dialectScanExempt {
		if got := perKey[key]; got != want {
			t.Errorf("豁免条目 %s got=%d want=%d —— 增是违规，减说明代码被删或扫描面变了，都要就地改基线并附理由", key, got, want)
		}
	}
}

// TestDialectScanPatternsActuallyFire ★ 反证：合成样本逐构造必须被抓到。
// 没有这条，上一节的绿灯无法区分「仓库真干净」与「正则写坏了所以永不命中」。
// 只在内存里跑，不落盘、不碰仓库。
func TestDialectScanPatternsActuallyFire(t *testing.T) {
	// one 构造一条必然命中的 SQL 字面量（键与 uncoveredDialects 的 name 一一对应）
	samples := map[string]string{
		"ILIKE": `q := "SELECT id FROM users WHERE name ILIKE $1"`,
		"GLOB":  `q := "SELECT id FROM t WHERE path GLOB 'a*'"`,
		"strftime/julianday/datetime/date 函数族": `q := "SELECT datetime('now','localtime') FROM t"`,
		"IFNULL":                                `q := "SELECT IFNULL(a,0) FROM t"`,
		"group_concat":                          `q := "SELECT group_concat(tag,',') FROM t"`,
		"json_extract/json_each":                `q := "SELECT json_extract(meta,'$.k') FROM t"`,
		"string_agg":                            `q := "SELECT string_agg(tag, ',') FROM t"`,
		"NOW()":                                 `q := "UPDATE t SET seen_at = NOW()"`,
		"UNNEST/ARRAY[]":                        `q := "SELECT * FROM UNNEST(ARRAY['a','b']) AS x"`,
		":: 类型转换":                               `q := "SELECT meta::jsonb FROM t"`,
		"INSERT OR REPLACE/ROLLBACK/ABORT/FAIL": `q := "INSERT OR REPLACE INTO t (a) VALUES (1)"`,
		"SERIAL/BIGSERIAL 直写":                   `ddl := "CREATE TABLE t (id BIGSERIAL PRIMARY KEY)"`,
		"PRAGMA":                                `_, _ = conn.Exec("PRAGMA table_info(t)")`,
	}
	for name, src := range samples {
		hits, files := scanDialectSources(map[string]string{"synthetic/x.go": src})
		if files != 1 {
			t.Fatalf("合成样本应被计入扫描面（files=%d）⇒ 扫描面对象本身错了", files)
		}
		var got []string
		for _, h := range hits {
			got = append(got, h.pattern)
		}
		found := false
		for _, g := range got {
			if g == name {
				found = true
			}
		}
		if !found {
			t.Errorf("构造 %q 的正则没抓到合成命中样本：%s（hits=%v）—— ⇒ 主闸门属于恒不命中的假绿", name, src, got)
		}
	}
	// 反向对照：两方言都成立的写法与已覆盖构造不许被误判（否则豁免表会被迫越写越长，闸门失去信号）
	clean := `// SELECT x ILIKE 'y' 注释里出现不算命中
package x
func f() {
	_ = "SELECT a, COALESCE(b,0) FROM t WHERE name LIKE '%' || $1 || '%' ON CONFLICT DO NOTHING"
	_ = "CREATE TABLE t (id INTEGER PRIMARY KEY AUTOINCREMENT, body BLOB, score REAL)"
	_ = "INSERT OR IGNORE INTO t (a) VALUES (1)"
}`
	if hits, _ := scanDialectSources(map[string]string{"synthetic/clean.go": clean}); len(hits) != 0 {
		t.Errorf("已覆盖构造/通用写法被误判为违规：%+v —— 说明正则射程过宽", hits)
	}
}
