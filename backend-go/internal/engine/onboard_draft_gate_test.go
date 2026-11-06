// ============ onboard_draft_gate_test.go · 职责说明 ============
// 引导页「自由描述 → 候选字段」判定腿的四把锁（★ 2026-11-07）：
//
//	A1 allowance **按构造恒 0**（AGENTS §28 同形）：喂不出非零，且函数体里不许长出乘法／
//	   ScriptTokens——将来塞进一个非零数必须在这条上红，而不是等客户账单对不上才发现。
//	A2 提示词只有一份：包级常量声明一次、调用点引用一次，全仓不许出现第二份抄本
//	   （§一·5④／§22「预估从真发出去的那句提示词现量」的最小形态——这里连"第二份"都不许有）。
//	A3 防御式解析的七档行为：正常／带围栏／前后寒暄／截断／非对象／语种三维容错／语种解不出出空数组。
//	   ⚠️ 截断那一档必须判失败：半截 JSON 最可能断在 target_langs 中间，
//	   "将就用读出来的那一两个键"就是给失败背书（§三 那条"读数不许为失败背书"）。
//	A4 没配阶段模型 ⇒ **一次都不拨**（§21 的 ocr_fix 教训的落地判据）：
//	   reason=no_stage_model 且上游命中数恒 0。只测"回了空草稿"测不到这一维——
//	   白拨一次翻译模型同样出空草稿，而客户等待与平台成本都真的付出去了。
//
// 方言口径（AGENTS §一·4）：涉及 config.C 的用例一律自钉 sqlite 并还原。
// ======================================================
package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"translator/internal/config"
)

// pinEngineSqliteDialect 自钉 SQLite 方言（config.Default() 会写全局 config.C，不还原会漏给同包后续用例）。
func pinEngineSqliteDialect(t *testing.T) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
}

// A1：allowance 恒 0（无入参＝任何输入同一个数），且函数体没有"算出来"的形态。
func TestOnboardDraftAllowanceIsZeroByConstruction(t *testing.T) {
	for i := 0; i < 5; i++ {
		if got := OnboardDraftAllowanceTokens(); got != 0 {
			t.Fatalf("引导页草稿识别的模型成本由平台承担 ⇒ 客户侧 allowance 必须恒 0，实得 %d", got)
		}
	}
	src, err := os.ReadFile("costmodel.go")
	if err != nil {
		t.Fatalf("读不到 costmodel.go: %v", err)
	}
	i := strings.Index(string(src), "func OnboardDraftAllowanceTokens")
	if i < 0 {
		t.Fatal("第 7 支 allowance 缺席（api/estimate.go 那处相加就没有委托对象了）")
	}
	seg := string(src)[i:]
	if j := strings.Index(seg, "\n}"); j > 0 {
		seg = seg[:j]
	}
	// ★ 判"有没有算式"必须**先剥行注释**：函数体里那句说明性的 `//` 本身就带斜杠，
	//   不剥的话这条腿会把"写了注释"读成"写了除法"（判据自伤，AGENTS §三 那一族）。
	body := seg
	var kept []string
	for _, line := range strings.Split(body, "\n") {
		if k := strings.Index(line, "//"); k >= 0 {
			line = line[:k]
		}
		kept = append(kept, line)
	}
	body = strings.Join(kept, "\n")
	// 反证靶子（M 枚的等价形态）：把 `return 0` 写成任何非零数、或引入 token 折算／乘除，都在这一条红。
	if strings.Contains(body, "ScriptTokens(") || strings.Contains(body, "*") || strings.Contains(body, "/") {
		t.Fatalf("引导页 allowance 不许按提示词或系数算 token（该成本平台承担）：\n%s", body)
	}
	if strings.Count(body, "return") != 1 || !strings.Contains(body, "return 0") {
		t.Fatalf("引导页 allowance 函数体必须只剩 `return 0` 这一行返回，实得：\n%s", body)
	}
}

