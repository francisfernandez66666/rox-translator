// ============ canned_purge_test.go · 职责说明 ============
// 锁 0AR 第 4 波第 ③ 步：启动期**一次性**清掉「口径已换代」的 canned 译文孤儿行
// （机制与三条放行分支见 canned_purge.go 文件头）。
//
// 这批用例的每一条都在问同一件事：**清理判据与运行期判据是不是同一个结论**。
// 现网这条链最难发现的坏状态不是"清多了"（那当场就能看见），而是
// 「脚本报 0 行、库里一行没清、界面照常投旧字节」——0 行看起来和"本来就没有旧代行"一模一样。
// 所以每条正向锁都必配一条"该留的必须留"的对照，反向锁则专门测**假绿形态**（空转）。
//
// 反证写在每条用例注释里（破坏点 → 哪一条红）。
// =============================================
package engine

import (
	"context"
	"strings"
	"testing"
)

// cannedTestSource 本文件统一的源文（提到品牌名与积分，两条口径都在场——
// 与现网 configs.welcome 的真实形态一致，见 canned_guard_test.go 的 liveWelcome）。
const cannedTestSource = "你好，我是能言 AI 助手，积分充值随时开通"

// seedCannedRow 按「当前口径」写一行译文缓存（模拟上一代二进制留下的行时传 staleHead）。
func seedCannedRow(t *testing.T, e *Engine, key, head, body string) {
	t.Helper()
	if err := e.db.SetConfig(key, head+"\n"+body); err != nil {
		t.Fatalf("预置缓存行 %s 失败：%v", key, err)
	}
}

// TestCannedCacheStaleRowsPurged 主断言：指纹不等就删、相等就留，且**逐行重算**而不是按前缀匹配。
//
// 反证：把判据写成 `head LIKE '096x%'` 那种前缀匹配（台账里被明令禁止的形态）⇒
// ① 库里没有任何一行的头含 rev 字样（头是 12 位 hex），删除数恒 0、本用例第一段当场红；
// ② 就算能匹配上，"原文被运营改过"的那一行也会被漏掉（它的头同样不含旧 rev）。
func TestCannedCacheStaleRowsPurged(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	// 现读现推的"当前指纹"：清理腿必须与写侧算出同一个值，所以这里用**同一对函数**
	curFp := srcFingerprint(cannedTestSource + "\x00" + localizeContract("en", srcTopicsOf(cannedTestSource)))
	_ = e.db.SetConfig("welcome", cannedTestSource)
	_ = e.db.SetConfig("quick_chips", "怎么上传文件翻译？,积分怎么收费？")

	seedCannedRow(t, e, "i18n:welcome:en", curFp, "Hello, this is LangCross. Credits top-up anytime.")
	seedCannedRow(t, e, "i18n:welcome:ja", "deadbeefcafe", "こんにちは、能言 AI アシスタントです。")
	// 语种写法不归一的那一档：'EN' 与 'en' 在库里是两把键（canonicalLang 已在写侧收口，
	// 这里模拟历史遗留），清理按**键里那一段**现算，两把键都该被认出来。
	seedCannedRow(t, e, "i18n:welcome:zh_hant", "0123456789ab", "舊譯文")

	if n := e.PurgeStaleCannedTranslations(ctx); n != 2 {
		t.Fatalf("应清掉两行旧代（ja／zh_hant），实际删 %d 行", n)
	}
	if got := e.db.GetConfig("i18n:welcome:en", ""); got == "" {
		t.Fatal("当前代译文被误删 ⇒ 每一次首屏都白打一次上游（清理变成了清库）")
	}
	if !strings.HasPrefix(e.db.GetConfig("i18n:welcome:en", ""), curFp+"\n") {
		t.Fatal("留下的那一行头不是当前指纹，说明判据根本没比指纹")
	}
	if e.db.GetConfig("i18n:welcome:ja", "") != "" {
		t.Fatal("旧代行没删：这一档就是现网那种「库里躺着、没人管的旧字节」")
	}
	if e.db.GetConfig("i18n:welcome:zh_hant", "") != "" {
		t.Fatal("另一把键上的旧代行没删 ⇒ 按前缀匹配清理的形态就是这样漏一半")
	}
	// 幂等：第二次跑必须 0 行（不是"又删两行"，那说明删除根本没落地）
	if n := e.PurgeStaleCannedTranslations(ctx); n != 0 {
		t.Fatalf("清理不幂等（第二次又删了 %d 行）", n)
	}
}

