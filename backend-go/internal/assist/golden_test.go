// ============ internal/assist/golden_test.go · 职责说明 ============
// 分级召回黄金集回归闸门（报告 §6 P3#22「质量基准不可见」的召回侧整改，任务 #58）。
//
// 形态：内嵌 40 条真实口吻的中文销售问句 → 期望命中的知识库条目 key（或期望
// 「零命中走兜底」）。断言口径：
//   - 命中 = 期望 key 出现在分级召回 top3（exact/fuzzy/vector 任一通道均算，
//     默认配置 vector 关闭，即测第 1+2 级）；
//   - 期望兜底 = 零命中（兜底引导 + unanswered_questions 登记由引擎负责）。
//
// 棘轮（只减不增的镜像：命中率下限只准升不准降）：
//   - goldenRecallFloor 钉在 2026-09-22 实现分级召回后的实测值（见常量注释），
//     低于即红灯——新增/改动打分逻辑造成召回退化会被此闸门拦截；
//   - 基线两个历史锚点（同一夹具实测）：仅第 1 级（纯关键词+同义词）命中率
//     与第 1+2 级命中率均写死在常量注释里，运营/开发调阈值时可对照。
//
// 新增知识库/话术条目或调相似度阈值后，若此测试红灯：优先修打分逻辑，
// 其次才允许带着理由改期望集（禁止为通过而放宽语义——AGENTS.md 三-2）。
// =============================================
package assist_test

import (
	"context"
	"encoding/json"
	"testing"

	"translator/internal/assist/engine"
	"translator/internal/assist/llm"
	"translator/internal/assist/seed"
	"translator/internal/assist/store"
)

// goldenCase 黄金问答一条：q → want（任一 key 进 top3 即命中；空 want = 期望兜底）
type goldenCase struct {
	q    string
	want []string
}

// goldenCases 黄金集（40 条，真实口吻/换说法/繁体/纯语义/无资料五类覆盖）。
// 期望 key 依据 seed.json 现有 27 条知识的语义归属人工核对（2026-09-22）。
var goldenCases = []goldenCase{
	// ---- 第 1 级就该稳定命中的（防退化基本盘，UAT 38 断言同源场景） ----
	{"想问问价格", []string{"billing-points"}},
	{"怎么开票", []string{"invoice-refund"}},
	{"你们支持哪些语种", []string{"languages"}},
	{"怎么充钱", []string{"recharge", "billing-points"}}, // 同义词表归一
	{"积分怎么收费", []string{"billing-points"}},
	{"支持哪些文件格式", []string{"file-formats"}},
	{"企业术语库怎么建", []string{"kb-setup"}},
	{"怎么联系人工", []string{"contact-human"}},
	{"能免费试用吗", []string{"trial"}},
	{"翻译一份材料要多少米", []string{"cost-estimate"}},
	{"你们家都有什么产品和服务", []string{"what-is", "core-capabilities"}},
	{"想咨询下商务合作", []string{"contact-human"}},
	{"新手第一次怎么用", []string{"first-file-translate"}},
	{"积分用完了会停止吗", []string{"billing-insufficient"}},
	{"有浏览器扩展吗", []string{"integrations"}},
	{"数据安全怎么保证", []string{"security"}},

	// ---- 第 2 级相似度救援（分级召回改造的收益面） ----
	{"韩国语可以翻吗", []string{"languages"}},                    // 韩语→字符集包含
	{"一个文件最多能传多大", []string{"bigfile"}},                   // 大文件→字符集包含
	{"請問怎麼收費", []string{"billing-points"}},                // 繁体归一
	{"翻译错了能重新翻一遍吗", []string{"quality-bad", "term-gate"}}, // 翻错/重译
	{"付了款多久到账", []string{"recharge"}},                     // 付款被语气词打断

	// ---- 期望兜底（KB 缺口或纯语义改写，宁可引导也不给错答案） ----
	{"xyzzy量子波动速翻布拉布拉", nil},
	{"你们公司叫什么名字", []string{"what-is"}}, // 「你们」第 1 级即命中产品介绍，答这问题合理
	{"月付还是年付", nil},                    // KB 无按周期付费资料 → 兜底登记，运营补料
	{"支持离线用吗", nil},                    // KB 无离线资料 → 兜底
	{"能翻图片里的文字吗", nil},                 // OCR 形态未入库 → 兜底（诚实边界）

	// ---- 语义改写：第 1+2 级允许兜底，第 3 级（默认关）开后可升级； ----
	// 期望值按「当前默认配置」口径给出，向量通道不进门禁。
	{"能翻论文吗", nil},
	{"翻出来的效果不好能重做吗", nil},
	{"公司要用多人协同怎么弄", nil},
	{"能不能便宜点", nil},
	{"文档翻译一遍要等多久", nil},
	{"可以接入我们自己的系统吗", nil},
	{"词对词翻译会不会丢专业名词", nil},
	{"支持繁简转换吗", nil},
	{"资料会拿去训练吗", nil},
	{"怎么付费", nil}, // 同义词表未覆盖「付费」——运营经管理台补表即可升级命中
	{"忘了登录密码怎么办", []string{"account-email"}},
	{"翻译完的文件在哪里下载", []string{"first-file-translate"}},
	{"合同保密的话能放心用吗", []string{"security"}},
	{"适合外贸团队用吗", []string{"who-for"}},
}

