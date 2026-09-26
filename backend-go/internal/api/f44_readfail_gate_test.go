// f44_readfail_gate_test.go —— F-44③ 行为闸门（〇-U 批 I-1 收尾，2026-09-27）。
//
// 背景：F-44 的事故链是「PG 方言错 → 读侧报错 → 调用方吞错当无修订 → 200 空集合」，
// 批 I-1 已把读侧改对（db.Query 包装器 + editor.go 不再 `edits, _ :=`），
// 并按「修复缺陷时同步补一条能复现的自动化断言」（AGENTS §三）落了 f44_pg_test.go（真库回归）。
// 但那一支锁的是 **store 层 SQL 方言**；**API 层吞错形态本身**没有断言覆盖——
// 谁把 `if err != nil { writeError(...500) }` 改回 `edits, _ :=`，所有现存测试照样绿。
// 本文件补的就是这一刀：构造一次**必然失败的读**（DROP 掉 translation_edits 表），
// 断言 handler 回带码 500 而不是 200 空集合。
//
// 反证口径：① 先跑一次健康态断言 200（否则「表根本没建好」也会让红测恒红，属结构性假红）；
// ② 再 DROP 表断言 500 + code=INTERNAL_ERROR + success=false；
// ③ 若有人回退成吞错，第 ② 步会拿到 200/segments 空 —— 正是本闸门要抓的形态。
package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/store"
)

// newF44Probe 内存 SQLite + 平台超管的最小服务栈（沿用 webhook 掩码探针的钉法）。
func newF44Probe(t *testing.T) (*Server, *sql.DB, string) {
	t.Helper()
	pinSqliteDialect(t)
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	st, err := store.New(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	super, err := st.CreateUser(0, "f44_super", "hash", "读侧吞错闸门超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := auth.Sign(super, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{Store: st}, sqlDB, tok
}

// callTicketSegments 直调 GET /api/editor/segments（handleTicketSegments），返回状态码与响应体。
func callTicketSegments(t *testing.T, s *Server, tok string, ticketID int64) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/editor/segments?id=%d&lang=en", ticketID), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.handleTicketSegments(rec, req)
	var out map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// TestTicketSegmentsReadFailIsHonestError F-44③：修订读侧失败必须如实 500，禁止吞成 200 空集合。
func TestTicketSegmentsReadFailIsHonestError(t *testing.T) {
	s, sqlDB, tok := newF44Probe(t)
	// 建一张纯文本工单（FilePath 空 → 走 extractTextSegments，不碰文件系统的其他表）
	tk, err := s.Store.CreateTicket(0, 1, "F-44 读侧闸门工单", "第一段。\n第二段。", "", "en")
	if err != nil {
		t.Fatalf("建工单失败: %v", err)
	}
	// ① 健康态基线：表在、读得到 → 200（钉死「本测不是结构性假红」）
	code, body := callTicketSegments(t, s, tok, tk.ID)
	if code != http.StatusOK {
		t.Fatalf("健康态应 200，实得 %d: %v", code, body)
	}
	if body["success"] != true {
		t.Fatalf("健康态 success 应为 true: %v", body)
	}
	// ② 构造必然失败的读：把 translation_edits 表直接拿掉（等价于 PG 方言报错的失败注入，
	//    但不依赖 PG——SQLite 下表不存在同样是 GetTranslationEdits 返回 error）。
	if _, err := sqlDB.Exec("DROP TABLE translation_edits"); err != nil {
		t.Fatalf("DROP TABLE 失败（表名口径变了？本闸门失去意义，先查 store/edits.go）: %v", err)
	}
	code, body = callTicketSegments(t, s, tok, tk.ID)
	if code == http.StatusOK {
		t.Fatalf("★ F-44 复现：读侧失败被吞成 200（空修订假成功），响应=%v", body)
	}
	if code != http.StatusInternalServerError {
		t.Fatalf("读侧失败应 500，实得 %d: %v", code, body)
	}
	if body["success"] != false {
		t.Fatalf("500 响应体 success 必须为 false: %v", body)
	}
	if c, _ := body["code"].(string); c != "INTERNAL_ERROR" {
		t.Fatalf("错误码应为 INTERNAL_ERROR（AGENTS §一·8 带码契约），实得 %v", body["code"])
	}
	// ③ 消息口径：必须点明「人工修订记录读取失败」，前端据此提示重试，不许泛化成「服务器错误」。
	if m, _ := body["message"].(string); m == "" || !strings.Contains(m, "人工修订") {
		t.Fatalf("message 应点明人工修订读取失败，实得 %q", m)
	}
}