// TestCannedPurgeKeepsManualAndUnknownRows 三条"宁可漏删不可误删"的放行分支各钉一条。
//
// 反证逐条点名（都在 canned_purge.go 里，摘掉哪条哪条红）：
//   - 摘 `HasSuffix(head, manualMark)` ⇒ ① 红（人工档被洗掉，
//     与 AGENTS §一·13 与 localize.go 文件头第 3 条「人工档永不自动作废」直接冲突）；
//   - 摘"未知 kind 跳过"或"空原文跳过"任一 ⇒ ②③ 各自红吗？**不会全红**，这两道是同一风险的两张网，
//     留两条因为它们会被**不同的改动**单独击穿：把 `cannedSourceFor` 的 default 分支改成"回落 welcome 原文"
//     时只有 `!known` 那条挡得住；把空原文那条改成"照算指纹"时只有 `!known` 之外的第二道挡得住。
//     只钉一条的锁会在下一次改动里静默失效，这里两条都点名是为了逼后来人一次摘掉两条；
//   - 摘"值形态不符即跳过"⇒ ④ 红（`splitCachedTranslation` 回 ok=false 时 head 是空串，
//     空串 ≠ 任何指纹 ⇒ 别人写的行会被当成旧代删掉）；
//   - 把前缀从 `i18n:` 改成 `%`（全表扫）⇒ ⑤ 红（admin_token 这类键进了射程）。
func TestCannedPurgeKeepsManualAndUnknownRows(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	_ = e.db.SetConfig("welcome", cannedTestSource)
	_ = e.db.SetConfig("quick_chips", "怎么上传文件翻译？,积分怎么收费？")

	// ① 人工档：头带 !manual 且指纹必然不等，**不许删**
	seedCannedRow(t, e, "i18n:welcome:en", "deadbeefcafe!manual", "运营手写的英文欢迎词")
	// ② 不认识的 kind（不是 canned 那两档）：本模块无权替别人清库
	seedCannedRow(t, e, "i18n:feature_name:en", "deadbeefcafe", "别的路径写的行")
	// ③ 键形态就不是三段（多一段）：直接跳过
	_ = e.db.SetConfig("i18n:welcome:en:extra", "deadbeefcafe\n旧形态")
	// ④ 值不是「指纹\n正文」形态（没有换行）：不猜、不删
	_ = e.db.SetConfig("i18n:chips:en", "只有一行不是我们的形态")
	// ⑤ 库里还住着别的 configs 键：前缀扫描不许碰它们
	_ = e.db.SetConfig("admin_token", "secret-looking-value")

	if n := e.PurgeStaleCannedTranslations(ctx); n != 0 {
		t.Fatalf("这五行一条都不该被删（人工档／未知 kind／畸形键／畸形值／非 i18n 前缀），实际删了 %d 行", n)
	}
	for _, k := range []string{"i18n:welcome:en", "i18n:feature_name:en", "i18n:welcome:en:extra", "i18n:chips:en", "admin_token"} {
		if e.db.GetConfig(k, "") == "" {
			t.Fatalf("%s 被清理腿动过（应当原样保留）", k)
		}
	}
}

