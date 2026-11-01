// ============================================================================
// free_equiv_face_test.go — 即时翻译免费档的**积分出参两维**锁（★ 2026-11-01 用户拍板
// 「显示等值积分并注明免扣」）。
//
// 判据本体是 F-49① 那句「出参 points_used ＝ 实扣」，加上新立的那句
// 「落在免费档的一单：实扣必须报 0，等值量必须单独出一维、并点名免扣」。
// 四条各对一个历史/潜在形态：
//
//	E1 免费腿（只写 freeBilled）⇒ points_used=0 ＋ points_equiv>0 ＋ points_free=true。
//	   旧形态在这里是 **points_used 兜成真实用量**（UsageDisplayTokens 那条「取不到实收就
//	   保守回退真实用量」的兜底腿——钱没扣、报文却写着「消耗 N 积分」）⇒ 读数给「扣了钱」背书。
//	E2 付费腿对照（只写 billed）⇒ points_used＝实收折算，而两个新键**整条缺席**（omitempty）。
//	   只测 E1 等于什么都没测：把分派写成"无条件报免扣"也能过 E1。
//	E3 等值折不出积分（小量）⇒ 不标注免扣、等值键缺席——
//	   一行「0 积分 · 本次免扣」没有信息量，而"0"会被读成"这一单不值钱"。
//	E4 口径函数自身：UsageFreeBilledTokens 的「无收集器／没写过／有值」三态、
//	   两桶互不串账、并发累加不丢账。
//
// ⚠️ 本文件测的是**映射那一半**（引擎按收集器两只桶分派出参）；
// 「免费腿到底写没写 freeBilled」那一半由 `internal/api/instant_free_charge_test.go` 的 G1
// 与 `internal/api/instant_free_equiv_face_test.go`（真接线面）钉——判据级代替不了守卫级（AGENTS §三）。
//
// 方言口径（AGENTS §一·4）：复用 `happyUpstreamEngine`（自钉 sqlite 并恢复 config.C）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/engine/ -run 'TestFreeTier'
// ============================================================================
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"translator/internal/llm"
	"translator/internal/tenant"
)

// freeEquivRaw／freeEquivBilledT 一次免费档用量的靶子数。
// ⚠️ 量必须够折算一档（汇率约 24917 token/积分）：几十上百个 token 折成积分就是 **0**，
// 于是「等值>0」那条判据恒假红，而「没折出积分」那一档（E3）反倒是常态——
// 拿小量当靶子会把两条腿读反（同 AGENTS §一·24 那条「小量折 0 积分」的夹具纪律）。
const (
	freeEquivRaw     = int64(35000) // 真实用量（上游确实被打了这一枪）
	freeEquivBilledT = int64(30000) // 对外口径的等值 token（≥1 积分）
)

// freeTierRun 跑一次「上游恒回同一句译文」的即时翻译，返回出参与其 JSON 串。
// 参数 pre: 在 HandleText 之前对**请求级收集器**做的那次记账（模拟扣费现场的两种落笔）。
func freeTierRun(t *testing.T, pre func(uc *llm.UsageCollector)) (*TextTranslateResult, string) {
	t.Helper()
	e := happyUpstreamEngine(t, "The company focuses on smart hardware.")
	options := map[string]interface{}{
		"target_langs": toInterfaceLangs([]string{"en"}),
		"mode":         "fast",
		"lang":         "zh",
	}
	// 收集器必须由**外层**注入（引擎内层按 F-49② 复用同一只），这样 pre 写的那一笔才看得见。
	ctx := e.WithUsageRecorder(tenant.WithMode(context.Background(), "fast"))
	uc := llm.CollectorFrom(ctx)
	if uc == nil {
		t.Fatal("外层注入后必须能取到收集器（取不到＝这一跑根本没账，判据全部空转）")
	}
	pre(uc)
	res := e.HandleText(ctx, "本公司专注于智能硬件的研发与设计", options, nil)
	if res == nil {
		t.Fatal("HandleText 不应返回 nil")
	}
	if res.Error != "" {
		t.Fatalf("成功路径不该报错：Error=%s Reply=%q", res.Error, res.Reply)
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("序列化出参失败: %v", err)
	}
	return res, string(b)
}

