// ============ engine/recall.go · 职责说明 ============
// 分级召回第 2 级：无外部依赖的中文友好相似度（字符 bigram 包含/Dice + 字符集包含），
// 配合文本归一（全半角 / 繁简 / 大小写 / 标点 / 语气词停用词）。
//
// 背景（2026-09-22 任务 #58，报告 §4.3 与 §6 P3#21）：原检索为纯关键词子串计数
// + 同义词表归一（engine.go hitScore/synonymHit），实测 50 条真实口吻销售问句中
// 15 条三层全脱靶（如「韩国语可以翻吗」vs 关键词「韩语」、「一个文件最多能传多大」
// vs 关键词「大文件」、「词对词翻译会不会丢专业名词」vs 关键词「专有名词」）。
// 本文件补一层「换一种说法仍字面接近」的保守召回，不改变编排语义
// （话术直配 → 知识命中 → LLM 融合 → 零命中兜底），只升级知识库打分。
//
// 保守原则（WHY）：宁可漏召回给兜底引导，也不能错召回给出牛头不对马嘴的答案——
// 兜底有 unanswered_questions 登记闭环兜住，错答没有。因此：
//   - 第 2 级只作用于知识库检索（RetrieveKB），话术直配 / 流程触发维持第 1 级
//     精确+同义词口径（A2/CI2/A3 等 UAT 快答断言依赖直配零误伤）；
//   - 命中分数刻意压到低于任何一次精确命中（见 entry 打分注释），不参与
//     compoundIntent 复合让位判定（该判定维持「真实命中」语义）；
//   - 极短输入（归一后 <4 字）不做相似度：信息量不足，误伤率高于收益。
//
// =============================================
package engine

import (
	"context"
	"strings"
	"unicode"
)

// ---------- 第 2 级相似度阈值（保守标定，golden_test.go 棘轮守着） ----------
const (
	fuzzyMinInputRune = 4    // 归一后输入最短参与相似度长度（短问句交给第 1 级精确/同义词）
	fuzzyMinKwRune    = 2    // 候选词/标题最短长度（单字太易误伤，直接不参与）
	fuzzyContainMin   = 0.75 // bigram 包含率（候选词 bigram 有多少出现在输入里）
	fuzzySharedMin    = 2    // bigram 共现最少个数（防「两个词碰巧共享一个 bigram」）
	fuzzyDiceMin      = 0.55 // 全串 bigram Dice（长句对长句的镜像相似）
	fuzzyDiceMinLen   = 4    // 走 Dice 通道要求的候选长度
	fuzzyCharMaxIn    = 12   // 字符集全包含通道的输入长度上限（越长越易「碰巧含全部字」）
)

// ---------- 文本归一 ----------

// zhStopwords 语气词/寒暄/填充词（归一时整体剔除）。
// WHY：「请问一下」「麻烦问下」这类前缀会把 bigram 相似度冲淡，
// 剔除后「请问支持哪些文件格式呀」与关键词「文件格式」的包含率才真实。
// 只收录几乎不可能独立成为关键词的填充语，宁缺勿滥（「什么」「多少」不剔，
// 它们本身是关键词成分）。
var zhStopwords = []string{
	"请问一下", "请问", "麻烦", "帮我", "帮忙", "问一下", "想问下", "想问", "问下", "问一下",
	"你们", "咱们", "有没有", "是不是", "能不能", "可不可以", "怎么样", "如何",
	"一下", "这个", "那个", "就是", "还有", "以及", "到底", "顺便", "如果",
}

// zhStopChars 单字语气助词（句尾/句中填充，剔除不影响关键词成分）
const zhStopChars = "吗呢啊呀吧嘛哦噢嗯咯哈喽噢欸咯啦呗"

