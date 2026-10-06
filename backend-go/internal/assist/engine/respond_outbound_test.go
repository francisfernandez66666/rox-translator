// respond_outbound_test.go — 出站咽喉的**接线证明**（★ 092x 三条红腿一起）。
//
// 单测各自绿不代表挂在链上：本仓有过「修好了但没接上出站」的形态（082x 第九条那条混排气泡，
// 机制在、入站没送 lang）。这一条从 `Respond` 打进一条带着**三种现网缺陷**的日文回答，
// 要求出栈时三条一起变干净；再用一条合格回答做对照，要求正文一字不动、上游只被打一次
// （守卫不该给每条回复白加往返）。
package engine

import (
	"context"
	"strings"
	"testing"
)

// TestRespondThrougOutboundGuards 一条脏回答过三道守卫。
//
// ★ 093x 把这条接线证明改硬了一处（当时探针发现它是"侥幸绿"）：原先那条中文词形「文件翻訳」
// 和乘法算式写在**同一句**里，于是删掉词形档「文件」之后它照样绿——因为报价守卫把整句换成
// 了不承诺总额那句，顺手把「文件」一起带走了（探针实跑读数：leaks=[]、calls=1、出栈正文里
// 「文件翻訳」整句已被换成守卫自己的那句「ファイルを送って…」）。
// 现在把词形残留单独放一句**不含算式**的话里：只有补翻那一枪打得中它。
// 「上游恰好 2 次」也从"不许超过 2 次"改成"必须等于 2 次"——少了那一次就是没接线。
func TestRespondThroughOutboundGuards(t *testing.T) {
	// 第 1 次调用＝生成（现网那三条形态一起复刻进来）；第 2 次＝汉字残留补翻
	st := newSeqStub(t,
		"能与より承ります。文件翻訳は元のレイアウトを保持します。\nプロモードは 2000×400+7.5 で約 150 ポイントです。\nご確認お願いします。",
		"能与より承ります。文書翻訳は元のレイアウトを保持します。\nプロモードは 2000×400+7.5 で約 150 ポイントです。\nご確認お願いします。")
	e := st.engine(t)
	e.sysValDoc = docForGuard() // 报价核验只读缓存（出站路上不打网络，见 cachedPricing）
	newSession(t, e, "s-out")

	rep := e.Respond(context.Background(), "s-out", "文件の翻訳はいくらですか", "/", "ja", nil)
	if strings.Contains(rep.Content, "文件") {
		t.Errorf("红腿一没接上出站（中文词形仍在正文）：%q", rep.Content)
	}
	if strings.Contains(rep.Content, "2000×400") || strings.Contains(rep.Content, "150 ポイント") {
		t.Errorf("红腿二没接上出站（自算的算式与总额仍在正文）：%q", rep.Content)
	}
	if strings.Contains(rep.Content, "能与") || strings.Contains(rep.Content, "Nengyan") {
		t.Errorf("红腿三没接上出站（品牌错形仍在正文）：%q", rep.Content)
	}
	if !strings.Contains(rep.Content, "能言") {
		t.Errorf("品牌归一后该语种档写法不在正文里：%q", rep.Content)
	}
	// 补翻一次就收手：整条链**恰好**两次上游（生成 + 一次补翻）。
	// 少了＝补翻没接上出站（那第一条断言就成了靠报价守卫顺带干掉的侥幸绿）；
	// 多了＝变成"反复重写"，访客在屏幕前等不起。
	if got := st.count(); got != 2 {
		t.Errorf("出站守卫打了 %d 次上游，期望恰好 2 次（生成＋一次补翻）", got)
	}
	// 换行必须还在：守卫把列表挤平就是排版破坏
	if !strings.Contains(rep.Content, "\n") {
		t.Errorf("换行被守卫吃掉：%q", rep.Content)
	}
}

// TestRespondLeavesCleanReplyAlone 反向对照：一条合格回答不许被三条守卫中的任何一条动过一个字，
// 也不许多打一次上游。缺这条对照，上面那条"变干净"就可能是"逢人就改"。
func TestRespondLeavesCleanReplyAlone(t *testing.T) {
	clean := "文書翻訳は元のレイアウトを保持します。\nプロモードは 2000 源字符で 23.5 ポイントです。"
	st := newSeqStub(t, clean)
	e := st.engine(t)
	e.sysValDoc = docForGuard()
	newSession(t, e, "s-clean")

	rep := e.Respond(context.Background(), "s-clean", "ドキュメント翻訳の料金は", "/", "ja", nil)
	if rep.Content != clean {
		t.Fatalf("合格回答被守卫改动了：\n 原：%q\n 出：%q", clean, rep.Content)
	}
	if st.count() != 1 {
		t.Fatalf("合格回答多打了上游（%d 次）——守卫不该给每条回复加一次往返", st.count())
	}
}

