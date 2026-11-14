// ============ assist_parity_gate_test.go · 职责说明 ============
// 四期 §4.5.3 **A1**：答复逐字节等价——固定假上游 + 30 条语料，
// 「迁移前（跨进程验签注入）」与「迁移后（本地 WithSalesFacts 直注）」
// 两条 ctx 上的 engine.Reply 序列化必须逐字相等。
//
// ★ A1 的取法（§4.5.3 末）：必须用**同一个假上游**，否则「迁移后答复不同」
//
//	分不清是搬家坏了还是模型变了。本用例起一个 httptest 假上游，
//	返回固定 chat/completions 响应，两个 ctx 共用同一个 *llm.Client。
//
// ★ 不是空转锁：新链路的 ctx 由本仓 D 文件的 s.localFactsContext 真实产出，
//
//	且事实集经过 A 文件同款 Encode→Decode 保形。M0298 把 localFactsContext
//	的 WithSalesFacts 摘掉 ⇒ SalesFactsFromContext 失明 ⇒ 守卫改写分叉 ⇒ 红。
//
// 反证：① 30 条语料逐条比对；② 两个 ctx 里的事实集 ok 与 JSON 必须相等；
//
//	③ 另造一份「不同事实集」的 ctx，比对结果必须**不等**（证明比对函数有判别力）。
//
// ================================================
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	assistengine "translator/internal/assist/engine"
	assistllm "translator/internal/assist/llm"
	assiststore "translator/internal/assist/store"
	mainengine "translator/internal/engine"
)

// a1FixedUpstream 固定假上游：任何请求都回同一段正文，杜绝模型漂移。
func a1FixedUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"欢迎体验能言翻译，可以把文件传给我试试。"},"finish_reason":"stop"}],"usage":{}}`))
	}))
}

// a1Corpus 30 条固定语料（覆盖价格/能力/格式/充值/竞品等销售轮，触发不同召回与守卫）。
var a1Corpus = []string{
	"多少钱", "怎么充值", "一个字多少钱", "支持哪些语言", "能翻译 PDF 吗",
	"PPT 翻完格式还在吗", "术语库怎么用", "有企业版吗", "和 DeepL 比怎么样",
	"怎么注册", "积分会过期吗", "能翻 Excel 吗", "有没有 API", "支持批量翻译吗",
	"翻译要多久", "能保留图片吗", "怎么开票", "有试用吗", "包月多少钱",
	"能翻日文吗", "品牌名能固定吗", "审校功能在哪", "支持多少语种",
	"文件最大多大", "怎么联系我们", "能开发票吗", "会员有什么权益",
	"翻译准确吗", "能翻扫描件吗", "怎么开通团队版",
}

// a1ReplyBytes 用同一把尺子把 Reply 序列化（json.Marshal 字段顺序稳定）。
func a1ReplyBytes(t *testing.T, eng *assistengine.Engine, ctx context.Context, sid, q string) []byte {
	t.Helper()
	rep := eng.Respond(ctx, sid, q, "", "zh", nil)
	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("A1 序列化 Reply 失败: %v", err)
	}
	return b
}

// TestA1AssistParityReplyByteEqual 迁移前后 30 条语料答复逐字等值。
func TestA1AssistParityReplyByteEqual(t *testing.T) {
	up := a1FixedUpstream(t)
	defer up.Close()
	client := assistllm.New([]assistllm.Provider{
		{Name: "main", BaseURL: up.URL, APIKey: "parity-fixed", Model: "fixed-model"},
	}, 5)
	adb, err := assiststore.Open(filepath.Join(t.TempDir(), "assist.db"))
	if err != nil {
		t.Fatalf("A1 造 assist 库失败: %v", err)
	}
	defer adb.Close()
	eng := assistengine.New(adb, client)

	// 一份有判别力的事实集（Facts + Controlled 都非空，守卫会真读它）。
	set := mainengine.SalesFactsSet{
		TenantID: 1,
		Lang:     "zh",
		Facts: []mainengine.SalesFact{
			{Kind: "price", Key: "unit_price", Value: "按字数计费", Lang: "zh"},
		},
		Controlled: []mainengine.ControlledKey{{Kind: "capability", Key: "语种数"}},
	}
	// 迁移前：跨进程验签后 assist 侧 InjectSalesFacts 的等价形态。
	oldCtx := assistengine.InjectSalesFacts(context.Background(), set)
	// 迁移后：本地链路——Encode→Decode 保形（A 文件口径）→ s.localFactsContext 直注。
	payload, err := mainengine.EncodeSalesFacts(set)
	if err != nil {
		t.Fatalf("A1 EncodeSalesFacts 失败: %v", err)
	}
	decoded, err := mainengine.DecodeSalesFacts(payload)
	if err != nil {
		t.Fatalf("A1 DecodeSalesFacts 失败: %v", err)
	}
	s := &Server{}
	newCtx := s.localFactsContext(context.Background(), decoded)

	// ② 两个 ctx 里的事实集 ok 与内容必须同形（守卫读的是同一个 ctx key）。
	oldSet, okOld := mainengine.SalesFactsFromContext(oldCtx)
	newSet, okNew := mainengine.SalesFactsFromContext(newCtx)
	if okOld != okNew {
		t.Fatalf("A1 事实集注入 ok 分叉：迁移前=%v 迁移后=%v（M0298 摘掉 WithSalesFacts 必红）", okOld, okNew)
	}
	bOld, _ := json.Marshal(oldSet)
	bNew, _ := json.Marshal(newSet)
	if !bytes.Equal(bOld, bNew) {
		t.Fatalf("A1 事实集内容不等值：迁移前=%s 迁移后=%s", bOld, bNew)
	}

	// ① 30 条语料逐条比对 Reply 序列化。
	for i, q := range a1Corpus {
		gotOld := a1ReplyBytes(t, eng, oldCtx, fmt.Sprintf("parity-old-%d", i), q)
		gotNew := a1ReplyBytes(t, eng, newCtx, fmt.Sprintf("parity-new-%d", i), q)
		if !bytes.Equal(gotOld, gotNew) {
			t.Fatalf("A1 第 %d 条语料 %q 答复不等值：\n迁移前=%s\n迁移后=%s", i, q, gotOld, gotNew)
		}
	}
	if len(a1Corpus) != 30 {
		t.Fatalf("A1 语料只有 %d 条（要求 30）：少了语料＝少一个守卫的覆盖面", len(a1Corpus))
	}

	// ③ 反证（射程自证）：走**同一个本地注入点**换一份事实集，ctx 里读到的
	//    必须是另一份内容；若 localFactsContext 恒不注入 / 恒返回同一份 set，
	//    上面 ② 的 ok 比对与 30 条等值就都成了空转锁，这条立即红。
	changed := mainengine.SalesFactsSet{TenantID: 1, Lang: "zh", Facts: []mainengine.SalesFact{
		{Kind: "price", Key: "unit_price", Value: "完全不同的另一份事实", Lang: "zh"},
	}}
	changedCtx := s.localFactsContext(context.Background(), changed)
	gotChanged, okChanged := mainengine.SalesFactsFromContext(changedCtx)
	if !okChanged {
		t.Fatal("A1 反证失明：localFactsContext 没有把事实集放进 ctx（M0298 摘掉 WithSalesFacts 必红）")
	}
	bc, _ := json.Marshal(gotChanged)
	if bytes.Equal(bc, bNew) {
		t.Fatal("A1 反证失明：换了事实集但 ctx 里读到的仍是同一份 ⇒ 注入点空转")
	}
}