// trad2simp 常见繁体字 → 简体映射（精选表，非全量）。
// WHY：不做完整 OpenCC 是硬约束「不引入新依赖」；此表覆盖销售问句高频字，
// 目标是「怎麼收費」「支持哪些語種」这类繁体输入不因字形不同而零命中。
// 表外繁体字保持原样参与比较，最多退化为未命中兜底，不会给出错答案。
var trad2simp = map[rune]rune{
	'麼': '么', '費': '费', '語': '语', '種': '种', '譯': '译', '發': '发', '錢': '钱',
	'價': '价', '關': '关', '鍵': '键', '詞': '词', '賬': '账', '帳': '账', '號': '号',
	'碼': '码', '註': '注', '冊': '册', '儲': '储', '數': '数', '據': '据', '團': '团',
	'隊': '队', '權': '权', '開': '开', '餘': '余', '額': '额', '積': '积', '試': '试',
	'們': '们', '這': '这', '個': '个', '說': '说', '時': '时', '間': '间', '對': '对',
	'後': '后', '來': '来', '會': '会', '給': '给', '將': '将', '從': '从', '於': '于',
	'與': '与', '並': '并', '場': '场', '務': '务', '員': '员', '經': '经', '辦': '办',
	'統': '统', '計': '计', '劃': '划', '設': '设', '備': '备', '續': '续', '繼': '继',
	'斷': '断', '點': '点', '樣': '样', '類': '类', '問': '问', '題': '题', '難': '难',
	'錄': '录', '聲': '声', '圖': '图', '網': '网', '頁': '页', '鏈': '链', '傳': '传',
	'電': '电', '腦': '脑', '機': '机', '軟': '软', '體': '体', '優': '优', '勢': '势',
	'覽': '览', '應': '应', '該': '该', '還': '还', '裏': '里', '裡': '里', '兩': '两',
	'檔': '档', '讀': '读', '寫': '写', '線': '线', '東': '东', '長': '长', '門': '门',
	'雲': '云', '帶': '带', '産': '产', '異': '异', '尋': '寻', '齊': '齐', '億': '亿',
	'償': '偿', '係': '系', '準': '准', '熱': '热', '習': '习', '慣': '惯', '屬': '属',
	'導': '导', '歲': '岁', '師': '师', '庫': '库', '當': '当', '徵': '征', '憶': '忆',
	'慮': '虑', '車': '车', '陽': '阳', '訴': '诉', '請': '请', '連': '连',
	'鎖': '锁', '記': '记', '資': '资', '質': '质', '驗': '验', '證': '证',
	'購': '购', '賣': '卖', '轉': '转', '輪': '轮', '遲': '迟', '選': '选',
}

// normalizeText 文本归一：小写 → 全半角 → 繁简 → 去标点/空白 → 去停用词/语气词。
// 参数：任意用户输入或词条文本。返回：只保留字母数字与汉字的归一串（rune 友好）。
func normalizeText(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		// 全角 ASCII（！到～）→ 半角；全角空格 → 普通空格
		case r >= 0xFF01 && r <= 0xFF5E:
			r -= 0xFEE0
		case r == 0x3000:
			r = ' '
		}
		if m, ok := trad2simp[r]; ok {
			r = m
		}
		// 保留字母与数字（含汉字，unicode.IsLetter 对 CJK 为真），标点/符号/空白一律剔除
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	out := b.String()
	// 去停用词（多字填充语）与单字语气词
	for _, sw := range zhStopwords {
		out = strings.ReplaceAll(out, sw, "")
	}
	var kept []rune
	for _, r := range out {
		if !strings.ContainsRune(zhStopChars, r) {
			kept = append(kept, r)
		}
	}
	return string(kept)
}

// fuzzyWeakChars 泛指/疑问功能字：这些字几乎出现在任何问句里，不能单独支撑
// 「字符集全包含」通道（实测「怎么用」三字符被「公司要用多人协同怎么弄」凑齐
// 即误召 first-file-translate）。字符集通道要求候选词去掉本表字符后仍剩 ≥2 个
// 「实义字」全被输入包含，才放行。
const fuzzyWeakChars = "怎么什这那就还和跟或以为能可要会想请问答样很太多小大新老做搞弄进行"

// strongChars 候选词中的实义字符数（去 fuzzyWeakChars 后的不同字符）
func strongChars(s string) int {
	set := distinctChars(s)
	n := 0
	for r := range set {
		if !strings.ContainsRune(fuzzyWeakChars, r) {
			n++
		}
	}
	return n
}

// ---------- bigram 工具 ----------

// runeBigrams 相邻二字组多重集（转集合用 map）。长度 <2 时为空。
func runeBigrams(s string) map[string]bool {
	rs := []rune(s)
	set := make(map[string]bool, len(rs))
	for i := 0; i+1 < len(rs); i++ {
		set[string(rs[i:i+2])] = true
	}
	return set
}

// distinctChars 去重字符集
func distinctChars(s string) map[rune]bool {
	set := map[rune]bool{}
	for _, r := range s {
		set[r] = true
	}
	return set
}

// ---------- 相似度主函数 ----------

