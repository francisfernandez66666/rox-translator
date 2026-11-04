// ============ 本文件职责中文说明 ============
// ★ 2026-11-03 立。`sensitive.go` 的 `sensitiveRecord` 头上写着
// 「一条留痕只由一条通道的一个咽喉调用一次（等值判据见 sensitive_record_once_test.go）」，
// 本文件就是那条判据——**注释点名的锚点必须存在**（AGENTS §一·14）。
//
// 这条锁防的具体形态（不是"清洁度"）：留痕调用被放进语种循环或逐批循环里。
// 现实里最容易发生的位置有三处，全都长着"顺手写进去"的样子：
//
//	F1 `sensitiveBlockedSegments` 的命中段循环（一次命中 3 段 × 3 语种 ⇒ 刷出 9 条审计）
//	F2 `sensitiveSweepOutput` 的 `for lc := range langTranslations`（每语种各刷一条）
//	F3 工单链的语种循环（`orchestrator` 侧已有 `recordOnce` 按通道去重，这里守的是引擎自己的两个咽喉）
//
// 后果：告警中心、群机器人、站内信三个面一起灌脏，而「这次拦了多少」的读数翻倍
// ——AGENTS §一·3「读数为失败背书」的姊妹形态：读数给规模背书。
//
// ★ 判据口径的两个刻意选择：
//
//	① **数审计（audit_logs），不数告警**：`store.CreateAlertEx` 对「同租户 + 同 kind + 未关闭」
//	   本身就幂等去重，所以哪怕真的刷了 9 次，告警表里也只有 1 行——拿它当计数锚点会**恒绿**。
//	   审计表才是每次都真写的那个面（本仓 §一·8「库侧事实才算验收」）。
//	② **每条通道各判一次**：只测 file 通道时，把 output 那条腿的留痕挪进循环里照样全绿
//	   （两族成因的处置动作不同，混着测等于给另一半放假）。
//
// 关于复用：Store 厂用同包 `b4Store`（`prompt_prefix_test.go`），它已经把
// `config.C` 的方言钉成 sqlite 并注册了 cleanup——AGENTS §一·4 那条「新增单测必须自钉方言」
// 的模板就是它，这里不再抄第二份（两份模板漂了以后，跑 UAT 的 PG 模式必漏一处）。
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

