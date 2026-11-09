// ============ 本文件职责中文说明 ============
// 二期 §5 配置种子与 L1 全集守卫：种子幂等且运营改动优先、中英两档都是真词；
// 引擎包生产源码里配置词表字面必须归 0，白名单只允许配置层文件（含射程自证与反证）。
// ========================================
package salesflow

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/db"
	"translator/internal/store"
)

// newSeedTestStore 用文件库建 Store 并跑词表域迁移。
// 参数：t=测试句柄；返回：可用 Store。
func newSeedTestStore(t *testing.T) *store.Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	conn, err := db.Open(db.Config{Driver: db.DriverSQLite, DSN: filepath.Join(t.TempDir(), "seed.db")})
	if err != nil {
		t.Fatalf("文件库建连失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	st, err := store.New(conn)
	if err != nil {
		t.Fatalf("store.New 失败: %v", err)
	}
	if err := st.SalesLexiconMigrate(); err != nil {
		t.Fatalf("词表域迁移失败: %v", err)
	}
	return st
}

// S1：种子全集覆盖全部类别，重复种入不增行，且绝不覆盖运营改过的平台档。
func TestSeedPlatformLexiconIsIdempotentAndRespectsOperatorEdits(t *testing.T) {
	st := newSeedTestStore(t)
	if err := SeedPlatformLexicon(st); err != nil {
		t.Fatalf("首次种入失败: %v", err)
	}
	rows, err := st.ListLexicon(1)
	if err != nil {
		t.Fatalf("列表查询失败: %v", err)
	}
	if len(rows) != len(PlatformLexiconSeed) {
		t.Fatalf("S1：平台档应恰好 %d 行，实得 %d", len(PlatformLexiconSeed), len(rows))
	}
	if len(LexiconKinds()) != 10 {
		t.Fatalf("S1：类别全集必须 10 项，实得 %d", len(LexiconKinds()))
	}
	if err := SeedPlatformLexicon(st); err != nil {
		t.Fatalf("二次种入失败: %v", err)
	}
	rows2, err := st.ListLexicon(1)
	if err != nil || len(rows2) != len(rows) {
		t.Fatalf("S1：重复种入不许增行，实得 %d err=%v", len(rows2), err)
	}
	if err := st.UpsertLexicon(0, SceneCrossBorder, "zh", KindAnchorGreeting, []string{"运营改过的招呼"}); err != nil {
		t.Fatalf("运营改档失败: %v", err)
	}
	if err := SeedPlatformLexicon(st); err != nil {
		t.Fatalf("改档后再种入失败: %v", err)
	}
	words, err := st.LoadLexiconWords(1, SceneCrossBorder, "zh", KindAnchorGreeting)
	if err != nil || len(words) != 1 || words[0] != "运营改过的招呼" {
		t.Fatalf("S1：种子不许覆盖运营改动，实得 %v err=%v", words, err)
	}
	en, err := st.LoadLexiconWords(1, SceneCrossBorder, "en", KindAnchorGreeting)
	if err != nil || len(en) < 3 || en[0] != "hi" {
		t.Fatalf("S1：英文档必须是真英文词，实得 %v err=%v", en, err)
	}
}

// S2：引擎读口 fail-closed（tid<=0 拒收），正常租户能读到种子词。
func TestLoadLexiconRequiresTenant(t *testing.T) {
	st := newSeedTestStore(t)
	if err := SeedPlatformLexicon(st); err != nil {
		t.Fatalf("种入失败: %v", err)
	}
	if _, err := LoadLexicon(context.Background(), st, 0, SceneCrossBorder, "zh", KindAnchorIntent); err == nil {
		t.Fatal("S2：tid<=0 必须拒收")
	}
	words, err := LoadLexicon(context.Background(), st, 1, SceneCrossBorder, "zh", KindAnchorIntent)
	if err != nil || len(words) == 0 {
		t.Fatalf("S2：租户必须读得到平台种子词，实得 %v err=%v", words, err)
	}
}

// configLayerWhitelist 配置层允许出现词表字面的文件（白名单以外一律按引擎侧判）。
// 参数：无；返回：相对 internal/ 的白名单路径。
func configLayerWhitelist() map[string]bool {
	return map[string]bool{
		"salesflow/lexicon_seed.go": true,
		"salesflow/scenes.go":       true,
	}
}

// sectionFiveBannedWords §5.1 曾在引擎里被手抄 5 份的中文词表样本（按全集判，不许清空判绿）。
// 参数：无；返回：禁词全集。
func sectionFiveBannedWords() []string {
	return []string{"到店", "试驾", "预算不够", "超出预算", "太贵", "好哒", "不满意", "极" + "石"}
}

// scanEngineLayer 扫引擎层生产源码（跳过测试与白名单），返回命中与扫描文件数。
// 参数：root=仓根；dirs=目录全集；banned=禁词；返回：命中、扫描数、错误。
func scanEngineLayer(root string, dirs, banned []string) ([]string, int, error) {
	var hits []string
	scanned := 0
	for _, dir := range dirs {
		base := filepath.Join(root, "internal", dir)
		entries, err := os.ReadDir(base)
		if err != nil {
			return nil, scanned, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			rel := dir + "/" + e.Name()
			if configLayerWhitelist()[rel] {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(base, e.Name()))
			if err != nil {
				return nil, scanned, err
			}
			scanned++
			for i, line := range strings.Split(string(raw), "\n") {
				code := line
				if at := strings.Index(code, "//"); at >= 0 {
					code = code[:at]
				}
				for _, lit := range banned {
					if lit != "" && strings.Contains(code, lit) {
						hits = append(hits, rel+":"+strconv.Itoa(i+1))
						break
					}
				}
			}
		}
	}
	return hits, scanned, nil
}

// L1 全集：引擎侧零词表字面；扫描集下限自证防目录写漂。
func TestEngineLayerHasNoLexiconLiterals(t *testing.T) {
	banned := sectionFiveBannedWords()
	if len(banned) < 6 {
		t.Fatalf("L1 禁词表被清空：只有 %d 项 ⇒ 本闸失明", len(banned))
	}
	dirs := []string{"salesflow", "strategytypes", "mq", "attribution", "talkmining", "state_machine"}
	hits, scanned, err := scanEngineLayer(filepath.Join("..", ".."), dirs, banned)
	if err != nil {
		t.Fatalf("L1 扫描失败: %v", err)
	}
	if scanned < 8 {
		t.Fatalf("L1 射程自证失败：只扫到 %d 个生产文件", scanned)
	}
	if len(hits) != 0 {
		t.Fatalf("L1：引擎侧仍有配置词表字面（只许在配置种子文件里）: %v", hits)
	}
}

// L1 反证：扫描器必须真看得见一处字面，且白名单确实生效。
func TestEngineLayerScannerHasTeeth(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "internal", "mq")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("反证夹具建目录失败: %v", err)
	}
	bad := "package mq\n\nvar words = []string{\"到店\"}\n"
	if err := os.WriteFile(filepath.Join(base, "bad.go"), []byte(bad), 0o644); err != nil {
		t.Fatalf("反证夹具写入失败: %v", err)
	}
	hits, scanned, err := scanEngineLayer(dir, []string{"mq"}, sectionFiveBannedWords())
	if err != nil {
		t.Fatalf("反证扫描失败: %v", err)
	}
	if scanned != 1 || len(hits) != 1 {
		t.Fatalf("L1 反证必须恰好 1 文件 1 命中，实得 files=%d hits=%v", scanned, hits)
	}
	if !configLayerWhitelist()["salesflow/lexicon_seed.go"] || !configLayerWhitelist()["salesflow/scenes.go"] {
		t.Fatal("L1：配置层白名单必须含种子与场景配置两份文件")
	}
}