// fuzzySimilarity 计算「输入 input 是否字面接近候选词 cand」（0 表示不接近）。
// 三条通道，全部要求候选 ≥2 字、输入 ≥4 字，任一通道达标即返回其分数（≤1）：
//  1. bigram 包含率：候选词的二字组几乎完整出现在输入里（含乱序容错），
//     如「文件格式」→「支持哪些的文件格式」（包含率 1.0）；
//  2. 全串 Dice：长候选（≥4 字）与输入的对称相似度，如「专有名词」→「专业名词」
//     （一字之差，Dice 0.43 不够、走通道 3）；
//  3. 字符集全包含：候选词的每个不同字符都出现在输入中且输入不太长，
//     救援「词序打断/插字」形态——「韩语」→「韩国语」、「大文件」→「传多大文件」。
//     对 2 字候选额外限制输入 ≤12 字，长句碰巧含两字的误伤概率不可忽略。
//
// WHY 保守：本函数只在第 1 级零命中后被调用，返回值只决定「兜底 or 弱命中」，
// 其分数永远低于任何一次精确命中（见 entry.score 口径），不参与话术/流程判定。
func fuzzySimilarity(input, cand string) float64 {
	nIn, nCand := normalizeText(input), normalizeText(cand)
	inR, candR := []rune(nIn), []rune(nCand)
	if len(inR) < fuzzyMinInputRune || len(candR) < fuzzyMinKwRune {
		return 0
	}
	bgIn, bgCand := runeBigrams(nIn), runeBigrams(nCand)
	shared := 0
	for bg := range bgCand {
		if bgIn[bg] {
			shared++
		}
	}
	// 通道 1：候选 bigram 几乎全部被输入包含
	if len(bgCand) >= 2 && shared >= fuzzySharedMin {
		contain := float64(shared) / float64(len(bgCand))
		if contain >= fuzzyContainMin {
			return contain
		}
	}
	// 通道 2：对称 Dice（长候选才有意义，短候选走 Dice 极易虚高）
	if len(bgCand) >= fuzzyDiceMinLen-1 && len(bgIn) > 0 {
		dice := 2 * float64(shared) / float64(len(bgIn)+len(bgCand))
		if dice >= fuzzyDiceMin {
			return dice
		}
	}
	// 通道 3：字符集全包含（短候选词被输入拆散场景）
	// 双闸门：① 候选须含 ≥2 个实义字（防「怎么用」这类泛指词被任何问句凑齐）；
	// ② 2 字候选要求输入 ≤12 字（长句碰巧含两字的概率不可忽略）。
	if strongChars(nCand) < 2 {
		return 0
	}
	charsCand := distinctChars(nCand)
	for ch := range charsCand {
		if !strings.ContainsRune(nIn, ch) {
			return 0
		}
	}
	if len(charsCand) >= 3 || len(inR) <= fuzzyCharMaxIn {
		return 0.6 // 固定弱信号分：过闸即可，排序另有权重通道兜着
	}
	return 0
}

// RecallHit 分级召回观测结果（导出口径：golden 回归测试与管理台诊断共用）。
type RecallHit struct {
	Key   string  `json:"key"`   // 知识库条目 key
	Title string  `json:"title"` // 条目标题
	Via   string  `json:"via"`   // 命中通道：exact / fuzzy / vector
	Score int     `json:"score"` // 分级打分（排序依据，三级分层见 engine.go 注释）
	Sim   float64 `json:"sim"`   // 原始相似度（exact 通道为关键词命中数）
}

// RecallReport 对 input 做一次知识检索并返回命中通道与分数（观测/回归专用）。
// 与对话链路共用同一实现（retrieveKB），保证「看到的分数」=「线上用的分数」。
// ★ 分级召回改造的「输出命中分数供日志观测」交付物之一。
func (e *Engine) RecallReport(ctx context.Context, input string, topN int) []RecallHit {
	if ctx == nil {
		ctx = context.Background()
	}
	if topN <= 0 {
		topN = 5
	}
	hits := e.retrieveKB(ctx, input, topN)
	out := make([]RecallHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, RecallHit{Key: h.key, Title: h.title, Via: h.via, Score: h.score, Sim: h.sim})
	}
	return out
}

// fuzzyEntryScore 一个知识库条目的第 2 级相似分：对全部关键词 + 标题取最大值。
// 返回 (best, ok)：best 为 [0.45,1.0] 区间的原始相似度，供日志观测与同分排序。
// WHY 带标题：运营建条时标题往往比关键词更接近用户原话（「支持哪些文件格式」），
// 标题通道与关键词通道同闸同权重，仅作为候选词来源扩展。
func fuzzyEntryScore(input, keywords, title string) (float64, bool) {
	best := 0.0
	for _, k := range strings.Split(keywords, ",") {
		if k = strings.TrimSpace(k); k == "" {
			continue
		}
		if s := fuzzySimilarity(input, k); s > best {
			best = s
		}
	}
	if s := fuzzySimilarity(input, title); s > best {
		best = s
	}
	return best, best > 0
}
