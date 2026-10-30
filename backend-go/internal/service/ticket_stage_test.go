// ============ ticket_stage_test.go · 职责说明 ============
// 组5「两段式交付」服务层锁（B8）：
//
//	① **服务层不再落盘 zip**——`zipOutputs` 调用归零、`archive/zip` 不再被这个文件引用；
//	② `persistTextOutputs` 只做**归属登记**并返回**首个真实存在**的产物路径
//	   （`tickets.text_result_path` 写它；保留期清理与历史读点都靠它，所以不能是空串）。
//
// 为什么这两条必须机械判：停打包以后"appendix 还能下载"这件事由 artifacts 行兜着，
// 一旦有人把 zip 写回来，磁盘上会同时长出一份整包与 N 份单件（体积翻倍且两套命名），
// 而唯一能看出这件事的地方是**包本身**——单测与界面都不会报错。
// =============================================
package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"translator/internal/db"
	"translator/internal/store"
)

// TestTicketStage_NoZipOnDisk B8①：服务层不再打 zip。
func TestTicketStage_NoZipOnDisk(t *testing.T) {
	raw, err := os.ReadFile("ticket.go")
	if err != nil {
		t.Fatalf("读不到 ticket.go（闸门失明）: %v", err)
	}
	src := string(raw)
	if strings.Contains(src, "zipOutputs(") {
		t.Fatal("B8：服务层又出现了 zipOutputs( ——两段式交付要求不再落盘 zip（单件由 artifacts 行兜着）")
	}
	if strings.Contains(src, `"archive/zip"`) {
		t.Fatal(`B8：ticket.go 又 import 了 "archive/zip"（现网 zip 只允许"按需流式"在 api 层生成）`)
	}
}

// TestTicketStage_ResultPathIsFirstExistingProduct B8②：文本产物只登记、返回首件。
func TestTicketStage_ResultPathIsFirstExistingProduct(t *testing.T) {
	svc := newLowBalanceSvc(t) // 复用同包的内存库夹具（Store 已迁移）
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.md")
	p1 := filepath.Join(dir, "a_en-text.md")
	p2 := filepath.Join(dir, "a_ja-text.md")
	for _, p := range []string{p1, p2} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("写临时产物失败: %v", err)
		}
	}
	tk := &store.Ticket{TenantID: 11}
	tk.ID = 77 // 内存库不校验外键；这里只验归属登记与返回值

	got := svc.persistTextOutputs(tk, 9001, []string{missing, p1, p2})
	if got != p1 {
		t.Fatalf("B8：必须返回**首个真实存在**的产物（跳过不存在的），期望 %s 实得 %s", p1, got)
	}
	var n int
	if err := db.QueryRow(svc.Store.DB(), db.CurrentDialect(),
		"SELECT COUNT(*) FROM output_artifacts WHERE tenant_id=? AND ticket_id=?", int64(11), int64(77)).Scan(&n); err != nil {
		t.Fatalf("读归属登记失败: %v", err)
	}
	if n != 2 {
		t.Fatalf("B8：两份存在的产物都应登记归属（不存在的跳过），实得 %d 行", n)
	}
}