// TestRespondRehardensRepairedDraft ★ 093x（2026-09-30 真机挂件复问的第四条漏点）：
// 补翻腿的产物**从不过末道卫生**——postProcess 那三道只作用于模型自己写的第一稿，
// 而整段翻译与带残片重写都是直接 `rep.Content = 新稿` 出栈的。
// 现网实证形态就是补翻把括号形态再换一次：控制序列换成 ASCII 半截链接、旁白照样带着走。
// 这一条从 Respond 打进"第一稿带残片、第二稿带控制序列＋日文旁白"的对话，
// 要求出栈时新稿同样过一遍归一＋摘标记＋sanitize，并把摘出来的 key 并回按钮区。
func TestRespondRehardensRepairedDraft(t *testing.T) {
	st := newSeqStub(t,
		// 第 1 次＝生成：文件翻訳 是残片，逼出补翻那一枪
		"ファイルではなく文件翻訳で対応します。\n料金は原文により変動します。",
		// 第 2 次＝补翻新稿：残片修好了，却带来写歪的控制序列＋一句日文旁白
		"文書翻訳で対応します。\n[go:pricing] をご確認ください。（※日本語で回答するため翻訳を実施。）")
	e := st.engine(t)
	e.sysValDoc = docForGuard()
	newSession(t, e, "s-reharden")

	rep := e.Respond(context.Background(), "s-reharden", "文件の翻訳はいくらですか", "/", "ja", nil)

	// ① 控制序列一个字都不许留在气泡里
	if strings.Contains(rep.Content, "go:") || strings.Contains(rep.Content, "】") || strings.Contains(rep.Content, "[go") {
		t.Errorf("补翻产物里的控制序列没被二次收口：%q", rep.Content)
	}
	// ② 日文旁白同样得剥（这一句是 093x 现网实证形态）
	if strings.Contains(rep.Content, "で回答するため") || strings.Contains(rep.Content, "翻訳を実施") {
		t.Errorf("补翻产物里的旁白没被剥：%q", rep.Content)
	}
	// ③ 摘出来的 key 必须并回按钮区——只剥不接等于把一条对外错报换成一条功能缺失
	found := false
	for _, a := range rep.Actions {
		if a.Key == "pricing" {
			found = true
		}
	}
	if !found {
		t.Errorf("控制序列被剥掉了却没长出按钮（入口丢失）：%+v", rep.Actions)
	}
	// ④ 正文其余部分照留：二次收口不是"重写整段"
	if !strings.Contains(rep.Content, "文書翻訳で対応します") || !strings.Contains(rep.Content, "ご確認ください") {
		t.Errorf("二次收口把正常正文改坏了：%q", rep.Content)
	}
	// ⑤ 上游次数按**那一枪是谁**拆开数（★ 0AR 第 4 波 ⑱ 起必须这么写）。
	//
	//	这条回复带着一颗按钮（③ 那一腿正是从控制序列里并回来的 pricing），
	//	而 ⑱ 把按钮名也接进了 canned 那条路 ⇒ 同一次 Respond 现在会多打一枪。
	//	把下面那条"2 次"直接改成"3 次"是**掏空这条锁**的写法：
	//	它原本防的是"守卫链逢人就重写"，抬成 3 之后，将来谁给回复真加一次往返照样绿灯。
	//	正确形态＝守卫链那两枪（生成＋补翻）一个字都不许多，按钮那一枪单独点名。
	if got := st.countExcluding(cannedSurface); got != 2 {
		t.Errorf("守卫链打了 %d 次上游（应为生成＋补翻各一次；二次收口是纯字符串处理，一律不加往返）", got)
	}
	if got := st.count(); got != 3 {
		t.Errorf("上游总次数 %d，期望 3（生成＋补翻＋按钮名翻译各一枪）——少了那一枪＝⑱ 从 Respond 上掉了", got)
	}
}
