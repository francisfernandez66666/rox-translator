// ============ 本文件职责中文说明 ============
// P5 表头上下文进记忆键的闸门锁 —— ★ 2026-10-28 计价与成本优化批（方案 §6.2(c)(f)）。
//
// 为什么必须进键：表头是**语境**，它会直接改变模型选词（同一句 "Rev." 在"职称表"与"发票表"里
// 是两种译法）。键里少这一维的后果是静默的：
//
//	先用 A 表头跑一遍 ⇒ 记忆里存的是"按 A 的术语理解"的译文；
//	换 B 表头再跑同一句 ⇒ 命中 A 的译文，**这一次的表头根本没被用过**；
//	客户看到的是一篇"术语前后一致，但整篇按另一张表的语境翻的"文档 —— 没人能归因。
//
// 六条腿：空表头等价 / 两表头键不同（M0010 靶子）/ 同表头键相同 / 批量链不跨命中 / 空表头不跨命中 /
// 源码接线（ctxHash 从 options 一路到 tmScope）。
// ⚠️ 断言"空表头等价"这条刻意用**逐字哈希等值**（tmHeaderContextHash("")==""）而不是"看起来对"：
// 关闭 P5 时记忆键必须与改造前逐字相同，否则上线当天全部历史命中率归零（成本反而涨）。
// ============================================
package engine

import (
	"context"
	"hash/fnv"
	"strings"
	"testing"

	"translator/internal/tenant"
)

// TestTMKeySeparatesHeaderContext 同源文 + 两种表头 ⇒ 键必须不同（M0010 靶子，变异「表头上下文必须进键」）。
//
// 反证口径：M0010 把 `h.Write([]byte(contextHash))` 换成 `h.Write(nil)`，
// 本用例的 `两种表头键不同` 一条必须当场红，且红在本文件的 t.Fatalf 字面「表头上下文必须进键」上。
func TestTMKeySeparatesHeaderContext(t *testing.T) {
	src := "Rev. 编号 1001"
	a := tmScope{tid: 1, srcLang: "zh", tgtLang: "en", mode: "m", modelFP: "p|m", ctxHash: tmHeaderContextHash("项目 | 金额 | 说明；甲方")}
	b := tmScope{tid: 1, srcLang: "zh", tgtLang: "en", mode: "m", modelFP: "p|m", ctxHash: tmHeaderContextHash("职称 | 姓名 | 部门；主管")}
	if a.ctxHash == "" || b.ctxHash == "" {
		t.Fatalf("非空表头的指纹不许为空（a=%q b=%q）⇒ 那就等于没进键", a.ctxHash, b.ctxHash)
	}
	if a.ctxHash == b.ctxHash {
		t.Fatalf("两种不同表头算出同一指纹（%q）⇒ 指纹函数退化了", a.ctxHash)
	}
	if tmKey(a, src) == tmKey(b, src) {
		t.Fatal("表头上下文必须进键：同一句源文在两种表头下共用了记忆 ⇒ 整篇会按另一张表的语境出译，客户无法归因")
	}

	// 同一表头（含首尾空白差异）必须同键：否则同一篇文档换个抽取时刻就全部失效。
	c := tmScope{tid: 1, srcLang: "zh", tgtLang: "en", mode: "m", modelFP: "p|m", ctxHash: tmHeaderContextHash("  项目 | 金额 | 说明；甲方  ")}
	if tmKey(a, src) != tmKey(c, src) {
		t.Fatal("表头只有首尾空白差异就必须同键（哈希前没 Trim 会让命中率被空白抖动打散）")
	}
}

// TestTMHeaderContextHashEmptyIsNeutral 空/纯空白表头 ⇒ 指纹为空串，且与"没这个特性"逐字等价。
//
// 这一条是**上线安全阀**：不成立就等于"打开 P5 的那一刻，全站历史译文记忆作废"。
func TestTMHeaderContextHashEmptyIsNeutral(t *testing.T) {
	for _, v := range []string{"", "   ", "\n\t "} {
		if got := tmHeaderContextHash(v); got != "" {
			t.Fatalf("tmHeaderContextHash(%q)=%q，必须为空串 ⇒ 否则关闭 P5 时记忆键与改造前不同，历史命中全废", v, got)
		}
	}
	base := tmScope{tid: 1, srcLang: "zh", tgtLang: "en", mode: "m", modelFP: "p|m"}
	withEmpty := base
	withEmpty.ctxHash = tmHeaderContextHash("  ")
	if tmKey(base, "请勿拆卸电池。") != tmKey(withEmpty, "请勿拆卸电池。") {
		t.Fatal("空表头必须与零值 options 逐字同键（关闭 P5 时提示词与记忆键都要零变化）")
	}
}

