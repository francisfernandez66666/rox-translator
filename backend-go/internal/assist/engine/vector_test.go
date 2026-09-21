// ============ engine/vector_test.go · 职责说明 ============
// 第 3 级向量召回单测（httptest 假嵌入端点，零真实网络依赖）：
//   - 默认关闭：无 embed_recall 配置时零 HTTP 调用，行为与第 2 级方案一致；
//   - 启用命中：cosine 达阈值的条目以 vector 通道入池，且分数低于 exact；
//   - 失败降级：嵌入端点 500 时静默回落字面召回（不报错给前端），
//     且进入冷却窗口——冷却期内不再重复撞挂死端点；
//   - 另附第 2 级护栏：fuzzy 跨域条目不得触发复合让位（CI2 语义单元守护）。
//
// =============================================
package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"translator/internal/assist/llm"
	"translator/internal/assist/store"
)

// fakeEmbedSrv 假 OpenAI 兼容 /embeddings：按文本查表返回 2 维向量，
// 表外文本回落 (0,1)——与知识向量 (0.99,0.141) 余弦 ≈0.14，必不过 vecMinCosine。
// 表内键必须与 engine 侧拼接形态完全一致：
//   - 条目：title。keywords。content[:120]（ensureVectorIndex）
//   - 问句：原文
func fakeEmbedSrv(t *testing.T, reqs *int32, fail *atomic.Bool) *httptest.Server {
	t.Helper()
	table := map[string][2]float64{
		"发票管理。发票,开票。支持电子普票": {0.99, 0.141},
		"报销凭证怎么弄":           {0.95, 0.312},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(reqs, 1)
		if fail != nil && fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
			return
		}
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		data := make([]any, 0, len(req.Input))
		for _, s := range req.Input {
			v, ok := table[s]
			if !ok {
				v = [2]float64{0, 1}
			}
			data = append(data, map[string]any{"embedding": []float64{v[0], v[1]}})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}

// newVecEngine 两条知识的极简引擎：kb-invoice 与测试问句零字面重叠，
// 只可能经向量通道召回（exact/fuzzy 均不可达，通道归因干净）。
func newVecEngine(t *testing.T, baseURL string) (*Engine, *store.DB) {
	t.Helper()
	db, err := store.Open(t.TempDir() + "/vec.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, _ = db.Create("kb_entries", map[string]any{
		"key": "kb-invoice", "title": "发票管理", "priority": 7, "enabled": 1,
		"content": "支持电子普票", "keywords": "发票,开票", "link_keys": "billing",
	})
	_, _ = db.Create("kb_entries", map[string]any{
		"key": "kb-other", "title": "格式支持", "priority": 7, "enabled": 1,
		"content": "支持 docx", "keywords": "格式,docx", "link_keys": "",
	})
	client := llm.New([]llm.Provider{{Name: "main", BaseURL: baseURL, APIKey: "k", Model: "chat-m"}}, 5)
	return New(db, client), db
}

// enableVector 打开第 3 级开关（模拟管理台配置）
func enableVector(t *testing.T, db *store.DB) {
	t.Helper()
	_ = db.SetConfig("embed_recall", "on")
	_ = db.SetConfig("embed_model", "bge-m3")
}

// TestVectorDisabledByDefault 默认关闭：不配开关时零 HTTP 调用、该兜底仍兜底
func TestVectorDisabledByDefault(t *testing.T) {
	var n int32
	var fail atomic.Bool
	srv := fakeEmbedSrv(t, &n, &fail)
	defer srv.Close()
	e, _ := newVecEngine(t, srv.URL)
	// 「报销凭证怎么弄」与两条知识零字面重叠（exact/fuzzy 均不可达）
	if hits := e.RecallReport(context.Background(), "报销凭证怎么弄", 3); len(hits) != 0 {
		t.Fatalf("默认应零命中走兜底, got %+v", hits)
	}
	if atomic.LoadInt32(&n) != 0 {
		t.Fatalf("默认关闭却发起了 %d 次嵌入请求", n)
	}
}