// A2：提示词全仓只有一份，且调用点用的就是那一份。
func TestOnboardDraftPromptIsTheOneCalled(t *testing.T) {
	raw, err := os.ReadFile("onboard_draft.go")
	if err != nil {
		t.Fatalf("读不到 onboard_draft.go: %v", err)
	}
	src := string(raw)
	if n := strings.Count(src, "const onboardDraftPrompt = `"); n != 1 {
		t.Fatalf("提示词常量必须只声明一次，实得 %d 次", n)
	}
	// 调用点必须引用常量本身（不许在 messages 里就地内联一份第二抄本）。
	if !strings.Contains(src, `{"role": "system", "content": onboardDraftPrompt}`) {
		t.Fatal("system 消息必须直接引用 onboardDraftPrompt（内联一份＝在预估与实扣之外再加一把「提示词尺子」）")
	}
	// 跨包反向腿：api 侧那个新文件里不许出现提示词的任意一句特征串。
	// ⚠️ 判据按**特征串**而不是按常量名：抄本不会保留变量名，只会保留那句话。
	apiFile, err := os.ReadFile(filepath.Join("..", "api", "product_onboard_draft.go"))
	if err != nil {
		t.Fatalf("读不到 api/product_onboard_draft.go: %v", err)
	}
	for _, probe := range []string{"只输出一个 JSON 对象", "四条规则，一条都不许越", "可选语种清单（只能从这里挑）"} {
		if strings.Contains(string(apiFile), probe) {
			t.Fatalf("api 侧出现了提示词抄本：%q（两份提示词迟早只改一处）", probe)
		}
	}
}

// A3：防御式解析的七档行为（每档都点名，缺档即红）。
func TestParseOnboardDraftJSONBehaviorTable(t *testing.T) {
	cases := []struct {
		name         string
		in           string
		wantOK       bool
		wantName     string
		wantTagline  string
		wantUseCase  string
		wantLangsCSV string
	}{
		{"正常四键", `{"name":"整机 A9","tagline":"月产 1200 台","use_case":"东南亚代工","target_langs":["en","ja"]}`,
			true, "整机 A9", "月产 1200 台", "东南亚代工", "en,ja"},
		{"带代码围栏", "```json\n{\"name\":\"A9\",\"tagline\":\"\",\"use_case\":\"\",\"target_langs\":[]}\n```",
			true, "A9", "", "", ""},
		{"前后寒暄", "好的：{\"name\":\"A9\",\"tagline\":\"x\",\"use_case\":\"y\",\"target_langs\":[\"ko\"]} 以上。",
			true, "A9", "x", "y", "ko"},
		{"截断（缺右括号）", `{"name":"A9","tagline":"x","use_case":"y","target_langs":["en"`, false, "", "", "", ""},
		{"整段不是对象", `这是一段说明文字，没有 JSON`, false, "", "", "", ""},
		{"语种混进数字", `{"name":"A9","target_langs":["en",7,"ja"]}`, true, "A9", "", "", "en,ja"},
		{"语种写成一个串", `{"name":"A9","target_langs":"en, ja ,, "}`, true, "A9", "", "", "en,ja"},
		{"没有 target_langs 键", `{"name":"A9","tagline":"x"}`, true, "A9", "x", "", ""},
	}
	for _, c := range cases {
		got, ok := parseOnboardDraftJSON(c.in)
		if ok != c.wantOK {
			t.Fatalf("%s：ok 应=%v，实得 %v（正文=%q）", c.name, c.wantOK, ok, c.in)
		}
		if !ok {
			if got.Name != "" || got.Tagline != "" || got.UseCase != "" || len(got.TargetLangs) != 0 {
				t.Fatalf("%s：判失败时不许还带着读出来的半截值（那等于给失败背书）：%+v", c.name, got)
			}
			continue
		}
		if got.Name != c.wantName || got.Tagline != c.wantTagline || got.UseCase != c.wantUseCase {
			t.Fatalf("%s：文本三键等值失败：%+v", c.name, got)
		}
		if strings.Join(got.TargetLangs, ",") != c.wantLangsCSV {
			t.Fatalf("%s：语种应=%q，实得=%q", c.name, c.wantLangsCSV, strings.Join(got.TargetLangs, ","))
		}
	}
	// 空数组那一档必须是**非 nil 的空切片**：nil 会在 api 侧序列化成 null（§一·5 空值归一那条）。
	if got, ok := parseOnboardDraftJSON(`{"name":"x","target_langs":[]}`); !ok || got.TargetLangs == nil {
		t.Fatalf("target_langs 解出空集时不许留 nil：%+v ok=%v", got, ok)
	}
}

