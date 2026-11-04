// ============ 本文件职责中文说明 ============
// ★ 2026-11-03 立。`sensitive.go` 的 `sensitiveRecord` 头上写着
// 「★ 2026-11-03 用户裁决三：告警正文**不再写词包路径**（旧形态把 `e.Sensitive.Path()`
// 打进站内信/群机器人——那是本仓 §一·12 对 /api/health 立的同一条纪律『只出状态词与计数，
// 禁出绝对路径』，告警面此前漏在外头，而告警是被截图转发的那一面）」。
// 本文件就是那条判据——**注释声称有锁，锁就得真存在**（AGENTS §一·14）。
//
// 为什么这条锁只能靠行为判、不能靠"扫源码里有没有 Path()"：
//
//	`CheckePath()` 的调用方式有无数种（`e.Sensitive.Path()`／先存变量再拼／经辅助函数带出），
//	源码级负向扫的是一个拼写，而缺陷会换个拼写复现；而**告警表里那一行是客户/运营真会看到的字**。
//	⇒ 判据取库侧事实（§一·8「接口说成功不算验收，库侧事实才算」）。
//
// 判据三腿（缺一条就有一条永远不会红的锁）：
//
//	A1 **负向**：告警正文与日志字段里不得出现词包的绝对路径、也不得出现任何形状的路径片段。
//	   （词包路径是部署机的真实目录，泄漏出去等于把内网结构写进一封会被转发的站内信。）
//	A2 **正向对照**：那条告警**必须真的存在**且带通道名与命中项数。
//	   只判 A1 时，把 `CreateAlert` 整通摘掉照样全绿——最典型的"负向锁空转"。
//	A3 **词面必须仍在**：用户令「写明跳过的是哪个词」的另一半——告警是运营面唯一的实时入口，
//	   把命中词也一起去掉，值班就只能看到"命中 1 项"却不知道是什么。A1 与 A3 的边界由此定死：
//	   **路径不许出、词面必须出**。
//
// ============================================
package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"translator/internal/sensitive"
)

// anpAlertMessages 取该租户下全部告警的 (message, log) 两段文本。
// 读不到 ⇒ 判死并点明"这一腿没跑到"（不许当 0——0 与"没跑到"含义相反）。
func anpAlertMessages(t *testing.T, e *Engine, tid int64) []string {
	t.Helper()
	rows, err := e.St.ListAlerts(tid, "", 200)
	if err != nil {
		t.Fatalf("读告警失败（这一腿没跑到，不许当通过）: %v", err)
	}
	out := make([]string, 0, len(rows))
	for _, a := range rows {
		out = append(out, a.Message, a.Log)
	}
	return out
}

// TestSensitiveAlertCarriesNoPath 告警正文不许带词包路径（A1 负向 ＋ A2 正向对照 ＋ A3 词面仍在）。
func TestSensitiveAlertCarriesNoPath(t *testing.T) {
	// 词包刻意放在一个**名字足够独特**的临时目录里：这样"路径泄漏"在断言里是
	// 一个能逐字比对的具体字符串，而不是"看着像路径"的模糊判断。
	dir := filepath.Join(t.TempDir(), "secret-words-dir-9f2a")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建临时词包目录失败: %v", err)
	}
	packPath := filepath.Join(dir, "sensitive_words.txt")
	if err := os.WriteFile(packPath, []byte("枪支\n"), 0o644); err != nil {
		t.Fatalf("写临时词包失败: %v", err)
	}
	checker := sensitive.New(packPath)
	if checker.Count() == 0 {
		t.Fatalf("词包没装载成功（路径 %s）⇒ 这条锁会在零命中的真空里跑", packPath)
	}

	st := b4Store(t)
	e := &Engine{St: st, Sensitive: checker}
	e.TicketSensitiveRecord(context.Background(), "ticket_input", []string{"枪支"}, "本产品随货附赠枪支配件一套。")

	msgs := anpAlertMessages(t, e, 1)
	// —— A2 正向对照：告警必须真写了（否则 A1 是空转锁）
	if len(msgs) == 0 {
		t.Fatal("一条告警都没写 ⇒ A1 的负向断言在真空里成立，本锁失明")
	}
	joined := strings.Join(msgs, "\n")

	// —— A1 负向：三段各判一次，都是"部署机上真实存在的内网结构"。
	for _, leak := range []string{
		packPath,           // 完整路径
		dir,                // 所在目录
		"secret-words-dir", // 目录名片段（拼进别的句式也照样是泄漏）
	} {
		if strings.Contains(joined, leak) {
			t.Errorf("告警正文泄漏了词包路径片段 %q —— 那是会被截图转发的站内信面（§一·12 同一条纪律）:\n%s",
				leak, joined)
		}
	}

	// —— A3 正向：通道名与命中词面必须都还在。
	// 这两个是"告警还有用"的最低要求：只有计数时值班无从判断该去核词包还是查模型。
	if !strings.Contains(joined, "ticket_input") {
		t.Errorf("告警必须点名通道（ticket_input / ticket_output …），实得:\n%s", joined)
	}
	if !strings.Contains(joined, "枪支") {
		t.Errorf("告警必须带命中词面（用户令「写明跳过的是哪个词」），实得:\n%s", joined)
	}
}

// TestSensitiveAlertNoPathGateIsNotVacuous A1 的自证：
// 把旧形态（正文拼进词包路径）喂进**同一把尺子**，必须判红。
//
// 为什么需要这一条：A1 判的是"不许出现某几段字符串"，而"这段字符串本来就抽不到"
// 与"实现干净"在断言里长得一样。这里拿旧病文本体复算一次，
// 证明射程覆盖的是缺陷本体、不是一句碰巧不含路径的文案。
func TestSensitiveAlertNoPathGateIsNotVacuous(t *testing.T) {
	packPath := "/opt/translator/data/sensitive_words.txt"
	oldWording := "[ticket_input] 敏感词命中 1 项：枪支（词包 " + packPath + "，处置见对应通道的交付面与审计留痕）"

	leaked := false
	for _, leak := range []string{packPath, "sensitive_words.txt"} {
		if strings.Contains(oldWording, leak) {
			leaked = true
		}
	}
	if !leaked {
		t.Fatal("旧病文本体竟不被本锁的判据命中 ⇒ A1 是空转的（判据与缺陷脱钩）")
	}
}
