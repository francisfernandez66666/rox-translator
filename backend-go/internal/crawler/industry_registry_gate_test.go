// ============ industry_registry_gate_test.go · 职责说明 ============
// 行业注册表的三处同源闸门（★ 2026-10-30 新增「金融 / 餐饮」两行业时建立）。
//
// 为什么必须有这一条锁（不是"代码整洁"问题，是**静默半成品**问题）：
//
//	一个行业要真正可用，同一个 code 必须同时出现在**三个地方**：
//	  ① crawler.builtinIndustrySeeds —— tier1（维基 langlinks）的种子词表；
//	  ② crawler.industryNames       —— tier3（llm_gen）提示词里的行业中文名；
//	  ③ 前端 lib/industries.ts 的 INDUSTRY_META —— 管理台/注册流程的行业下拉与展示名。
//	少任何一处都不会报错，只会留下一种"看着对、实际缺一条腿"的形态：
//	  · 缺 ①：该行业的采集源 base_url 又留空 ⇒ **静默回落到通用种子**
//	    （欢迎/谢谢/价格…），新行业包里躺着一堆问候语，而采集状态恒 `ok`；
//	  · 缺 ②：llm_gen 提示词里行业名变成 code 原文（"finance"），
//	    模型按字面理解 ⇒ 生成的还是泛泛词，账却记在该行业包上；
//	  · 缺 ③：租户根本选不到这个行业 ⇒ `tenants.industry` 落不进 finance，
//	    而 store.BuildPackScope 的可见性判据正是 `p.code == tenants.industry`——
//	    包建好了、词也采到了，**一条都不会注入**（这就是"行业适配感觉没实现"的第三种成因）。
//
// 判据取向：三处都**现算全集**再比集合差，不写死数量（写死的锁加一个行业就红，
// 那种红的归宿是被摘掉）；数量只作**射程自证下界**（现算结果小于下界＝解析漂了、闸门失明）。
// =============================================
package crawler

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// goKeys 取 map 的键全集（排序后便于做集合差）。
func goKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// nameKeys 取行业中文名表的键全集。
func nameKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// diffKeys 返回「在 a 不在 b」的差集（读错误信息的人要一眼看出少在哪一侧）。
func diffKeys(a, b []string) []string {
	in := map[string]bool{}
	for _, k := range b {
		in[k] = true
	}
	var out []string
	for _, k := range a {
		if !in[k] {
			out = append(out, k)
		}
	}
	return out
}

// TestIndustrySeedAndNameListsAgree R1：种子表与行业名表必须**同一个 code 全集**。
// 只加一处＝那条腿空转（见文件头三条形态里的①②），所以这一条按全集判、不逐条点名。
func TestIndustrySeedAndNameListsAgree(t *testing.T) {
	seeds, names := goKeys(builtinIndustrySeeds), nameKeys(industryNames)
	if len(seeds) < 10 {
		t.Fatalf("现算到的行业种子组只有 %d 个（<10）⇒ 判据已失明（不是「行业变少了」）", len(seeds))
	}
	if only := diffKeys(seeds, names); len(only) > 0 {
		t.Errorf("有行业**只有种子没有中文名**（llm_gen 提示词会退化成 code 原文）：%s", strings.Join(only, ","))
	}
	if only := diffKeys(names, seeds); len(only) > 0 {
		t.Errorf("有行业**只有中文名没有种子**（base_url 留空时静默回落通用词）：%s", strings.Join(only, ","))
	}
}

// TestIndustrySeedsAreUsableTerms R2：种子的**形状**必须是 tier1 那条腿能用的东西。
// tier1 是「一词一次 zh.wikipedia langlinks 查询」，所以：
//
//	· 纯汉字（拉丁缩写/带空格短语/数字在中文维基上多半查不到，查不到就是白烧一次往返）；
//	· ≤30 rune（与引擎侧批量术语层、术语检索的长度口径同尺，超了也进不了提示词）；
//	· 组内不重复（重复词＝同一语言白跑一次）；
//	· 每组 ≥10 条（tier1 单语言产出上限**恰好等于种子数**，少于 10 条就是样例不是产能）。
func TestIndustrySeedsAreUsableTerms(t *testing.T) {
	cjk := regexp.MustCompile(`^[一-鿿]+$`)
	for code, list := range builtinIndustrySeeds {
		if len(list) < 10 {
			t.Errorf("行业 %s 的种子只有 %d 条（<10）⇒ tier1 该语言最多产出 %d 条", code, len(list), len(list))
		}
		seen := map[string]bool{}
		for i, s := range list {
			if strings.TrimSpace(s) != s || s == "" {
				t.Errorf("行业 %s 第 %d 条种子有空白边或为空：%q", code, i+1, s)
			}
			if !cjk.MatchString(s) {
				t.Errorf("行业 %s 种子 %q 不是纯汉字（tier1 走中文维基，混拉丁/数字＝结构性查不到）", code, s)
			}
			if n := len([]rune(s)); n > 30 {
				t.Errorf("行业 %s 种子 %q 长 %d rune（>30，与引擎侧术语长度口径不同尺）", code, s, n)
			}
			if seen[s] {
				t.Errorf("行业 %s 种子重复：%q", code, s)
			}
			seen[s] = true
		}
	}
}

// TestNewIndustriesAreRegisteredBothSides R3：跨语言等值——前端行业表必须等于
// Go 侧全集再并上 `general`（通用行业是前端展示项、不参与采集，故不在 Go 两张表里）。
// 这一条是"包建了、词采了、却一条都不注入"那一族的唯一机械防线。
func TestNewIndustriesAreRegisteredBothSides(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("取仓根失败：%v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "frontend-react", "src", "lib", "industries.ts"))
	if err != nil {
		t.Fatalf("读不到前端行业表（跨侧锁已失明）：%v", err)
	}
	// 只取 INDUSTRY_META 那一段里的 code（别把注释/别的对象里的 code: 也算进来）
	body := string(raw)
	if i := strings.Index(body, "INDUSTRY_META"); i >= 0 {
		body = body[i:]
	}
	re := regexp.MustCompile(`code:\s*'([a-z0-9_]+)'`)
	fe := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		fe[m[1]] = true
	}
	if len(fe) < 10 {
		t.Fatalf("前端行业解析到 %d 个 code（<10）⇒ 解析漂了，本锁已失明", len(fe))
	}
	want := map[string]bool{"general": true}
	for _, k := range goKeys(builtinIndustrySeeds) {
		want[k] = true
	}
	var missing []string
	for k := range want {
		if !fe[k] {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("后端有、前端行业下拉里没有的 code：%s ⇒ 租户选不到这个行业，包与词表永不注入", strings.Join(missing, ","))
	}
	var extra []string
	for k := range fe {
		if !want[k] {
			extra = append(extra, k)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		t.Errorf("前端有、后端没有的行业 code：%s ⇒ 选了它既没有包也没有采集源（空选项）", strings.Join(extra, ","))
	}
	// 点名正锁：本批新增的两行业必须在**三处**都在（缺一处就是半成品）
	for _, code := range []string{"finance", "catering"} {
		if _, ok := builtinIndustrySeeds[code]; !ok {
			t.Errorf("新行业 %s 缺 tier1 种子", code)
		}
		if _, ok := industryNames[code]; !ok {
			t.Errorf("新行业 %s 缺 llm_gen 行业中文名", code)
		}
		if !fe[code] {
			t.Errorf("新行业 %s 缺前端 INDUSTRY_META（管理台/注册流程选不到）", code)
		}
	}
}