// A4：没配 onboard_draft 阶段模型 ⇒ 一次都不拨，且 reason 逐字钉住（对外排障契约）。
func TestExtractOnboardDraftWithoutStageModelNeverDials(t *testing.T) {
	pinEngineSqliteDialect(t)
	var hits int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"name\":\"不该被用到\"}"}}]}`))
	}))
	t.Cleanup(up.Close)

	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	cfg.OnlineAPIBase = up.URL + "/v1" // 全局端点是**可用**的：这样"没拨"才只可能来自阶段档没配
	cfg.OnlineAPIKey = "sk-test"
	cfg.OnlineModel = "global-translation-model"
	config.C = cfg

	eng := NewEngine(cfg, nil, nil, nil)
	eng.St = nil // 没有平台存储＝读不到 stage_models（新装/裸引擎形态）

	cand, reason := eng.ExtractOnboardDraft(context.Background(), "本公司月产 1200 台整机，面向东南亚客户。", "zh", []string{"en", "ja"})
	if reason != "no_stage_model" {
		t.Fatalf("没配阶段档时 reason 必须逐字=%q（档名是对外排障契约），实得 %q", "no_stage_model", reason)
	}
	if cand.Name != "" || cand.Tagline != "" || cand.UseCase != "" || len(cand.TargetLangs) != 0 {
		t.Fatalf("fail-open 必须回零值候选，实得 %+v", cand)
	}
	if n := atomic.LoadInt64(&hits); n != 0 {
		t.Fatalf("★ 回落链被接回来了：没配阶段档却拨了上游 %d 次（§21 的 ocr_fix 形态＝白等满预算再全超时）", n)
	}
	// 同一入口的另两条提前返回（各自都不许拨上游）。
	if _, r := eng.ExtractOnboardDraft(context.Background(), "  ", "zh", []string{"en"}); r != "no_text" {
		t.Fatalf("空描述必须回 reason=no_text，实得 %q", r)
	}
	if _, r := eng.ExtractOnboardDraft(context.Background(), "有内容", "zh", nil); r != "no_langs" {
		t.Fatalf("空语种清单必须回 reason=no_langs（没有清单＝没有可选项，不许让模型自由发挥），实得 %q", r)
	}
	if n := atomic.LoadInt64(&hits); n != 0 {
		t.Fatalf("两条提前返回也不许拨上游，实得命中 %d 次", n)
	}
}

// A5：健康面只出两个状态词，且**绝不**带端点/模型/Key（/api/health 匿名可达）。
func TestOnboardDraftHealthWordsCarryNoCoordinates(t *testing.T) {
	pinEngineSqliteDialect(t)
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	eng := NewEngine(cfg, nil, nil, nil)
	if got := eng.OnboardDraftHealthWord(context.Background()); got != "degraded" {
		t.Fatalf("裸引擎（无 St／无阶段档）必须是 degraded（动作＝去配），实得 %q", got)
	}
	if got := (*Engine)(nil).OnboardDraftHealthWord(context.Background()); got != "degraded" {
		t.Fatalf("nil 引擎也要能安全读数，实得 %q", got)
	}
	// nil 接收者走同一条 fail-open 分支（api 侧允许 s.Engine 尚未装配，绝不能 panic 成 500）。
	if _, r := (*Engine)(nil).ExtractOnboardDraft(context.Background(), "本公司月产 1200 台", "zh", []string{"en"}); r != "no_stage_model" {
		t.Fatalf("nil 接收者必须走同一条 fail-open 分支，实得 reason=%q", r)
	}
}