// E1 免费腿：实扣 0 ＋ 等值单独一维 ＋ 标注免扣。
func TestFreeTierResultShowsEquivNotDebit(t *testing.T) {
	res, body := freeTierRun(t, func(uc *llm.UsageCollector) {
		uc.Add(freeEquivRaw, 0)
		uc.AddFreeBilled(freeEquivBilledT) // 等值桶：扣费现场的免费分支只写这一笔
	})
	want := (&Engine{}).PointsOfTokens(freeEquivBilledT)
	if want <= 0 {
		t.Fatalf("射程自证失败：PointsOfTokens(%d)=%d，折不出积分 ⇒ E1 的「等值>0」判据无从判定", freeEquivBilledT, want)
	}
	if res.PointsUsed != 0 {
		t.Fatalf("E1：免费腿的 points_used 必须是**实扣** 0，实得 %d（旧形态：展示侧兜到真实用量 ⇒ 报文说收了钱、余额一动不动）", res.PointsUsed)
	}
	if res.PointsEquiv != want {
		t.Fatalf("E1：等值积分应为 %d（＝PointsOfTokens(%d)，与扣费侧同一把尺子），实得 %d", want, freeEquivBilledT, res.PointsEquiv)
	}
	if !res.PointsFree {
		t.Fatal("E1：points_free 必须为 true——只出数字不点名免扣，客户会把等值读成扣费")
	}
	for _, need := range []string{`"points_used":0`, `"points_free":true`, `"points_equiv":`} {
		if !strings.Contains(body, need) {
			t.Fatalf("E1：出参 JSON 缺 %s。响应体：%s", need, body)
		}
	}
	// 反向对照：真实用量不许被折算成「消耗」（那是本批要消灭的形态）。
	if bad := fmt.Sprintf(`"points_used":%d`, (&Engine{}).PointsOfTokens(freeEquivRaw)); body == bad || strings.Contains(body, bad) {
		t.Fatalf("E1：points_used 又兜回真实用量了（%s）——F-49① 那条承诺复发。响应体：%s", bad, body)
	}
}

// E2 付费腿对照：points_used＝实收折算，两个新键整条缺席。
func TestFreeTierPaidLegKeepsDebitAndOmitsEquiv(t *testing.T) {
	res, body := freeTierRun(t, func(uc *llm.UsageCollector) {
		uc.Add(freeEquivRaw, 0)
		uc.AddBilled(freeEquivBilledT) // 扣费现场的付费分支写这一笔
	})
	want := (&Engine{}).PointsOfTokens(freeEquivBilledT)
	if want <= 0 {
		t.Fatalf("夹具前提失效：PointsOfTokens(%d)=%d，付费腿的等值判据无从判定", freeEquivBilledT, want)
	}
	if res.PointsUsed != want {
		t.Fatalf("E2：付费腿 points_used 必须＝实收折算 %d（F-49①），实得 %d", want, res.PointsUsed)
	}
	if res.PointsFree || res.PointsEquiv != 0 {
		t.Fatalf("E2：付费腿却带了免费档维度 equiv=%d free=%v ⇒ 出参在给没发生的事背书", res.PointsEquiv, res.PointsFree)
	}
	for _, bad := range []string{"points_equiv", "points_free"} {
		if strings.Contains(body, bad) {
			t.Fatalf("E2：付费腿的出参里出现了 %s（非免费单必须整条缺席，出 0 会被读成「这一单本该收 0 分」）：%s", bad, body)
		}
	}
}

