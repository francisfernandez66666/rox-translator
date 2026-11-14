package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// KN5/KN6（施工图 §5 / §4.3.5）：主服务 api 层零知识表直读直写。
// 扫描 internal/api 下全部非测试 .go 源文件，不得出现对四张知识表
// （products / product_facts / product_assets / product_fact_i18n）的
// FROM / INSERT / UPDATE / DELETE。这两条守卫拦住「顺手在主服务直连主库知识表」的回潮
// （W3 之后知识表归知识服务进程所有，主服务只经 knowledge 客户端契约访问）。
//
// 变异体（由 gate_mutants.sh 注入）把一条带 forbidden 子串的注释塞进某 api 源文件 ⇒ 本测试必红：
//   M0287（KN5）：注入 `FROM products ` ⇒ 直读计数 > 0 ⇒ 红；
//   M0288（KN6）：注入 `INSERT INTO products` ⇒ 直写计数 > 0 ⇒ 红。
//
// 注：扫描按「原始子串出现次数」判定，注释与字符串字面量中的 forbidden 子串同样计入——
// 这是刻意保守的口径（任何带该子串的落库 SQL 草稿都不该出现在主服务 api 层）。

var knReadPatterns = []string{
	"FROM products ", "FROM product_facts ", "FROM product_assets ", "FROM product_fact_i18n ",
	"from products ", "from product_facts ", "from product_assets ", "from product_fact_i18n ",
}
var knWritePatterns = []string{
	"INSERT INTO products", "INSERT INTO product_facts", "INSERT INTO product_assets", "INSERT INTO product_fact_i18n",
	"UPDATE products", "UPDATE product_facts", "UPDATE product_assets", "UPDATE product_fact_i18n",
	"DELETE FROM products", "DELETE FROM product_facts", "DELETE FROM product_assets", "DELETE FROM product_fact_i18n",
}

// knCount 扫描当前目录（internal/api 包根）下所有非测试 .go 源文件，累加给定模式的命中数。
func knCount(patterns []string) (int, error) {
	count := 0
	err := filepath.WalkDir(".", func(p string, e os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if e.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		s := string(b)
		for _, pat := range patterns {
			count += strings.Count(s, pat)
		}
		return nil
	})
	return count, err
}

// TestKN5NoDirectKnowledgeRead 主服务 api 层不得出现对四张知识表的直读（FROM）。
func TestKN5NoDirectKnowledgeRead(t *testing.T) {
	n, err := knCount(knReadPatterns)
	if err != nil {
		t.Fatalf("KN5 扫描 internal/api 失败: %v", err)
	}
	if n != 0 {
		t.Fatalf("KN5 主服务 api 层不得直读知识表，实际命中 %d 处（注入 `FROM products ` 即红，M0287）", n)
	}
}

// TestKN6NoDirectKnowledgeWrite 主服务 api 层不得出现对四张知识表的直写（INSERT/UPDATE/DELETE）。
func TestKN6NoDirectKnowledgeWrite(t *testing.T) {
	n, err := knCount(knWritePatterns)
	if err != nil {
		t.Fatalf("KN6 扫描 internal/api 失败: %v", err)
	}
	if n != 0 {
		t.Fatalf("KN6 主服务 api 层不得直写知识表，实际命中 %d 处（注入 `INSERT INTO products` 即红，M0288）", n)
	}
}
