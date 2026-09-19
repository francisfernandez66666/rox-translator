// ============================================================================
// admin_models_cache_test.go — ★ B5（方案 B Phase 2 同批必修）路由能力位透传回归
// 锁定的历史缺陷：handleModelsSave 旧版按 5 字段逐一手构 ProviderConfig 重建路由，
// 任何新增能力位（SupportsConstraints 曾被丢过、本轮新增 SupportsCache）都会
// 在保存链路被静默吞掉——管理台勾选后「看起来保存成功」，库里永远是 false。
// 断言：POST 带 supports_cache/supports_constraints 的路由 → 加密入库并可解密回读，
// 能力位逐位保真；掩码 Key 位置回填链不受透传改造影响。
// 运行：go test ./internal/api/ -run TestModelsSave -v
// ============================================================================
package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/store"
)

// newModelsSaveServer 内存 SQLite + 平台超管 JWT（同 superadmin scope 测试构造模式）。
func newModelsSaveServer(t *testing.T) (*Server, string) {
	t.Helper()
	// ★ 方言钉死（2026-09-19 UAT PG 矩阵教训）：config.Default() 会读 DB_DRIVER 环境变量并把
	// 全局 config.C 置为 postgres，而 store.New 的 migrateColumnsPG 据此走 PG 迁移路径，
	// 在内存 SQLite 上查 information_schema 直接炸——且污染会沿字母序泄漏给同包后续测试族
	// （admin_openapi_docs/superadmin_scope/bot/captcha/H10/H12c/H3 共 14 例）。
	// 本测试族固定内存 SQLite：显式钉方言并在结束后还原，与 engine/stagemodel_test.go 同法。
	oldCfg := config.C
	config.C = config.Default()
	config.C.DatabaseDriver = "sqlite"
	t.Cleanup(func() { config.C = oldCfg })
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	if err := st.EnsureAdmin(0, "admin", "hash-admin", "平台超管", ""); err != nil {
		t.Fatalf("创建超管失败: %v", err)
	}
	// 复用上方已钉方言的全局配置，避免再次调用 config.Default() 触发全局 C 污染
	s := &Server{Store: st, Cfg: config.C}
	all, _ := st.ListAllUsers()
	for _, u := range all {
		if u.Username == "admin" {
			tk, err := auth.Sign(u, time.Hour)
			if err != nil {
				t.Fatalf("签发超管 JWT 失败: %v", err)
			}
			return s, tk
		}
	}
	t.Fatal("未找到超管账号")
	return nil, ""
}

// TestModelsSaveRouteCapabilityPassthrough 能力位保存链逐位保真 + 掩码 Key 回填不误伤。
func TestModelsSaveRouteCapabilityPassthrough(t *testing.T) {
	s, token := newModelsSaveServer(t)

	// 第一次保存：两条路由均带明文 Key（建立 oldRoutes 基线，供掩码位对齐回填）
	seed, _ := json.Marshal(map[string]interface{}{
		"routes": []map[string]interface{}{
			{"provider": "deepseek", "api_base": "https://api.deepseek.com/v1", "api_key": "sk-real-ds", "model": "deepseek-chat", "weight": 80},
			{"provider": "siliconflow", "api_base": "https://api.siliconflow.cn/v1", "api_key": "sk-real-sf", "model": "tencent/Hunyuan-MT-7B", "weight": 20},
		},
	})
	req0 := httptest.NewRequest(http.MethodPost, "/api/admin/models", bytes.NewReader(seed))
	req0.Header.Set("Authorization", "Bearer "+token)
	w0 := httptest.NewRecorder()
	s.handleModelsSave(w0, req0)
	if w0.Code != http.StatusOK {
		t.Fatalf("种子保存应 200，实际 %d: %s", w0.Code, w0.Body.String())
	}

	// 第二次保存：同结构路由 + 能力位；第 2 条 Key 用掩码（= 未修改，须按位回填旧明文）
	body := map[string]interface{}{
		"routes": []map[string]interface{}{
			{"provider": "deepseek", "api_base": "https://api.deepseek.com/v1", "api_key": "sk-real-ds", "model": "deepseek-chat", "weight": 80,
				"supports_cache": true, "supports_constraints": false},
			{"provider": "siliconflow", "api_base": "https://api.siliconflow.cn/v1", "api_key": "sk-mask****old", "model": "tencent/Hunyuan-MT-7B", "weight": 20,
				"supports_cache": false, "supports_constraints": true},
		},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/models", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.handleModelsSave(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("保存应 200，实际 %d: %s", w.Code, w.Body.String())
	}

	// 回读库内密文配置并解密（与 loadRoutesDecrypted 同语义）
	stored, err := s.Store.GetConfig("model_routes")
	if err != nil || stored == "" {
		t.Fatalf("model_routes 未落库: %v", err)
	}
	got := s.loadRoutesDecrypted()
	if len(got) != 2 {
		t.Fatalf("期望 2 条路由，实际 %d", len(got))
	}
	// 能力位保真：第 1 条 cache=true/constraints=false；第 2 条反之
	if !got[0].SupportsCache || got[0].SupportsConstraints {
		t.Errorf("deepseek 路由能力位被吞: %+v", got[0])
	}
	if got[1].SupportsCache || !got[1].SupportsConstraints {
		t.Errorf("siliconflow 路由能力位被吞: %+v", got[1])
	}
	// 掩码 Key（第 2 条 sk-mask****old 未修改）→ 位置对齐回填旧明文，不写掩码入库
	if got[1].APIKey != "sk-real-sf" {
		t.Errorf("掩码 Key 回填失败: %q", got[1].APIKey)
	}
	if got[0].APIKey != "sk-real-ds" {
		t.Errorf("明文 Key 应原样保存: %q", got[0].APIKey)
	}
	// 热同步内存态与库内一致（引擎即时生效口径）
	if len(s.Cfg.ModelRoutes) != 2 || !s.Cfg.ModelRoutes[0].SupportsCache {
		t.Errorf("s.Cfg.ModelRoutes 热同步缺能力位: %+v", s.Cfg.ModelRoutes)
	}
	// 加密落库红线：库内不得出现明文 Key 裸串
	if bytes.Contains([]byte(stored), []byte("sk-real-ds")) {
		t.Errorf("库内路由含明文 Key（enc:v1: 加密链回归）")
	}
}