// E3 小量折不出积分：不标注免扣、等值键缺席（宁可这一行不出，也不出「0 积分 · 免扣」）。
func TestFreeTierZeroEquivStaysSilent(t *testing.T) {
	res, body := freeTierRun(t, func(uc *llm.UsageCollector) {
		uc.Add(60, 40)
		uc.AddFreeBilled(80) // 80 token 远不够一档 ⇒ 折算为 0
	})
	if got := (&Engine{}).PointsOfTokens(80); got != 0 {
		t.Fatalf("夹具前提失效：PointsOfTokens(80)=%d，这一档不再是「小量」，E3 失去射程（要改靶子而不是改判据）", got)
	}
	if res.PointsFree || res.PointsEquiv != 0 {
		t.Fatalf("E3：折不出积分的一单却标注了免扣（equiv=%d free=%v）——一行「0 积分 · 本次免扣」没有信息量", res.PointsEquiv, res.PointsFree)
	}
	if res.PointsUsed != 0 {
		t.Fatalf("E3：免费腿的实扣仍必须是 0，实得 %d", res.PointsUsed)
	}
	for _, bad := range []string{"points_equiv", "points_free"} {
		if strings.Contains(body, bad) {
			t.Fatalf("E3：出参仍带 %s：%s", bad, body)
		}
	}
}

// E4 口径函数：三态、两桶互不串账、并发累加不丢账。
func TestFreeTierUsageFreeBilledThreeStates(t *testing.T) {
	e := &Engine{}
	// ① 没有收集器（后台任务直调引擎）：必须 (0,false)，不许 panic
	if n, ok := e.UsageFreeBilledTokens(context.Background()); n != 0 || ok {
		t.Fatalf("E4：无收集器时应回 (0,false)，实得 (%d,%v)", n, ok)
	}
	ctx := e.WithUsageRecorder(context.Background())
	uc := llm.CollectorFrom(ctx)
	if uc == nil {
		t.Fatal("E4：WithUsageRecorder 必须注入收集器（注入不了＝判据失去落点）")
	}
	// ② 有收集器但这一档没被写过（付费单）：同样 (0,false)——「没这一维」与「这一维是 0」在调用方同形且都该不标注
	if n, ok := e.UsageFreeBilledTokens(ctx); n != 0 || ok {
		t.Fatalf("E4：付费腿应回 (0,false)，实得 (%d,%v)", n, ok)
	}
	// ③ 写入后读到累计值；且**不污染实收桶**（两桶串账＝F-49① 当场破约）
	uc.AddFreeBilled(12000)
	if n, ok := e.UsageFreeBilledTokens(ctx); n != 12000 || !ok {
		t.Fatalf("E4：应读到等值 12000，实得 (%d,%v)", n, ok)
	}
	if n := uc.BilledTotal(); n != 0 {
		t.Fatalf("E4：AddFreeBilled 污染了实收桶（BilledTotal=%d）⇒ 免费单会报出 points_used>0", n)
	}
	// ④ 非正数不记账：AddFreeBilled(0) 不许把「没这一维」翻成「这一维是 0」
	uc.AddFreeBilled(0)
	if n, ok := e.UsageFreeBilledTokens(ctx); n != 12000 || !ok {
		t.Fatalf("E4：AddFreeBilled(0) 不该改动累计值，实得 (%d,%v)", n, ok)
	}
	// ⑤ 并发累加不丢账（多语种并发翻译同时写同一只免费桶）
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); uc.AddFreeBilled(1000) }()
	}
	wg.Wait()
	if n, _ := e.UsageFreeBilledTokens(ctx); n != 20000 {
		t.Fatalf("E4：并发 AddFreeBilled 丢账，期望 12000+8000=20000，实得 %d", n)
	}
	// ⑥ 没有收集器时两个读函数都回 -1（调用方据此区分「一分没收」与「没有口径」）
	var nilUc *llm.UsageCollector
	if n := nilUc.FreeBilledTotal(); n != -1 {
		t.Fatalf("E4：nil 收集器的 FreeBilledTotal 必须回 -1，实得 %d", n)
	}
	if n := nilUc.BilledTotal(); n != -1 {
		t.Fatalf("E4：nil 收集器的 BilledTotal 必须回 -1，实得 %d", n)
	}
	nilUc.AddFreeBilled(100) // 不许解引用（那是 panic 不是降级）
	if nilUc.FreeBilledTotal() != -1 {
		t.Fatal("E4：nil 收集器上 AddFreeBilled 之后仍必须回 -1（读的是「没有口径」而不是 0）")
	}
}
