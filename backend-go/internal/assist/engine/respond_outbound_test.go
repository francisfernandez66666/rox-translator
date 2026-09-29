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
func TestRespondThroughOutboundGuards(t *testing.T) {
	// 第 1 次调用＝生成（现网那三条形态一起复刻进来）；第 2 次＝汉字残留补翻
	st := newSeqStub(t,
		"能与より承ります。文件翻訳は元のレイアウトを保持し、プロモードは 2000×400+7.5 で約 150 ポイントです。\nご確認お願いします。",
		"能与より承ります。文書翻訳は元のレイアウトを保持し、プロモードは 2000×400+7.5 で約 150 ポイントです。\nご確認お願いします。")
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
	// 补翻一次就收手：整条链最多两次上游（生成 + 一次补翻），不许变成"反复重写"
	if got := st.count(); got > 2 {
		t.Errorf("出站守卫打了 %d 次上游（访客等待链上最多 2 次）", got)
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