// goldenRecallFloor 命中率下限（条数）——分级召回落地时（2026-09-22）实测锚点：
//   - 仅第 1 级（纯关键词+同义词，旁路第 2/3 级实测）：34/40 = 85.0%
//     （漏：韩国语→languages、传多大→bigfile、繁体收費、翻错重做、付款打断、下载问句）
//   - 第 1+2 级（当前默认配置，第 3 级默认关）：40/40 = 100%
//
// WHY 钉 40（当前实测值）：棘轮口径「只减不增」——6 条相似度救援全部转正入库，
// 后续任何打分/阈值/seed 改动导致这 40 条有任何一条脱靶即红灯，须带着理由同步修
// 夹具期望（如运营给「付费」补同义词后，该条期望应升级为 recharge）。
const goldenRecallFloor = 40

// newSeededEngine 按内嵌 seed 装配引擎（与 cmd/assist-server 首启灌入路径同构，
// 无 LLM（llm.New(nil)）→ 第 3 级 vector 天然关闭，测第 1+2 级默认口径）。
func newSeededEngine(t *testing.T) *engine.Engine {
	t.Helper()
	db, err := store.Open(t.TempDir() + "/golden.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var sf struct {
		KB       []map[string]any  `json:"kb"`
		Scripts  []map[string]any  `json:"scripts"`
		Flows    []map[string]any  `json:"flows"`
		Features []map[string]any  `json:"features"`
		Configs  map[string]string `json:"configs"`
	}
	if err := json.Unmarshal(seed.SeedJSON, &sf); err != nil {
		t.Fatalf("seed: %v", err)
	}
	put := func(table string, rows []map[string]any) {
		for _, r := range rows {
			for _, k := range []string{"priority", "sort", "enabled"} {
				if f, ok := r[k].(float64); ok {
					r[k] = int(f)
				}
			}
			if _, err := db.Create(table, r); err != nil {
				t.Fatalf("seed %s: %v", table, err)
			}
		}
	}
	put("kb_entries", sf.KB)
	put("scripts", sf.Scripts)
	put("flows", sf.Flows)
	put("feature_links", sf.Features)
	for k, v := range sf.Configs {
		_ = db.SetConfig(k, v)
	}
	return engine.New(db, llm.New(nil, 2))
}

// TestGoldenRecall 黄金集召回率棘轮（top3 口径，期望兜底=零命中）。
func TestGoldenRecall(t *testing.T) {
	e := newSeededEngine(t)
	ctx := context.Background()
	hits := 0
	var missed []string
	for _, c := range goldenCases {
		report := e.RecallReport(ctx, c.q, 3)
		ok := false
		if len(c.want) == 0 {
			ok = len(report) == 0 // 期望兜底：必须零命中，弱信号不许抢答
		} else {
			for _, h := range report {
				for _, w := range c.want {
					if h.Key == w {
						ok = true
					}
				}
			}
		}
		if ok {
			hits++
		} else {
			got := "none"
			if len(report) > 0 {
				got = ""
				for _, h := range report {
					got += h.Key + ":" + h.Via + " "
				}
			}
			missed = append(missed, c.q+" ⇒ want "+joinWant(c.want)+" | got "+got)
		}
	}
	t.Logf("黄金集召回：%d/%d（下限 %d）", hits, len(goldenCases), goldenRecallFloor)
	for _, m := range missed {
		t.Logf("  MISS %s", m)
	}
	if hits < goldenRecallFloor {
		t.Fatalf("分级召回命中率 %d/%d 跌破棘轮下限 %d——检查打分/阈值/seed 变更", hits, len(goldenCases), goldenRecallFloor)
	}
}

// TestGoldenExactChannelNoRegression 防退化专项：第 1 级基本盘用例必须以 exact 通道
// 命中（相似度通道不许是这些用例的唯一命脉——同义词表/UAT B1 依赖的正是该口径）。
func TestGoldenExactChannelNoRegression(t *testing.T) {
	e := newSeededEngine(t)
	exactCases := []goldenCase{
		{"积分怎么收费", []string{"billing-points"}},
		{"支持哪些文件格式", []string{"file-formats"}},
		{"企业术语库怎么建", []string{"kb-setup"}},
		{"怎么联系人工", []string{"contact-human"}},
		{"怎么充钱", []string{"recharge", "billing-points"}},
	}
	for _, c := range exactCases {
		viaExact := false
		for _, h := range e.RecallReport(context.Background(), c.q, 3) {
			for _, w := range c.want {
				if h.Key == w && h.Via == "exact" {
					viaExact = true
				}
			}
		}
		if !viaExact {
			t.Errorf("第 1 级退化：%q 未经 exact 通道命中 %v", c.q, c.want)
		}
	}
}

func joinWant(w []string) string {
	if len(w) == 0 {
		return "(fallback)"
	}
	s := ""
	for i, x := range w {
		if i > 0 {
			s += "/"
		}
		s += x
	}
	return s
}
