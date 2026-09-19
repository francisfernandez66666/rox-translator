// ============================================================================
// langs_zh_test.go — ★ #23 语言列表接口回归（2026-09-19）
// 锁定契约：/api/translation/langs 在 34 个 KB 语言之后必须追加 zh（简体中文）
// 条目并带 kb=false——它是「外语→简体中文」方向的前端目标语言来源；
// zh 不进 TranslateLangs（KB/TM 列契约），后端 SplitOptions 自动归入纯模型直翻链。
// 运行：go test ./internal/api/ -run TestTranslationLangs -v
// ============================================================================
package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"

	"translator/internal/config"
	"translator/internal/store"
)

func TestTranslationLangsIncludesZhTarget(t *testing.T) {
	// ★ 方言钉死（2026-09-20 UAT PG 矩阵假红教训）：config.Default() 读 DB_DRIVER 环境变量并
	// 把全局 config.C 置为 postgres，泄漏给按文件序在其后执行的 lead/ops/points 等全部
	// 内存 SQLite 测试族（migrateColumnsPG 在 SQLite 上查 information_schema 直接炸）。
	// 与 admin_models_cache_test/h5_chunk_test 同法：显式钉 sqlite 并在结束后还原。
	oldCfg := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = oldCfg })
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	defer db.Close()
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	s := &Server{Store: st, Cfg: cfg}

	req := httptest.NewRequest(http.MethodGet, "/api/translation/langs", nil)
	w := httptest.NewRecorder()
	s.handleTranslationLangs(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("应 200，实际 %d", w.Code)
	}
	var body struct {
		KbLangs []map[string]string `json:"kb_langs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	// 34 KB 语言 + 末尾 zh = 35 条，且逐条带 kb 标记
	if len(body.KbLangs) != len(config.TranslateLangs)+1 {
		t.Fatalf("条目数 %d，期望 %d", len(body.KbLangs), len(config.TranslateLangs)+1)
	}
	last := body.KbLangs[len(body.KbLangs)-1]
	if last["code"] != "zh" || last["kb"] != "false" {
		t.Errorf("末条应为 zh/kb=false，实际 %v", last)
	}
	if last["name"] != "简体中文" || last["name_en"] != "Chinese (Simplified)" {
		t.Errorf("zh 展示名缺失（LangNames/LangNamesEn 未同步）: %v", last)
	}
	for _, l := range body.KbLangs[:len(body.KbLangs)-1] {
		if l["kb"] != "true" {
			t.Errorf("KB 语言 %s 缺 kb=true 标记", l["code"])
			break
		}
	}
}
