// ============ chat_audio_qps_gate_test.go · 职责说明 ============
// G14 的锁（★ 2026-10-31 断言缺口补齐第二轮）：**语音识别这一腿也必须过 QPS／并发闸**。
//
// 为什么不能只做"接线形状"判据（那种判据在这里根本测不到东西）：
//
//	ASR 的闸门调用点在 `fileproc.AudioDuration` 之后，而时长探测要 python＋ffmpeg＋真音频，
//	单测里那条腿永远进不去（本仓 §一·17 早写过"能打开产物不算验收"）。
//	所以这里把 G14 拆成两半各判一次：
//	  ① **行为半段**：闸门本身在并发名额耗尽时必须拒绝，且**免费档开关不改变这一结果**
//	     （`gateUsageInstant` 只摘"余额＋预算墙"两段，QPS/并发/日限额照旧）。
//	     这一半证明"闸是真的会拦"，也顺手证明免费档没有把闸绕过去。
//	  ② **接线半段**：ASR 分支的两次闸门调用必须排在 `TranscribeAudio` 之前，
//	     失败分支必须走 `writeGateError`（同一套错误码出口），
//	     并且 `gateUsageInstant` 只能是 `gateUsageDepth(r,false)` 的薄委托——
//	     谁在 handler 里自抄一份 `TryQPS`/`TryAcquire`，§一·11「多把尺子」当场判红。
//	两半合起来才是"ASR 过闸"这句话的完整意思：缺一半都是空头支票。
//
// 方言：复用 `newPlatformCostProbe`（自钉 SQLite 内存库＋钉死 config.C，AGENTS §一·4）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run 'TestChatASR_(Gate|Wiring)'
// =============================================
package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"translator/internal/auth"
)

// ifcMemberTok 给探针租户里的普通成员签一张真 token（authUser 会回查库，令牌版本/uid 都要对得上）。
func ifcMemberTok(t *testing.T, p *platformCostProbe) string {
	t.Helper()
	u, err := p.s.Store.GetUser(p.memberID, p.tid)
	if err != nil || u == nil {
		t.Fatalf("取探针成员失败: %v", err)
	}
	tok, serr := auth.Sign(u, time.Hour)
	if serr != nil {
		t.Fatalf("签发成员 token 失败: %v", serr)
	}
	return tok
}

// TestChatASR_GateRejectsWhenSlotsBusy ① 行为半段：并发耗尽 ⇒ 付费闸与**免费闸**都必须拦。
func TestChatASR_GateRejectsWhenSlotsBusy(t *testing.T) {
	p := newPlatformCostProbe(t)
	tok := ifcMemberTok(t, p)

	// 先把这个租户的并发名额全部占住（上限读不出来就 fatal——那是"闸门没有并发档"，判据没有落点）
	var held []func()
	occupied := false
	for i := 0; i < 64; i++ {
		ok, rel := p.s.Bill.TryAcquire(p.tid)
		if !ok {
			occupied = true
			break
		}
		held = append(held, rel)
	}
	if !occupied {
		for _, rel := range held {
			rel()
		}
		t.Fatalf("射程自证失败：占满 64 个并发名额仍拿得到 ⇒ 本租户没有并发档，这条判据无从判定")
	}
	t.Cleanup(func() {
		for _, rel := range held {
			rel()
		}
	})

	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/chat/asr", nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		return r
	}
	// 免费档那条腿（ASR 30 秒内＝免费）必须照样吃并发闸
	if _, release, err := p.s.gateUsageInstant(req()); err == nil {
		release()
		t.Fatal("G14：并发名额耗尽时免费闸却放行了 ⇒ ASR 这一腿根本没有过闸（或免费档把闸整个绕过去了）")
	} else {
		release()
		if m := err.Error(); !strings.Contains(m, "并发") && !strings.Contains(m, "频繁") {
			t.Fatalf("G14：免费闸的拒绝文案不是 QPS/并发那一族（实得 %q）——那说明红的是别的原因，判据指错了地方", m)
		}
	}
	// 付费档同一条闸（30–60 秒那一档走这条）也必须拦
	if _, release, err := p.s.gateUsage(req()); err == nil {
		release()
		t.Fatal("G14：并发名额耗尽时付费闸却放行了")
	} else {
		release()
	}

	// 反向对照：名额还回去之后两条腿都必须放行——否则上面的红是"永远拦"，不是"被并发档拦"。
	for _, rel := range held {
		rel()
	}
	held = nil
	for name, gate := range map[string]func(*http.Request) (int64, func(), error){
		"免费闸": p.s.gateUsageInstant,
		"付费闸": p.s.gateUsage,
	} {
		_, release, err := gate(req())
		if err != nil {
			if release != nil {
				release()
			}
			t.Fatalf("G14 反向对照：%s 在名额空闲时仍拒（%v）⇒ 上面那条红不能算并发档的证据", name, err)
		}
		release()
	}
}

