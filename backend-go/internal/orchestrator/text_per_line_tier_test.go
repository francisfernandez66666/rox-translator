// ============ 本文件职责中文说明 ============
// DEF-02 的应急回退开关档位锁：`text_ticket_per_line`（system_config，默认 1，**0＝回退整篇旧行为**）。
//
// 为什么单独一条：这一枚与 `ticket_repair_max=0` 同批被方案（《修改文档_UAT云端E2E核实与修法_20261024》
// 档位表）写成"应急开关"，但两者原先都走 `Store.ConfigInt`——那把尺子把 ≤0 读成"没配"并回落默认，
// 于是库里填 0、进程读回 1，逐行翻译**关不掉**（现网表现＝"改了配置没反应"，日志零线索）。
// 修法＝改走 Store.ConfigIntAllowZero（语义由 store/config_int_zero_test.go 钉）。
//
// 断言取向：不只测判定函数 textPerLineEnabled()（判据级），**必须再测一次接线点**
// translateTextPerLine 在 0 档时回 false＝真的把整篇路径让回去（守卫级）；
// 否则「开关判对了但调用方没读它」这一族完全无感。
// =============================================
package orchestrator

import (
	"context"
	"testing"
)

// TestTextPerLineTier text_ticket_per_line 三档等值：缺省 1／脏值回落 1／0 是一档真实值。
func TestTextPerLineTier(t *testing.T) {
	cases := []struct {
		val  string
		want bool
	}{
		{"", true},     // 库里没这一行＝默认启用逐行（DEF-02 的修法本体）
		{"1", true},    // 显式开
		{"0", false},   // ★ 应急回退档：必须关得掉
		{"abc", true},  // 脏值不表态，回落默认（把打错的值读成"关"＝整站悄悄退回整篇）
		{"-1", true},   // 负数同上，按脏值处理
		{" 0 ", false}, // 带空格仍是 0 档（管理台保存链路不保证 trim）
	}
	for _, c := range cases {
		t.Run("配"+c.val, func(t *testing.T) {
			h := pwSetup(t, nil)
			if err := h.St.SetConfig("text_ticket_per_line", c.val); err != nil {
				t.Fatalf("SetConfig: %v", err)
			}
			if got := h.W.textPerLineEnabled(); got != c.want {
				t.Fatalf("text_ticket_per_line=%q 应 enabled=%v，实得 %v", c.val, c.want, got)
			}
		})
	}
}

// TestTextPerLineZeroHandsBackToWholeDoc 守卫级接线锁：0 档时逐行分支必须让回整篇路径。
// 靶子是一段两行的源文（≥2 非空行才会进逐行链），配 0 后 translateTextPerLine 须回 false。
func TestTextPerLineZeroHandsBackToWholeDoc(t *testing.T) {
	h := pwSetup(t, nil)
	if err := h.St.SetConfig("text_ticket_per_line", "0"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	// 载荷刻意给**两个 nil map**：这是 loadPayload 反序列化一段没带 translations/sources 键的
	// JSON 后的真实形态。0 档要证明「没接管」，1 档要证明「接管且不 panic」
	// （旧形态会在写入点退 `assignment to entry in nil map`，整条流水线挂掉）。
	p := &ticketPayload{SourceText: "第一段内容。\n第二段内容。", TargetLangs: []string{"en"}}
	if h.W.translateTextPerLine(context.Background(), p) {
		t.Fatal("0 档仍由逐行链接管 ⇒ 应急回退开关没接到调用点上（只修判定函数＝没修）")
	}
	// 同一套零件把开关翻回 1，必须立刻接管——防止「永远返回 false」也能过上一条。
	if err := h.St.SetConfig("text_ticket_per_line", "1"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if !h.W.translateTextPerLine(context.Background(), p) {
		t.Fatal("1 档没接管逐行翻译 ⇒ 默认行为被这条应急改动一起打掉（假回退）")
	}
}