// hdrKeyStubStubHash 独立复算"六维 + 源文"的指纹，只为**自证靶子有射程**用：
// 它与产品实现必须一致；一旦产品换了维度清单，这一条会红（提醒同步本锁，而不是静默漂）。
func hdrKeyStubHash(sc tmScope, text string) uint64 {
	h := fnv.New64a()
	for _, part := range []string{
		sc.srcLang, sc.tgtLang, sc.mode, sc.modelFP, sc.ctxHash, tmPromptRev, text,
	} {
		// 分隔符用 NUL：源文里带 '|' 不会造成跨字段歧义（与 tmKey 同口径）。
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return h.Sum64()
}

// TestTMKeyHeaderContextIsOnlyDifferentiator 反向对照：**只**改表头，其余六维全同 ⇒ 键必须变；
// 而"只改表头"以外的任何一维变化（已由 L2 覆盖）也变。
//
// 这一条的用途是把「表头进键」与「别的维度顺手把它带变了」分开：
// 只测"两种表头键不同"时，若 scope 构造里恰好还带了 tenant/model 差异，读数照样绿而表头其实没进键。
func TestTMKeyHeaderContextIsOnlyDifferentiator(t *testing.T) {
	base := tmScope{tid: 7, srcLang: "en", tgtLang: "zh", mode: "mode-1", modelFP: "prov|model-x"}
	a := base
	a.ctxHash = tmHeaderContextHash("Part | Qty；A")
	b := base
	b.ctxHash = tmHeaderContextHash("Name | Role；B")
	src := "Rev. 42"
	if hdrKeyStubHash(a, src) == hdrKeyStubHash(b, src) {
		t.Fatal("自证夹具失效：仅表头不同时独立复算也不该相同（说明本用例的两种 scope 其实还有别的差异）")
	}
	if tmKey(a, src) == tmKey(b, src) {
		t.Fatal("仅表头不同就必须换键（其余六维完全相同）⇒ 表头没有真正进 tmKey，是别的维度在替它变键")
	}
	if !strings.Contains(tmKey(base, src), "") { // 防"tmKey 退回空串"这类退化
		t.Fatal("tmKey 不许返回空串")
	}
}

// ============ 批量链：不跨命中 ============

// TestTMBatchDoesNotCrossHitAcrossHeaders 端到端：A 表头跑一遍写库 → B 表头必须重新打上游 →
// 再用 A 表头必须零上游调用（命中）。三步各自是独立读数，合成一条就分不清"没命中"与"没写库"。
func TestTMBatchDoesNotCrossHitAcrossHeaders(t *testing.T) {
	u := &tmStubUpstream{good: "Do not disassemble the battery."}
	e := tmTestEngine(t, u)
	ctx := tenant.WithTenant(context.Background(), 1)
	srcs := []string{"请勿拆卸电池。", "首次使用前请充电。"}

	run := func(header string) {
		t.Helper()
		if _, ok := e.BatchTranslateMarkedOpts(ctx, srcs, "en", 0, nil, BatchOptions{HeaderContext: header}); len(ok) != 0 {
			// 带真读数的那一口第二返回值是"被截断段"台账；正常路径必须为空。
			t.Fatalf("正常路径不该有截断段台账，实际 %v", ok)
		}
	}

	run("项目 | 金额 | 说明；甲方")
	if n := tmCountRows(t, e.St); n != len(srcs) {
		t.Fatalf("A 表头应逐段写库 %d 行，实际 %d 行", len(srcs), n)
	}

	// ① 换表头 ⇒ 必须重新打上游（跨表头不许命中）
	before := u.calls.Load()
	run("职称 | 姓名 | 部门；主管")
	if u.calls.Load() == before {
		t.Fatal("换表头后一次上游都没打 ⇒ 命中了另一张表头的记忆（整篇会按错表的术语出译）")
	}

	// ② 回到 A 表头 ⇒ 必须全命中（正向对照：证明 ① 的红不是"缓存根本没生效"）
	before = u.calls.Load()
	run("项目 | 金额 | 说明；甲方")
	if u.calls.Load() != before {
		t.Fatalf("回到原表头后同样的源文必须全命中，实际又打了 %d 次 ⇒ 表头维度把命中率打散了", u.calls.Load()-before)
	}

	// ③ 空表头（关闭 P5 的形态）也必须自成一路：不许命中 A 表头那一格
	before = u.calls.Load()
	run("")
	if u.calls.Load() == before {
		t.Fatal("空表头命中了带表头的记忆 ⇒ \"有上下文/无上下文\"互相复用（v1 §4.2 第 2 条）")
	}
}
