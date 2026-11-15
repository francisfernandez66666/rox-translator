// ============ no_direct_knowledge_gate_test.go · KN5/KN6 验收闸门 ============
// 四期 W3 / KN5+KN6：主服务 api 层零知识表直读直写（施工图 §5 / §4.3.5）。
//
// 扫描 internal/api 下全部非测试 .go 源文件，不得出现对四张知识表
// （products / product_facts / product_assets / product_fact_i18n）的 FROM / INSERT / UPDATE / DELETE。
// 这两条守卫拦住「顺手在主服务直连主库知识表」的回潮（W3 之后知识表归知识服务进程所有，
// 主服务只经 knowledge 客户端契约访问）。
//
// ★ 2026-11-15 收口（审计第 10 条）——原形态有两个可以静默通过的洞：
//
//	① 只按**字面子串**数：`FROM  products`（双空格）、`FROM\nproducts`、`FROM\tproducts`
//	   全部漏过 —— 而仓里 SQL 恰恰经常跨行写；
//	② 没有「扫了几个文件」的下界：`knCount` 走错目录、或调用方改了包路径时得到 0 命中，
//	   屏幕上是绿的，与「真的很干净」同形（同 AGENTS §二「喂目录得 0 文件＝恒空假绿」）。
//	修法：扫描前做**空白归一**（所有空白run → 单空格），并强制返回文件数与下界。
//
// 变异体（由 gate_mutants.sh 注入）：
//
//	M0287（KN5）：注入 `FROM products ` ⇒ 直读计数 > 0 ⇒ 红；
//	M0288（KN6）：注入 `INSERT INTO products` ⇒ 直写计数 > 0 ⇒ 红；
//	M0355（KN5）：注入 `FROM  products`（**双空格**）⇒ 归一后仍命中 ⇒ 红（原形态必绿）。
//
// 注：扫描按「原始子串出现次数」判定，注释与字符串字面量中的 forbidden 子串同样计入——
// 这是刻意保守的口径（任何带该子串的落库 SQL 草稿都不该出现在主服务 api 层）。
// ============================================================
package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var knReadPatterns = []string{
	"FROM products ", "FROM product_facts ", "FROM product_assets ", "FROM product_fact_i18n ",
	"from products ", "from product_facts ", "from product_assets ", "from product_fact_i18n ",
}
var knWritePatterns = []string{
	"INSERT INTO products", "INSERT INTO product_facts", "INSERT INTO product_assets", "INSERT INTO product_fact_i18n",
	"UPDATE products", "UPDATE product_facts", "UPDATE product_assets", "UPDATE product_fact_i18n",
	"DELETE FROM products", "DELETE FROM product_facts", "DELETE FROM product_assets", "DELETE FROM product_fact_i18n",
}

// knWSRun 任意空白 run（含换行/制表/多空格）。
var knWSRun = regexp.MustCompile(`\s+`)

// normalizeSQL 把一段源码的空白归一成单空格，让 `FROM  products` / `FROM\nproducts`
// 与 `FROM products` 同形。模式串自身也要过这一道，否则两边永远对不上。
func normalizeSQL(s string) string { return knWSRun.ReplaceAllString(s, " ") }

// knCount 扫描当前目录（internal/api 包根）下所有非测试 .go 源文件，
// 累加给定模式的命中数，并返回**扫描到的文件数**（射程自证用）。
func knCount(patterns []string) (hits int, files int, err error) {
	normPatterns := make([]string, len(patterns))
	for i, p := range patterns {
		normPatterns[i] = normalizeSQL(p)
	}
	err = filepath.WalkDir(".", func(p string, e os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if e.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		files++
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		s := normalizeSQL(string(b))
		for _, pat := range normPatterns {
			hits += strings.Count(s, pat)
		}
		return nil
	})
	return hits, files, err
}

// knMinScannedFiles 射程下界（现读 internal/api 非测试 .go 在 120 上下）。
// 这是「比清单短一大截」的探测器，不是镜像：它只拦「扫描面塌了」，
// 不拦合法的文件增删（AGENTS §一·14 那条口径）。
const knMinScannedFiles = 100

// TestKN5NoDirectKnowledgeRead 主服务 api 层不得出现对四张知识表的直读（FROM）。
func TestKN5NoDirectKnowledgeRead(t *testing.T) {
	n, files, err := knCount(knReadPatterns)
	if err != nil {
		t.Fatalf("KN5 扫描 internal/api 失败: %v", err)
	}
	if files < knMinScannedFiles {
		t.Fatalf("KN5 射程失明：只扫到 %d 个非测试 .go 文件（下界 %d）—— 扫描面塌了，绿是假的",
			files, knMinScannedFiles)
	}
	if n != 0 {
		t.Fatalf("KN5 主服务 api 层不得直读知识表，实际命中 %d 处（注入 `FROM products ` 即红，M0287）", n)
	}
}

// TestKN6NoDirectKnowledgeWrite 主服务 api 层不得出现对四张知识表的直写（INSERT/UPDATE/DELETE）。
func TestKN6NoDirectKnowledgeWrite(t *testing.T) {
	n, files, err := knCount(knWritePatterns)
	if err != nil {
		t.Fatalf("KN6 扫描 internal/api 失败: %v", err)
	}
	if files < knMinScannedFiles {
		t.Fatalf("KN6 射程失明：只扫到 %d 个非测试 .go 文件（下界 %d）—— 扫描面塌了，绿是假的",
			files, knMinScannedFiles)
	}
	if n != 0 {
		t.Fatalf("KN6 主服务 api 层不得直写知识表，实际命中 %d 处（注入 `INSERT INTO products` 即红，M0288）", n)
	}
}

// TestKN5KN6WhitespaceNormalizationHasTeeth 空白归一的**进程内**反证：
// 三种「原形态必绿」的写法在归一后必须判命中。这条不依赖变异体，跑在本测试自己的进程里，
// 所以哪怕 gate_mutants 没跑，归一腿被摘掉也会立刻红。
func TestKN5KN6WhitespaceNormalizationHasTeeth(t *testing.T) {
	readTails := []string{
		"SELECT * FROM products WHERE id=?",        // 基准形态（原形态也能抓）
		"SELECT * FROM  products WHERE id=?",       // 双空格
		"SELECT * FROM\nproducts WHERE id=?",       // 换行
		"SELECT *\n  FROM\tproducts\n  WHERE id=?", // 混合缩进
	}
	for _, src := range readTails {
		norm := normalizeSQL(src)
		hit := false
		for _, pat := range knReadPatterns {
			if strings.Contains(norm, normalizeSQL(pat)) {
				hit = true
				break
			}
		}
		if !hit {
			t.Fatalf("KN5 空白归一失效：%q 归一成 %q 后仍不命中任何模式 ⇒ 新的一行写法可绕过闸门", src, norm)
		}
	}
	// 负向对照：`products_old` 之类不该被误伤（否则闸门会逼人改名而不是改 SQL）。
	fp := normalizeSQL("SELECT * FROM products_old WHERE id=?")
	for _, pat := range knReadPatterns {
		if strings.Contains(fp, normalizeSQL(pat)) {
			t.Fatalf("KN5 误伤：products_old 被判成知识表 %q", pat)
		}
	}
}
