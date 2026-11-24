// clean.go · 探针的样本集与三臂提示词（old / clean / nothink）。
//
// 为什么要三臂而不是两臂：
//
//	① old→clean 的变化只能回答"是不是提示词里的希伯来举例在干扰"；
//	② 推理系候选模型（与 AGENTS §21 的 ocr_fix 同一个坑）会占掉生成预算，
//	   而这条打分腿 max_tokens 只有 64 —— 关掉思维链（thinking:disabled）是第二个变量，
//	   必须单独测，否则"clean 还是乱"会被错读成"模型不行"，而真凶可能是"没关推理"。
//
// 样本分三组：
//   - prod：清洗 job 抽查里真实出现的 6 条；5 条应通过，房东 1 条只观察不设硬期望。
//   - ctrl：不做行业语境判断也能判的对照样本（汽车/银行 应过；刹车系统/安全气囊 应拒）。
//   - real：历史日志里 15 条真实丢弃样本，含应通过与应拒，用来同时观察误杀与误放。
package main

import (
	"strings"

	"translator/internal/config"
	"translator/internal/evals"
	"translator/internal/llm"
	"translator/internal/qualityjudge"
)

// probeCase 一条打分样本。
//
// ★ lang 不能省：现网提示词里的语言名取自 config.LangNames[targetLang]（qualityjudge.JudgeUserPrompt），
// 探针第一版把它硬写成"希伯来语"，而 real 组样本是马来语/瑞典语/波兰语 ——
// 模型看到"希伯来语译文：Aplikasi Mudah Alih"这种自相矛盾，分数就没法归因到模型能力上。
// 这类"探针自己把话说错"的缺陷与产品缺陷同形（都是 0/0），必须钉住。
type probeCase struct {
	grp  string // 样本组：prod/ctrl=手工样本；real=现网真实被丢弃的条目
	lang string // 目标语代码（config.LangNames 的键）；空=he
	src  string // 中文源词
	tgt  string // 目标语译文
	back string // HY 回译（按现网读数抄录，探针不重复调 HY）
	want int    // 1=应通过(term/semantic 均>=70) -1=应拒绝 0=观察样本（无硬期望）
	note string
}

// langName 目标语中文名，与 qualityjudge.Evaluate 同一取法（config.LangNames，缺则回代码本身）。
func (c probeCase) langName() string {
	code := c.lang
	if code == "" {
		code = "he"
	}
	if n := config.LangNames[code]; n != "" {
		return n
	}
	return code
}

// cs 构造一条样本（位置参数太容易串，尤其 lang 与 grp 都是短串）。
func cs(lang, grp, src, tgt, back string, want int, note string) probeCase {
	return probeCase{grp: grp, lang: lang, src: src, tgt: tgt, back: back, want: want, note: note}
}

