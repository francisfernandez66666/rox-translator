// system_values_test.go — ★ 082x 第七条（2026-09-29 换件后现网复问第二批·最严重的一条）：
// **算术不许交给模型**。上一批把系数注进【系统现值】之后，模型拿到系数真的当场自己乘：
//
//	一条把 2000 个英文单词按「1 词 ≈ 400 字符」折算（平台按源字符计费，字数与字符之间没有官方换算），
//	另一条直接报「800credits + 7.5 = 807.5 credits（约 ¥80）」——两个数谁都没核过。
//	这是**对外错报价**，和 F-12「报价三口径打架」同级，只是这次分叉发生在模型的心算里。
//
// 修法是把总额挪到服务端：renderQuoteExamples 按现值算好三档（含人民币约等数），
// 配一条报价纪律（禁自乘除、禁字数换算、禁报示例之外的总额）。
// 所以这里锁三件事：① 算出来的每个数字与系数**逐项等值**（单向"要有数字"是假绿）；
// ② 取不到现值时整段不出现（半截的系数比没有系数更危险，模型会拿 0 去算钱）；
// ③ 纪律那句话必须在（只给数字不给纪律，示例会被当成"参考"继续自己算）。
package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// pricingDocForTest 造一份计费现值（fast 每千字符 4、pro 每千字符 8，建单固定 7.5，1 积分≈0.1 元）。
// 走 JSON 反序列化而不是拼匿名结构体字面量：字段与主服务 /api/pricing/meta 的出栈名同源，
// 那边改名时这里会直接解不出来而红灯，比抄一份字段声明更可靠。
func pricingDocForTest() *pricingMetaDoc {
	var doc pricingMetaDoc
	raw := `{"success":true,"unit":"积分","points_price_money":0.1,"modes":[
		{"code":"fast","points_per_1k_chars":4,"points_fixed":7.5},
		{"code":"pro","points_per_1k_chars":8,"points_fixed":7.5}]}`
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		panic("计费现值夹具解析失败：" + err.Error())
	}
	return &doc
}

func TestRenderQuoteExamplesAreComputedNotPromised(t *testing.T) {
	block := renderQuoteExamples(pricingDocForTest())
	if block == "" {
		t.Fatal("有现值却没生成报价示例（模型只能自己乘，就是现网那两条错报的形态）")
	}
	// ① 逐项等值：points = 每千字符系数 × 字符/1000 + 建单固定；money = points × 单价（收 2 位）
	wantLines := []string{
		"1000 源字符 = 11.5 积分（约 1.15 元）",
		"5000 源字符 = 27.5 积分（约 2.75 元）",
		"20000 源字符 = 87.5 积分（约 8.75 元）",
		"1000 源字符 = 15.5 积分（约 1.55 元）",
		"5000 源字符 = 47.5 积分（约 4.75 元）",
		"20000 源字符 = 167.5 积分（约 16.75 元）",
	}
	for _, w := range wantLines {
		if !strings.Contains(block, w) {
			t.Fatalf("示例里缺 %q：\n%s", w, block)
		}
	}
	// ② 两个模式各自行，数字不许串档（串了就是把快速模式的钱报给专业模式的客户）
	if !strings.Contains(block, "快速模式：") || !strings.Contains(block, "专业模式：") {
		t.Fatalf("示例没按模式分行：\n%s", block)
	}
	// ③ 纪律三句：禁自算、禁字数换算、禁报示例外的总额
	for _, w := range []string{"禁止自己做任何乘除", "没有官方换算", "禁止报上面示例之外的总额", "约 X 元"} {
		if !strings.Contains(block, w) {
			t.Fatalf("报价纪律缺 %q（只给示例不给纪律，示例会被当成参考数字）：\n%s", w, block)
		}
	}
	// ④ 现值缺失＝整段不出现（与文件头第 2 条同一口径，绝不编一份示例）
	if got := renderQuoteExamples(nil); got != "" {
		t.Fatalf("没有现值时不许编示例，实际：%q", got)
	}
	if got := renderQuoteExamples(&pricingMetaDoc{Success: true}); got != "" {
		t.Fatalf("模式表为空时不许编示例，实际：%q", got)
	}
	// ⑤ 系数调档示例必须跟着变：示例是**派生读数**，不是第二份写死的价目表
	other := pricingDocForTest()
	other.Modes[0].PointsPer1kChars = 5
	if !strings.Contains(renderQuoteExamples(other), "1000 源字符 = 12.5 积分") {
		t.Fatal("系数调档后示例数字没跟着变（那就是第二份价目表，迟早与现值分叉）")
	}
}

// TestSystemValuesBlockAbsentWhenPricingMissing 「取不到就整段不出现」在现值段一侧。
// 只有语种数、没有计费系数时，示例与报价纪律都不该出现——那会让人以为数字是真取到的。
func TestSystemValuesBlockAbsentWhenPricingMissing(t *testing.T) {
	only := renderSystemValues("http://127.0.0.1:8787", nil, 35)
	if !strings.Contains(only, "可选目标语言") {
		t.Fatalf("语种数那行该在：\n%s", only)
	}
	for _, bad := range []string{"现算示例", "报价纪律", "源字符 ="} {
		if strings.Contains(only, bad) {
			t.Fatalf("没取到计费现值却出现了 %q（半截报价比不报价更容易被念给客户）：\n%s", bad, only)
		}
	}
	if renderSystemValues("http://127.0.0.1:8787", nil, 0) != "" {
		t.Fatal("两项都没取到时应返回空串（整段不拼）")
	}
	// 系数与示例同源渲染：整段里两者都在
	full := renderSystemValues("http://127.0.0.1:8787", pricingDocForTest(), 35)
	if !strings.Contains(full, "每 1000 源字符·单语种 4 积分") || !strings.Contains(full, "11.5 积分") {
		t.Fatalf("现值段里系数与示例没同源：\n%s", full)
	}
}

// TestSystemValuesExamplesReachSystemPrompt 示例必须真的进**发给模型的提示词**，
// 而不是只活在渲染函数里（抓真出站请求体是本批语言类断言的统一做法）。
func TestSystemValuesExamplesReachSystemPrompt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/pricing/meta"):
			_ = json.NewEncoder(w).Encode(pricingDocForTest())
		case strings.HasSuffix(r.URL.Path, "/translation/langs"):
			_, _ = w.Write([]byte(`{"kb_langs":[{"code":"en"},{"code":"ja"}]}`))
		default:
			http.Error(w, "nope", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	e := newTestEngine(t)
	_ = e.db.SetConfig("main_base_url", srv.URL)
	sys := e.buildSystemPrompt(context.Background(), nil, "en")
	for _, want := range []string{"【系统现值】", "现算示例", "11.5 积分", "禁止自己做任何乘除"} {
		if !strings.Contains(sys, want) {
			t.Fatalf("发给模型的系统提示词里缺 %q：\n%s", want, sys)
		}
	}
}