// TestCannedPurgeSkipsWhenSourceUnavailable 原文现读不到 ⇒ **整段跳过**，一条都不删。
//
// 这是最容易写错的一条：`GetConfig("quick_chips","")` 取不到时返回空串，
// 空串算出来的指纹与任何一行都不等 ⇒ 如果不设这道放行分支，
// 一个"运营把 chips 清空了"的动作就会顺手把 12 个语种的 chips 译文全洗掉，
// 而运营第二天把 chips 填回来时，全部语种的首屏都要现翻一遍——那是把误删做成了功能。
func TestCannedPurgeSkipsWhenSourceUnavailable(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	_ = e.db.SetConfig("welcome", cannedTestSource)
	_ = e.db.SetConfig("quick_chips", "") // 运营此刻没配 chips
	seedCannedRow(t, e, "i18n:chips:en", "deadbeefcafe", "How do I upload a file?")

	if n := e.PurgeStaleCannedTranslations(ctx); n != 0 {
		t.Fatalf("原文取不到时必须整段跳过（算不出该配什么指纹），实际删了 %d 行", n)
	}
	if e.db.GetConfig("i18n:chips:en", "") == "" {
		t.Fatal("算不出指纹却把行删了 ⇒ 清理腿把猜测当成了证据")
	}
}

// TestCannedPurgeFallsBackToGreetingScript welcome 那一档的原文有**两级来源**
// （configs.welcome ＞ greeting 话术），清理腿必须走同一条回落链。
//
// 反证：只读 configs.welcome ⇒ 现网那些"欢迎词配在话术表里、configs 键为空"的实例
// 会把每一行 welcome 译文都判成过期（清理腿根本没回落），而运行期是正常命中缓存的
// ——两侧结论不等，就是本文件开头那句"脚本报删了 N 行、界面还在投旧字节"的另一面。
func TestCannedPurgeFallsBackToGreetingScript(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	_ = e.db.SetConfig("welcome", "") // 走话术回落：newTestEngine 的 sc-greet 内容是「你好呀」
	src := e.Greeting()
	if src == "" {
		t.Fatal("夹具的 greeting 话术取不到，本用例的靶子没了")
	}
	fp := srcFingerprint(src + "\x00" + localizeContract("en", srcTopicsOf(src)))
	seedCannedRow(t, e, "i18n:welcome:en", fp, "Hello there")
	seedCannedRow(t, e, "i18n:welcome:ja", "ffffffffffff", "古い訳")

	if n := e.PurgeStaleCannedTranslations(ctx); n != 1 {
		t.Fatalf("只该删 ja 那一行旧代（en 那行走的是话术回落、仍然有效），实际删 %d 行", n)
	}
	if e.db.GetConfig("i18n:welcome:en", "") == "" {
		t.Fatal("回落链没走 ⇒ 配在话术表里的欢迎词，其所有语种译文都被判成过期")
	}
}

// TestCannedCacheKeyBuilderIsSingleSource 键构造只有一个事实源（cannedCacheKey）。
//
// 这条锁看起来像"格式洁癖"，实际射程是**清理腿与读写腿必须落在同一把键上**：
// 将来谁在清理腿里把语种写成不归一（`uiLang` 直接拼）、或在写侧另拼一份，
// 现网表现就是"清理删不到那一行、界面还在投它"，而且两侧各自都"对"。
func TestCannedCacheKeyBuilderIsSingleSource(t *testing.T) {
	if got := cannedCacheKey("welcome", "zh-Hant"); got != "i18n:welcome:zh_hant" {
		t.Fatalf("键的语种没归一：%q（'zh-Hant' 与 'zh_hant' 必须在同一把键上）", got)
	}
	kind, lang, ok := splitCannedCacheKey("i18n:chips:ko")
	if !ok || kind != "chips" || lang != "ko" {
		t.Fatalf("键拆不开：kind=%q lang=%q ok=%v（清理腿与写侧必须能对上）", kind, lang, ok)
	}
	for _, bad := range []string{"i18n:", "i18n:welcome", "i18n::en", "i18n:welcome:", "other:welcome:en", "i18n:welcome:en:extra"} {
		if _, _, ok := splitCannedCacheKey(bad); ok {
			t.Fatalf("畸形键 %q 被判成合法 ⇒ 清理腿会拿它去算指纹并可能删掉别人的行", bad)
		}
	}
	// chipsLinesFromCSV 与 LocalizeChips 用的是同一个拆分口径（两处各拆一遍＝下一次结论不同的产地）
	if got := chipsLinesFromCSV(" a , b ,, c "); len(got) != 3 || got[1] != "b" {
		t.Fatalf("chips 拆分口径不对：%v", got)
	}
}