// TestVectorEnabledHit 开启后向量通道召回语义近义问句，且分数处于最低层级
func TestVectorEnabledHit(t *testing.T) {
	var n int32
	var fail atomic.Bool
	srv := fakeEmbedSrv(t, &n, &fail)
	defer srv.Close()
	e, db := newVecEngine(t, srv.URL)
	enableVector(t, db)

	rep := e.RecallReport(context.Background(), "报销凭证怎么弄", 3)
	via, score := "", 0
	for _, h := range rep {
		if h.Key == "kb-invoice" {
			via, score = h.Via, h.Score
		}
	}
	if via != viaVector {
		t.Fatalf("kb-invoice 应经 vector 通道命中: %+v", rep)
	}
	if score >= 10 || score < 1 {
		t.Fatalf("vector 分数越界: %d", score)
	}
	if atomic.LoadInt32(&n) == 0 {
		t.Fatal("启用后应发生嵌入调用")
	}
}

// TestVectorFailureDegradesSilently 嵌入失败：回落字面召回不报错，冷却期内不重复打端点
func TestVectorFailureDegradesSilently(t *testing.T) {
	var n int32
	var fail atomic.Bool
	fail.Store(true)
	srv := fakeEmbedSrv(t, &n, &fail)
	defer srv.Close()
	e, db := newVecEngine(t, srv.URL)
	enableVector(t, db)

	// 第一次：端点 500 → 向量通道静默失效，exact 命中不受影响、不报错
	rep := e.RecallReport(context.Background(), "支持哪些格式", 3)
	if len(rep) == 0 || rep[0].Key != "kb-other" || rep[0].Via != viaExact {
		t.Fatalf("降级后 exact 命中丢失: %+v", rep)
	}
	first := atomic.LoadInt32(&n)
	if first == 0 {
		t.Fatal("首次应尝试嵌入")
	}
	// 第二次：60s 冷却窗口内 → 不再撞端点（请求计数不涨），字面召回照常
	rep = e.RecallReport(context.Background(), "docx 格式", 3)
	if len(rep) == 0 {
		t.Fatalf("冷却期误伤字面召回: %+v", rep)
	}
	if atomic.LoadInt32(&n) != first {
		t.Fatalf("冷却失效: 嵌入请求从 %d 涨到 %d", first, atomic.LoadInt32(&n))
	}
}

// TestCompoundIntentIgnoresSoftChannels 复合让位只认 exact：fuzzy 跨域条目不得
// 把单意图快答（话术直配）带偏成融合应答（CI2/UAT A2 语义的单元级守护）。
func TestCompoundIntentIgnoresSoftChannels(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/ci.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, _ = db.Create("scripts", map[string]any{
		"key": "sc-price", "stype": "keyword", "title": "价格", "priority": 8, "enabled": 1,
		"content": "按积分计费", "keywords": "多少钱", "link_keys": "",
	})
	// 仅 fuzzy 可达的跨域条目：关键词「大概多少呀」经归一（去语气词）后 bigram
	// 完整包含于输入「这个东西大概多少钱」，但原始串并非输入子串（exact 不可达）。
	// 若让位判定不设 via==exact 闸，此条目会把单意图快答抢成融合应答。
	_, _ = db.Create("kb_entries", map[string]any{
		"key": "kb-fuzzy-cross", "title": "字数与额度", "priority": 5, "enabled": 1,
		"content": "无关跨域内容", "keywords": "大概多少呀", "link_keys": "",
	})
	e := New(db, llm.New(nil, 5))
	// 前置自检：确认该条目确实以 fuzzy 通道出现在检索结果里（否则本测试失去守护意义）
	found := false
	for _, h := range e.RecallReport(context.Background(), "这个东西大概多少钱", 4) {
		if h.Key == "kb-fuzzy-cross" && h.Via == viaFuzzy {
			found = true
		}
	}
	if !found {
		t.Fatal("夹具失效：kb-fuzzy-cross 未经 fuzzy 通道命中，本守护测试失去意义")
	}
	rep := e.Respond(context.Background(), "s-ci-soft", "这个东西大概多少钱", "/", nil)
	if rep.Source != "rule" {
		t.Fatalf("fuzzy 跨域条目不得触发让位: source=%s %+v", rep.Source, rep)
	}
}