// TestChatASR_GateWiringIsBeforeTranscribe ② 接线半段：闸在识别之前，失败走统一出口，且没有第二把尺子。
func TestChatASR_GateWiringIsBeforeTranscribe(t *testing.T) {
	b, err := os.ReadFile("chat_audio.go")
	if err != nil {
		t.Fatalf("读 chat_audio.go 失败: %v", err)
	}
	code := stripGoLineComments(string(b))
	// ASR 分支的界：从 handleChatASR 到下一个顶层 func 为止
	start := strings.Index(code, "func (s *Server) handleChatASR")
	if start < 0 {
		t.Fatal("G14：找不到 handleChatASR（靶子搬家，闸门已失明）")
	}
	end := strings.Index(code[start+1:], "\nfunc ")
	if end < 0 {
		t.Fatal("G14：handleChatASR 函数体切不出来（判据无从定位）")
	}
	body := code[start : start+1+end]

	transcribe := strings.Index(body, "fileproc.TranscribeAudio(")
	if transcribe < 0 {
		t.Fatal("G14：ASR 分支里找不到 TranscribeAudio 调用（靶子搬家）")
	}
	for _, call := range []string{"s.gateUsage(r)", "s.gateUsageInstant(r)"} {
		i := strings.Index(body, call)
		if i < 0 {
			t.Fatalf("G14：ASR 分支缺少闸门调用 %s（识别是本地 CPU，没过闸就能被脚本按次烧）", call)
		}
		if i > transcribe {
			t.Fatalf("G14：%s 写在 TranscribeAudio **之后** ⇒ 先烧算力再问有没有资格，闸等于没有", call)
		}
	}
	if n := strings.Count(body, "writeGateError("); n < 2 {
		t.Fatalf("G14：ASR 两条闸门分支里 writeGateError 只出现 %d 次 ⇒ 有旁路把闸拒写成了自造错误体（AGENTS §一·8）", n)
	}
	// 负向锁：handler 里绝不允许自抄一份限流实现（那是 §一·11 的"多把尺子"）
	if regexp.MustCompile(`\.TryQPS\(|\.TryAcquire\(`).MatchString(body) {
		t.Fatal("G14：chat_audio.go 里出现了裸 TryQPS/TryAcquire ⇒ 造出了第二把尺子，档位与归还语义迟早和闸门分叉")
	}
	// 薄委托自证：gateUsageInstant 只能是 gateUsageDepth(r,false) 那一层
	fb, err := os.ReadFile("instant_free.go")
	if err != nil {
		t.Fatalf("读 instant_free.go 失败: %v", err)
	}
	fc := stripGoLineComments(string(fb))
	i := strings.Index(fc, "func (s *Server) gateUsageInstant")
	if i < 0 {
		t.Fatal("G14：找不到 gateUsageInstant 定义")
	}
	j := strings.Index(fc[i:], "\nfunc ")
	if j < 0 {
		t.Fatal("G14：gateUsageInstant 函数体切不出来")
	}
	if !strings.Contains(fc[i:i+j], "s.gateUsageDepth(r, false)") {
		t.Fatal("G14：gateUsageInstant 不再薄委托 gateUsageDepth(r,false) ⇒ 免费闸长成第二套实现，QPS/并发/日限额迟早只在一侧生效")
	}
}

// TestChatASR_GateSkipsMoneyOnlyForFreeTier G8／W18 的行为腿：
// 「免费档不看钱」只摘**余额＋预算墙**那两段，QPS/并发/日限额照旧——
// 这条不对称是 §5.4 的设计，也是 gateUsageInstant 存在的唯一理由；
// 把它写成"免费档整条闸门都跳过"，语音这条腿就能被脚本按次烧本地算力（G14 的另一面）。
func TestChatASR_GateSkipsMoneyOnlyForFreeTier(t *testing.T) {
	p := newPlatformCostProbe(t)
	tok := ifcMemberTok(t, p)
	// 强制计费开、且这个租户**一张额度授予都没有** ⇒ 付费闸必须拦、免费闸必须放
	if err := p.s.Store.SetConfig("billing_enforced", "1"); err != nil {
		t.Fatalf("开强制计费失败: %v", err)
	}
	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/chat/asr", nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		return r
	}
	if _, release, err := p.s.gateUsage(req()); err == nil {
		release()
		t.Fatal("G8 反向对照：余额为零、强制计费已开，付费闸却放行 ⇒ 这条判据失去了落点（上面那条免费放行也证明不了什么）")
	} else if release != nil {
		release()
	}
	if _, release, err := p.s.gateUsageInstant(req()); err != nil {
		if release != nil {
			release()
		}
		t.Fatalf("G8：0 积分账号被免费闸拦住了（%v）⇒ 免费档把余额墙一起搬回来了，W18 的不对称失效", err)
	} else {
		release()
	}
}