// srPack 写一份临时词包并返回装载好的 Checker。
// 词条一律 CJK：本文件判的是"留痕条数"，与词边界口径无关（那把尺子在 internal/sensitive 自己的锁里）。
func srPack(t *testing.T, words ...string) *sensitive.Checker {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sensitive_words.txt")
	if err := os.WriteFile(p, []byte(strings.Join(words, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("写临时词包失败: %v", err)
	}
	return sensitive.New(p)
}

// srAudits 数某一通道的敏感留痕条数。
// 取不到 ⇒ 判死并说明"这一腿没跑到"，不许当成 0（0 与"没跑到"在断言里含义相反）。
func srAudits(t *testing.T, e *Engine, channel string) int {
	t.Helper()
	logs, err := e.St.ListAuditFilter(1, "sensitive_block", channel, 0, "", "", 200)
	if err != nil {
		t.Fatalf("数审计留痕失败（这一腿没跑到，不许当 0）: %v", err)
	}
	return len(logs)
}

// TestSensitiveFileInputRecordOncePerChannel F1：文件通道输入侧，
// 3 段命中 × 3 语种 ⇒ 审计**恰 1 条**，而占位符仍要逐段逐语种写满（9 条）。
//
// 两个读数必须同时成立才叫"留痕收口"：
//
//	留痕 1 条（不刷屏）＋ 占位 9 条（不因为收口而少拦一段）。
//
// 只判前者时，把循环整个删掉也会绿。
func TestSensitiveFileInputRecordOncePerChannel(t *testing.T) {
	e := &Engine{St: b4Store(t)}
	e.Sensitive = srPack(t, "枪支", "弹药")

	writes := 0
	blocked, _ := e.sensitiveBlockedSegments(context.Background(),
		[]string{"甲段含枪支。", "乙段含弹药。", "丙段两者都有枪支弹药。", "丁段正常内容。"},
		[]string{"en", "ja", "ko"},
		func(lc, orig, tr string) { writes++ })

	if len(blocked) != 3 {
		t.Fatalf("夹具不对：应拦下 3 段，实得 %d ⇒ 下面两条断言都会空转", len(blocked))
	}
	if writes != 9 {
		t.Errorf("占位译文必须逐段逐语种写满（3 段 × 3 语种 = 9），实得 %d ⇒ 收口把「少拦」一起收进去了", writes)
	}
	if n := srAudits(t, e, "file_input"); n != 1 {
		t.Errorf("一次调用内多段多语种命中必须**只留一条**审计，实得 %d 条（翻倍即「告警灌脏」那族事故）", n)
	}
}

// TestSensitiveOutputRecordOncePerChannel F2：文件通道输出侧，
// 3 个语种 × 2 段命中 ⇒ 审计**恰 1 条**，且 6 处全被换回占位符。
//
// 这条腿最容易被写进 `for lc := range` 里（写起来最顺手的那一行），
// 而它偏偏是**多语种同批**发生的场景 ⇒ 刷屏规模 = 语种数。
func TestSensitiveOutputRecordOncePerChannel(t *testing.T) {
	e := &Engine{St: b4Store(t)}
	e.Sensitive = srPack(t, "枪支")

	lts := map[string]map[string]string{
		"en": {"原文一": "译文里带枪支字样", "原文二": "另一段也有 guns 之外的枪支"},
		"ja": {"原文一": "訳文に枪支が入る", "原文二": "別の訳文にも枪支"},
		"ko": {"原文一": "번역문에 枪支 포함", "原文二": "다른 번역에도 枪支"},
	}
	n := e.sensitiveSweepOutput(context.Background(), lts)
	if n != 6 {
		t.Fatalf("夹具不对：应替换 6 处（3 语种 × 2 段），实得 %d", n)
	}
	if n := srAudits(t, e, "output"); n != 1 {
		t.Errorf("多语种输出侧命中必须**只留一条**审计，实得 %d 条（= 语种数就是「留痕被放进语种循环」的铁证）", n)
	}
}

// TestSensitiveRecordAbsentWhenNoHit 负向对照：零命中 ⇒ 两族都 0 条。
//
// 没有这一条，"每次都只写一条"会在"整条留痕腿压根没接"时照样绿
// （AGENTS §三「负向锁必须配正向对照」的镜像：正向锁也必须配零值对照）。
func TestSensitiveRecordAbsentWhenNoHit(t *testing.T) {
	e := &Engine{St: b4Store(t)}
	e.Sensitive = srPack(t, "枪支")

	blocked, _ := e.sensitiveBlockedSegments(context.Background(),
		[]string{"甲段正常。", "乙段也正常。"}, []string{"en"}, func(lc, orig, tr string) {})
	if len(blocked) != 0 {
		t.Fatalf("夹具不对：本例不该有命中段，实得 %d", len(blocked))
	}
	lts := map[string]map[string]string{"en": {"原文": "clean translation"}}
	if n := e.sensitiveSweepOutput(context.Background(), lts); n != 0 {
		t.Fatalf("夹具不对：本例不该有输出侧命中，实得 %d", n)
	}
	if n := srAudits(t, e, "file_input"); n != 0 {
		t.Errorf("零命中不许留痕，file_input 实得 %d 条", n)
	}
	if n := srAudits(t, e, "output"); n != 0 {
		t.Errorf("零命中不许留痕，output 实得 %d 条", n)
	}
}

// TestSensitiveRecordOffMeansNoAudit 显式关闸（sensitive_gate_enabled=0）⇒ 一条留痕都不写。
//
// 这条钉的是「关档＝没有这个特性」这条不变式（与 AGENTS §22 表头上下文那条同源）：
// 运营关掉闸之后若还在写"敏感词命中"的审计，值班会照着一份不存在的拦截记录去查词包。
func TestSensitiveRecordOffMeansNoAudit(t *testing.T) {
	e := &Engine{St: b4Store(t)}
	e.Sensitive = srPack(t, "枪支")
	if err := e.St.SetConfig("sensitive_gate_enabled", "0"); err != nil {
		t.Fatalf("写运营开关失败: %v", err)
	}
	if e.TicketSensitiveEnabled() || e.sensitiveOn() {
		t.Fatal("夹具失效：开关写 0 之后闸仍被判生效 ⇒ 下面两条断言会在真空里跑")
	}
	blocked, _ := e.sensitiveBlockedSegments(context.Background(),
		[]string{"甲段含枪支。"}, []string{"en", "ja"}, func(lc, orig, tr string) {})
	if len(blocked) != 0 {
		t.Errorf("关闸后不许再拦段，实得 %d", len(blocked))
	}
	if n := srAudits(t, e, "file_input"); n != 0 {
		t.Errorf("关闸后不许留痕，实得 %d 条", n)
	}
}
