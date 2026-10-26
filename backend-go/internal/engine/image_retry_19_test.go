// ============ 本文件职责中文说明 ============
// W4（2026-10-26 用户裁决 5／7）图片原位链「未译出行先充分重试，实在失败再在交付文档标注」的锁。
//
// 缺陷本体不是"翻坏了"，而是**重试这个动作从来没发生过**：
// 旧形态首翻没给某一行 ⇒ 图上与 .md 直接按原文顶上，日志里只留一个 residue 计数。
// 客户看见的还是中文，而"再给它几枪"这件事在代码里并不存在。
//
// 四条腿各自钉死一件事（都配正向对照，否则负向恒真＝空转）：
//
//	A 没译文的行 ⇒ 补译梯救回来 ⇒ tr 被补上、清单为空；
//	B 恒吐坏形态的行 ⇒ 进清单且**点名轮数**，坏候选绝不覆盖已到货的译文；
//	C 上游整条不通 ⇒ 早停不再逐行烧调用（**按上游被拨的次数判**，不靠日志措辞），
//	  但每一段仍进清单——早停省的是钱，不许顺手把"哪些行没翻"也一起省掉；
//	D 空段不进清单（它本来就没有可译内容，报出来是把设计当缺陷给客户看）。
//
// 本文件只起一个假上游（httptest），不连库、不跑真渲染脚本。
// ============================================
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/llm"
)

// imgStub 记录每一次请求的 user 段，并按注入的函数决定回复。
type imgStub struct {
	mu     sync.Mutex
	users  []string
	reply  func(user string) string
	finish string
}

