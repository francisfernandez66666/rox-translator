// ============ admin_ui_test.go · 职责说明 ============
// assist web 包测试文件。
// =============================================

// ============ 本文件职责中文说明 ============
// AI 助手管理台（/assist/admin，assist-server 直出的单文件 HTML）视觉真值闸门。
//
// 为什么单独一条：本页随 assist-server 二进制内嵌（go:embed），既不在前端 dist 里、
// 也不在主站后端 public.go 里，前端 readability.test.ts 与 api 包的 public_ui_test.go
// 都扫不到它。2026-09-22 全站按 UI 还原时它仍是「蓝靛主色 #2f47f5 + 浅底 + 绿态徽标」，
// 与交付的 X/Grok 单色纯黑体系（UI-ANNOTATIONS §1.1：全站无蓝无绿）完全相反。
// 本文件把「旧配色不得复活」与「必须等于令牌真值」两侧钉住，堵住第三个盲区。
// ========================================
package web

import (
	"regexp"
	"strings"
	"testing"
)

// legacyAssistAdminPalette 历史独立配色的全部字面值（蓝主色族 + 浅底族 + 绿/红状态族），
// 任何一个回到页面里都说明有人又把这套主题搬回来了。
var legacyAssistAdminPalette = []string{
	"#2f47f5", "#f5f7fa", "#fafbfd", "#f3f4f6", "#e5e8ef", "#eef2ff", "#dbeafe", "#1d4ed8",
	"#1f2329", "#6b7280", "#9ca3af", "#dcfce7", "#15803d", "#fee2e2", "#b91c1c", "#fef3c7", "#b45309",
}

// cssBlockCommentRE 剥 CSS/JS 块注释（/* … */），htmlCommentRE 剥 HTML 注释。
var (
	cssBlockCommentRE = regexp.MustCompile(`(?s)/\*.*?\*/`)
	htmlCommentRE     = regexp.MustCompile(`(?s)<!--.*?-->`)
)

// TestAssistAdminMonochromeTruth 管理台必须按 §1.1 令牌出单色纯黑主题。
func TestAssistAdminMonochromeTruth(t *testing.T) {
	page := string(AdminHTML)
	if len(page) < 1000 {
		t.Fatalf("内嵌 admin.html 内容异常（%d 字节），闸门本身失效", len(page))
	}
	// 只扫代码：本文件顶部的「旧值 → 真值」说明注释里就带着 #2f47f5 这类字样，
	// 不剥注释会让负向锁命中注释自己（本仓静态负向锁的历史踩坑）。
	code := cssBlockCommentRE.ReplaceAllString(page, "")
	code = htmlCommentRE.ReplaceAllString(code, "")
	for _, hex := range legacyAssistAdminPalette {
		if strings.Contains(code, hex) {
			t.Errorf("旧独立配色 %s 复活：全站无蓝无绿、浅底已废止（UI-ANNOTATIONS §1.1）", hex)
		}
	}
	for _, want := range []string{
		// ★ 〇-P（2026-09-23 用户后令「严格按 UI 交付稿来」）：撤销 〇-O，面回交付值、
		// 描边族（line/card-line/pill/input-line/done）回交付灰阶；文字色逐字未动。
		"--bg:#000000", "--panel:#0E1014", "--surface:#16181C", "--inset:#0A0B0D",
		"--txt:#E7E9EA", "--sub:#9AA0AA", "--weak:#71767B",
		"--line:#464C58", "--card-line:#3A404C", "--pill:#424956",
		"--white:#FFFFFF", "--warn:#D29922", "--danger:#E5484D",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("缺少 §1.1 令牌声明 %s", want)
		}
	}
	// 〇-O 的遗留档不得复活（按成对声明比对，理由同 public_ui_test.go 的 retiredLegacy00O）
	for _, banned := range []string{
		"--line:#FFFFFF", "--card-line:#FFFFFF", "--pill:#FFFFFF",
		"--input-line:#FFFFFF", "--done:#FFFFFF", "--panel:#121417", "--surface:#1A1D21",
	} {
		if strings.Contains(code, banned) {
			t.Errorf("〇-O 的遗留档 %s 复活（框线应走交付灰阶、面应回交付值）", banned)
		}
	}
	// 主按钮纯白底黑字（交付真值 .lc-btn--primary），且不得用文字档灰 #E7E9EA 当底
	if !strings.Contains(code, "button.pri{background:var(--white);color:#000000") {
		t.Error("主按钮未按交付真值走纯白底黑字")
	}
	if strings.Contains(code, "background:var(--txt)") || strings.Contains(code, "background:#E7E9EA") {
		t.Error("实心白件用文字档灰 #E7E9EA 做底，应取 --white")
	}
	// 遮罩 72% 黑（§1.4 浮层规格）
	if !strings.Contains(code, "rgba(0,0,0,.72)") {
		t.Error("对话框遮罩 ≠ §1.4 的 72% 黑")
	}
}