// probeCases 返回固定样本集（顺序即输出顺序）。
func probeCases() []probeCase {
	return []probeCase{
		// ---- 生产抽查样本（希伯来语）：六条均为"原译文基本正确"，期望全部通过 ----
		cs("he", "prod", "长视频", "סרטונים ארוכים", "长视频", 1, "回译逐字相同却被打 0/0"),
		cs("he", "prod", "二手房", "דירה משומשת", "这套公寓正在被使用中", 1, "回译取错义项（实际=使用过的公寓）"),
		cs("he", "prod", "房地产", "נכסים ותכנון ערים", "房地产与城市规划", 1, "多了一个限定词，核心含义对"),
		cs("he", "prod", "流量统计", "מדידה של תנועה גולמית", "测量整体运动", 1, "回译取错义项（实际=原始流量测量）"),
		cs("he", "prod", "房东", "מוכר נכסים", "房产卖家", 0, "★待复核：מוכר נכסים=房产卖家，而房东=出租方，本条译文本身可疑，故不设硬期望"),
		cs("he", "prod", "卖家", "מוכר", "已知/著名", 1, "מוכר 多义：卖家/已知；回译取了另一义"),
		// ---- 对照样本（希伯来语）：不依赖行业语境也应判对 ----
		cs("he", "ctrl", "汽车", "מכונית", "汽车", 1, "对照：逐字对应"),
		cs("he", "ctrl", "银行", "בנק", "银行", 1, "对照：逐字对应"),
		cs("he", "ctrl", "刹车系统", "מערכת ברק", "闪电系统", -1, "对照：明显错译"),
		cs("he", "ctrl", "安全气囊", "רובה הצלה", "救生枪", -1, "对照：明显错译"),
		// ---- 现网样本：2026-11-23~24 translator.log 里"语义闸判错且重译修正失败，丢弃"的真实条目，
		//      回译一字不改抄自日志（原文见 /opt/translator/log/translator.log）。
		//      want=1 的是"译文本身没错、被判死"的；want=-1 的是"译文确实是错的"（判死是对的）。
		cs("ms", "real", "移动应用", "Aplikasi Mudah Alih", "移动应用程序", 1, "马来语正译，与源词几乎逐字相同，却被判 30/30"),
		cs("ms", "real", "合作伙伴关系", "Perkongsian Kerjasama", "合作分享", 1, "马来语正译，被判 70/65（差 5 分）"),
		cs("ms", "real", "博士生", "ijazah doktor", "博士学位", 1, "马来语正译（博士=PhD，学位=degree），判 70/60"),
		cs("sv", "real", "物联网", "internet av saker", "“互联网上的物品”", 1, "瑞典语直译（IoT 的另一种写法），判 50/50"),
		cs("sv", "real", "客户关系管理", "kundrelationsskapande", "与客户建立关系（的）", 1, "瑞典语同义表达，判 70/65"),
		cs("sv", "real", "数字化转型", "digital omvandling", "数字转换", 1, "瑞典语同义表达，判 60/60"),
		cs("sv", "real", "云计算", "cloudteknik", "云技术（Cloud Technology）", 1, "上位词（云技术），判 60/65"),
		cs("pl", "real", "素质教育", "edukacja jakościowa", "高质量的教育", 1, "波兰语正译（quality education），判 70/60"),
		cs("sv", "real", "轮胎", "däck", "（汽车等上的）车顶；覆盖物", 1, "瑞典语正译（däck=轮胎），回译取了同形词的车顶义"),
		cs("sv", "real", "供应链管理", "livsledningshantering", "生命历程管理", -1, "瑞典语成了'生命历程管理'，译文确实错"),
		cs("sv", "real", "供应链管理", "Lagertillverkningshantering", "库存生产管理", -1, "成了'库存生产管理'，术语不对"),
		cs("sv", "real", "客户关系管理", "kundrelationsskydd", "客户关系保护", -1, "成了'客户关系保护'，含义跑偏"),
		cs("pl", "real", "数据备份", "Rezerwacja danych", "数据保留", -1, "成了'数据保留/预留'，错"),
		cs("pl", "real", "人工智能", "Łączna inteligencja", "综合智能", -1, "成了'综合智能'（正译应为 sztuczna inteligencja）"),
		cs("sv", "real", "在线支付", "onlineskötering", "在线驾驶管理", -1, "成了'在线驾驶管理'，错"),
	}
}

// arm 一臂提示词与调用参数。
type arm struct {
	name      string
	model     string // 空=用打分档默认模型（-candidates 时为候选模型名）
	maxTokens int
	extra     map[string]interface{} // 厂商私有字段（thinking 等）
	sys       string
	user      func(probeCase) string
}

// cleanArm 直接复用产品提示词，供候选模型臂只换模型、不换口径。
func cleanArm(name string, maxTokens int) arm {
	return arm{
		name: name, maxTokens: maxTokens, sys: qualityjudge.JudgeSystemPrompt(),
		user: func(c probeCase) string {
			return qualityjudge.JudgeUserPrompt(c.src, c.langName(), c.tgt, c.back)
		},
	}
}

// dim5Arms 复用 internal/evals 的真实提示词，避免探针与五维评估链各抄一份。
func dim5Arms(models []string, maxTokens int) []arm {
	out := make([]arm, 0, len(models))
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		out = append(out, arm{
			name: "d5:" + m, model: m, maxTokens: maxTokens,
			user: func(c probeCase) string { return evals.EvaluatePrompt(c.src, c.langName(), c.tgt) },
		})
	}
	return out
}

// probeArms 返回三臂定义。
func probeArms() []arm {
	oldSys := "你是翻译质量评估员。请评估译文是否准确表达了原文含义（0-100分）。只输出 JSON: {\"term\":0,\"semantic\":0}"
	oldUser := func(c probeCase) string {
		return "原文术语(中文): " + c.src + "\n" +
			"目标语译文(" + c.langName() + "): " + c.tgt + "\n" +
			"该译文被独立回译成中文是: " + c.back + "\n\n" +
			"请评估这个译文是否准确表达了原文术语的含义。注意行业语境：营销词在通用词典里可能有别的含义（如 ליד 在营销语境是\"线索\"而非\"旁边\"），品牌名音译可能对应普通词（如 צ'רי 是\"奇瑞\"而非\"樱桃\"），不要只按通用字面判断。只输出 JSON。"
	}

	return []arm{
		{name: "old", maxTokens: 64, sys: oldSys, user: oldUser},
		cleanArm("clean", 64),
		func() arm {
			a := cleanArm("nothink", 64)
			a.extra = llm.NoThinkingExtra()
			return a
		}(),
	}
}

// candidateArms 候选打分模型臂：同一份干净提示词，只换模型名；默认沿用生产 64-token 参数。
func candidateArms(models []string, maxTokens int) []arm {
	out := make([]arm, 0, len(models))
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		a := cleanArm("m:"+m, maxTokens)
		a.model = m
		out = append(out, a)
	}
	return out
}