func (s *imgStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	user := ""
	for _, m := range req.Messages {
		if m.Role == "user" {
			user = m.Content
		}
	}
	s.mu.Lock()
	s.users = append(s.users, user)
	reply := s.reply
	finish := s.finish
	s.mu.Unlock()
	content := ""
	if reply != nil {
		content = reply(user)
	}
	if finish == "" {
		finish = "stop"
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"choices":[{"message":{"content":%q},"finish_reason":%q}]}`, content, finish)
}

func (s *imgStub) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.users)
}

// imgEngine 只装配被测函数用到的零件（LLM 客户端＋配置），不连库。
// 参数：t、reply=假上游分派函数。返回 (engine, stub)。
func imgEngine(t *testing.T, reply func(user string) string) (*Engine, *imgStub) {
	t.Helper()
	stub := &imgStub{reply: reply}
	srv := httptest.NewServer(stub)
	t.Cleanup(srv.Close)
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	cfg.OnlineAPIBase = srv.URL + "/v1"
	cfg.OnlineAPIKey = "test-key"
	cfg.OnlineAPIKeyIsPlaceholder = false
	cfg.OnlineAPIKeyOrigin = config.OriginAPIKeyEnv // 假 Key 必须被当成可用 Key（档位锚点，见 AGENTS §一.3）
	cfg.OnlineModel = "test-model"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	return &Engine{LLM: llm.NewClient(cfg), Cfg: cfg, breaker: NewBreaker(5, time.Minute)}, stub
}

// TestImageRetrySalvagesMissingLine A 腿：首翻漏掉的一行由补译梯救回来，清单为空。
func TestImageRetrySalvagesMissingLine(t *testing.T) {
	const src = "蓝牙钥匙已激活，请勿拆卸。"
	const good = "The Bluetooth key has been activated. Do not disassemble it."
	e, stub := imgEngine(t, func(user string) string {
		if strings.Contains(user, "<s1>") {
			return "<s1></s1>" // 首翻这一格什么都没给＝未译出形态（空）
		}
		return good // 单段补译腿给出可用译文
	})
	texts := []string{src}
	tr := map[string]string{src: ""}
	notes, _ := e.retryImageSegments(context.Background(), texts, tr, "en")
	if tr[src] != good {
		t.Errorf("补译梯救回来的译文必须写回 tr（交付面才拿得到），实得 %q", tr[src])
	}
	if len(notes) != 0 {
		t.Errorf("救回来的行不许进「未译出内容」清单，实得 %+v", notes)
	}
	// 正向对照：本函数自己不发批量枪（首翻在链上游），所以这一条唯一该出现的上游请求
	// 就是补译梯那一枪——它必须在位，否则 A 腿的"救回来"是无中生有。
	if stub.calls() == 0 {
		t.Errorf("正向对照：补译梯一枪都没打 ⇒ A 腿的绿不是本函数给的，判据失明")
	}
}

// TestImageRetryListsPersistentBadShape B 腿：恒吐坏形态的行进清单并点名轮数；坏候选绝不覆盖已到货的译文。
func TestImageRetryListsPersistentBadShape(t *testing.T) {
	const src = "产品方案书"
	const refusal = "I am unable to provide the translation you requested" // 现网 M1 真实形态
	e, _ := imgEngine(t, func(user string) string { return refusal })
	texts := []string{src}
	tr := map[string]string{src: ""}
	notes, _ := e.retryImageSegments(context.Background(), texts, tr, "en")
	if len(notes) != 1 {
		t.Fatalf("恒坏形态的行必须进清单，实得 %+v", notes)
	}
	if notes[0].Line != 1 || notes[0].Source != src {
		t.Errorf("清单必须点名行号与原文（客户要能定位到具体那一行），实得 %+v", notes[0])
	}
	if !strings.Contains(notes[0].Reason, fmt.Sprint(e.LineRetryMax())) {
		t.Errorf("成因必须写明「补译几轮仍无可用输出」，实得 %q", notes[0].Reason)
	}
	if tr[src] == refusal {
		t.Errorf("坏候选绝不许覆盖交付值——回显／拒译话术交给客户比原文更坏（AGENTS §一.19 族三共同前提）")
	}
}

// TestImageRetryStopsWhenUpstreamDead C 腿：上游整条不通 ⇒ 早停不再逐行烧调用，但每段仍进清单。
func TestImageRetryStopsWhenUpstreamDead(t *testing.T) {
	min := 3 // LineDeadStreakMin 默认档（本用例不连库，走代码默认）
	texts := []string{"第一段说明。", "第二段说明。", "第三段说明。", "第四段说明。", "第五段说明。"}
	e, _ := imgEngine(t, func(user string) string { return "" }) // 恒空＝上游压根没吐
	tr := map[string]string{}
	notes, _ := e.retryImageSegments(context.Background(), texts, tr, "en")
	if len(notes) != len(texts) {
		t.Fatalf("早停省的是调用，不是「哪些行没翻」的可见性——5 段都必须进清单，实得 %+v", notes)
	}
	// 判据按**上游被拨的次数**：不早停时最多 5 段 × 每段 3 轮 = 15 枪；
	// 早停后触发前最多 (min-1) 段各 3 轮，剩余段一枪都不拨。
	// 判据按**被逐行重拨的段数**，不按 HTTP 请求数：一次单段补译在引擎内部自己还会走
	// 降级模型／截断抬预算等多跳，请求数会随上游形态浮动（拿它当阈值＝一条随时会假红的锁）。
	// 段数才是本函数的动作：触发早停**之前**最多 min 段被重拨，之后的段一枪都不拨。
	fired, skipped := 0, 0
	for _, n := range notes {
		switch {
		case strings.Contains(n.Reason, "未再逐行重拨"):
			skipped++
		case strings.Contains(n.Reason, "补译"):
			fired++
		}
	}
	if fired > min {
		t.Errorf("上游断后仍在逐行烧调用：被重拨的段数 %d > 阈值 %d ⇒ 早停没生效", fired, min)
	}
	if skipped == 0 {
		t.Errorf("一条都没跳过 ⇒ 早停根本没触发（fired=%d，5 段全打了＝旧形态）", fired)
	}
	late := notes[len(texts)-1]
	if !strings.Contains(late.Reason, "未再逐行重拨") && !strings.Contains(late.Reason, "补译") {
		t.Errorf("清单要分得开「早停没再打」与「打过仍坏」，最后一段成因实得 %q", late.Reason)
	}
}

// TestImageRetrySkipsEmptySegments D 腿：空段不进清单（没有可译内容，报出来是把设计当缺陷）。
func TestImageRetrySkipsEmptySegments(t *testing.T) {
	e, stub := imgEngine(t, func(user string) string { return "" })
	texts := []string{"", "   ", "\n"}
	tr := map[string]string{}
	notes, _ := e.retryImageSegments(context.Background(), texts, tr, "en")
	if len(notes) != 0 {
		t.Errorf("空段不许进未译出清单，实得 %+v", notes)
	}
	if stub.calls() != 0 {
		t.Errorf("空段一行都不该送上游（白烧客户积分），实得被问 %d 次", stub.calls())
	}
}
