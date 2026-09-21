// ============ health_probes_test.go · 职责说明 ============
// ★ #42（2026-09-22）探针拆分（/livez + /readyz）的 httptest 单测，锁住三条容易回退的语义：
//
//	① /livez 零依赖：即使 Store 为 nil、即使 DB 已关闭，也必须恒 200
//	   （否则一次依赖抖动就会让编排器重启本可自愈的进程——正是本次拆分要修的故障）；
//	② /readyz 真探依赖：Store 未装配 / DB 不可达 → 503 且 body 点名 store；
//	   内存 SQLite 可达 → 200 且 store=ok、distributed 为已登记的状态词；
//	③ 失败原因只出粗粒度状态词，绝不把驱动错误原文（含主机/文件路径）写进匿名可访问的响应。
//
// 方言自钉（AGENTS.md 一.4）：本文件用例会经 store.New 走 db 层，必须自己钉 sqlite，
// 否则 run_uat 的 PG 模式下 config.C 会泄漏方言给本包后续内存 SQLite 用例（历史两连败的假红）。
// =============================================
package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"translator/internal/config"
	"translator/internal/store"
)

// pinSqliteDialect 自钉 sqlite 方言并在用例结束后复原全局 config.C（AGENTS.md 一.4 模板）。
func pinSqliteDialect(t *testing.T) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
}

// newProbeStoreServer 起一个只装配平台存储（内存 SQLite）的 Server，供依赖探针用例复用。
func newProbeStoreServer(t *testing.T) *Server {
	t.Helper()
	pinSqliteDialect(t)
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.New(sqlDB)
	if err != nil {
		sqlDB.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return &Server{Store: st, Cfg: config.C}
}

// decodeProbeBody 解析探针出参。
func decodeProbeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("探针出参非法 JSON: %v (%s)", err, rec.Body.String())
	}
	return body
}

// TestLivezNeverFailsOnDependencies 存活探针恒 200：Store 未装配、DB 已关闭两种坏境都要能过。
func TestLivezNeverFailsOnDependencies(t *testing.T) {
	cases := []struct {
		name string
		s    *Server
	}{
		{"空Server", &Server{}},
		{"DB已关闭", func() *Server {
			s := newProbeStoreServer(t)
			_ = s.Store.DB().Close() // 模拟依赖故障（连接池已死）
			return s
		}()},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/livez", nil)
		rec := httptest.NewRecorder()
		c.s.handleLivez(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s：/livez 应恒 200，实际 %d（依赖故障不该让存活探针翻红）", c.name, rec.Code)
			continue
		}
		body := decodeProbeBody(t, rec)
		if body["status"] != "ok" {
			t.Errorf("%s：/livez status 应为 ok，出参 %s", c.name, rec.Body.String())
		}
		// 存活探针必须零依赖信息：出现 store/distributed 字段说明有人往里加了依赖检查。
		for _, forbidden := range []string{"store", "distributed", "failed_dependencies"} {
			if _, ok := body[forbidden]; ok {
				t.Errorf("%s：/livez 出参不应含 %q（存活探针只做零依赖检查，见 health_probes.go 文件头）", c.name, forbidden)
			}
		}
	}
}

// TestReadyzFailsWhenStoreUnreachable 依赖缺失：未装配与库不可达都必须 503 并点名 store。
func TestReadyzFailsWhenStoreUnreachable(t *testing.T) {
	t.Run("未装配", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		rec := httptest.NewRecorder()
		(&Server{}).handleReadyz(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("Store 未装配应 503，实际 %d (%s)", rec.Code, rec.Body.String())
		}
		body := decodeProbeBody(t, rec)
		if body["status"] != "not_ready" || body["store"] != "not_initialized" {
			t.Errorf("出参口径不符：%s", rec.Body.String())
		}
		assertFailedDependency(t, body, "store")
	})
	t.Run("库不可达", func(t *testing.T) {
		s := newProbeStoreServer(t)
		if err := s.Store.DB().Close(); err != nil { // 关闭后 PingContext 报 "sql: database is closed"
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		rec := httptest.NewRecorder()
		s.handleReadyz(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("库不可达应 503，实际 %d (%s)", rec.Code, rec.Body.String())
		}
		body := decodeProbeBody(t, rec)
		if body["store"] != "unreachable" {
			t.Errorf("store 状态词应为 unreachable，出参 %s", rec.Body.String())
		}
		assertFailedDependency(t, body, "store")
		// ★ 泄漏守卫：/readyz 匿名可达，驱动错误原文（sql: database is closed 等）不得进 body。
		if strings.Contains(strings.ToLower(rec.Body.String()), "sql") {
			t.Errorf("出参疑似泄漏驱动错误原文：%s", rec.Body.String())
		}
	})
}

// TestReadyzReadyWithLocalStore 依赖齐备：内存 SQLite 可达即 200，且 distributed 只出状态词。
func TestReadyzReadyWithLocalStore(t *testing.T) {
	s := newProbeStoreServer(t)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	s.handleReadyz(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("库可达应 200，实际 %d (%s)", rec.Code, rec.Body.String())
	}
	body := decodeProbeBody(t, rec)
	if body["status"] != "ready" || body["store"] != "ok" {
		t.Fatalf("出参口径不符：%s", rec.Body.String())
	}
	if _, ok := body["failed_dependencies"]; ok {
		t.Errorf("就绪时不应出现 failed_dependencies：%s", rec.Body.String())
	}
	// 未配置 REDIS_ADDR（单测进程未走 cmd/server 启动闸门）→ in-process/unknown 均视为就绪，
	// 这条断言把「in-process 算就绪」的决策钉住（改动必先红在这里，见文件头口径 2）。
	dist, _ := body["distributed"].(string)
	if dist != "in-process" && dist != "unknown" {
		t.Errorf("单测环境 distributed 预期 in-process/unknown，实际 %q（有人改了判定口径？）", dist)
	}
}

// assertFailedDependency 断言 failed_dependencies 数组里点名了指定依赖。
func assertFailedDependency(t *testing.T, body map[string]interface{}, dep string) {
	t.Helper()
	arr, ok := body["failed_dependencies"].([]interface{})
	if !ok {
		t.Fatalf("failed_dependencies 缺失或非数组：%v", body)
	}
	for _, v := range arr {
		if v == dep {
			return
		}
	}
	t.Errorf("failed_dependencies 未点名 %q：%v", dep, arr)
}
