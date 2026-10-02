// ============ reply_instr_echo_test.go · P2 根治腿的断言 ============
// D-LLM-20261001-001 §十一「彻底根治方案 P2」的落地锁：三条腿各钉①现网真形被治、
// ②正常文案不误伤、③判据真的在多腿同时成立才动手（反证）。
// 口径照 TestSanitizeStripsInstructionEchoLead：误剥的代价是客户要看的正文，
// 所以每条「剥」的断言旁边都必须站着一排「不许动」的对照。
// =============================================
package engine

import (
	"context"
	"strings"
	"testing"
)

// TestGuardReplyInstructionEchoStripsVerbatim ★ P2 本体（引用重合度过滤）：
// 模型把 tone_rules 第 9 条那句「我们跟通用翻译工具真正不一样」**逐字**念进正文时，
// 重合度腿必须把它整句删掉；同时把一句**改写形**的合法正文（只借了「真正不一样」五个字）留住。
func TestGuardReplyInstructionEchoStripsVerbatim(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()

	// ① 现网形态：正文里夹一句从 tone_rules 逐字抄下来的方法论散文（连续命中远超 14 字）。
	verbatim := &Reply{
		Source:  "llm",
		Content: "你好。我们跟通用翻译工具真正不一样的地方讲清楚就是原版式能保住。你手头有文件可以试试。",
	}
	got := e.guardReplyInstructionEcho(ctx, "zh", verbatim)
	if strings.Contains(got.Content, "我们跟通用翻译工具真正不一样的地方讲清楚") {
		t.Fatalf("逐字复述的指令句没被重合度腿删掉：%q", got.Content)
	}
	// 正文两头（招呼与收尾提问）不许跟着丢
	for _, want := range []string{"你好", "你手头有文件可以试试"} {
		if !strings.Contains(got.Content, want) {
			t.Fatalf("重合度腿把正常正文一起吃掉了 %q：%q", want, got.Content)
		}
	}

	// ② 改写形合法正文：只碰了语料里的短片段，够不到重合阈值 → 一个字都不许动。
	legit := &Reply{
		Source:  "llm",
		Content: "咱们跟通用工具真正不一样的是原版式保留和术语库锁定，你传进去什么样出来还什么样。",
	}
	if g := e.guardReplyInstructionEcho(ctx, "zh", cloneReply(legit)); g.Content != legit.Content {
		t.Fatalf("合法正文被重合度腿误伤：%q → %q", legit.Content, g.Content)
	}

	// ③ 反证：非 llm 出栈（话术／流程／兜底是人写文案）根本不进这条腿——
	//    给它一句和 tone_rules 完全同字的正文也必须原样返回，证明判据收在 Source=="llm" 这一档。
	canned := &Reply{Source: "rule", Content: verbatim.Content}
	if g := e.guardReplyInstructionEcho(ctx, "zh", canned); g.Content != canned.Content {
		t.Fatalf("非 llm 出栈被这条腿动了（越权）：%q → %q", canned.Content, g.Content)
	}

	// ④ 反证：判据要真在连续命中／包含度上办事。把整句换成一段与提示词毫无关系的正常答复，
	//    即便它很长也绝不能被判抄（否则这条腿就成了「逢长句就删」的空转闸门）。
	unrelated := &Reply{Source: "llm", Content: "你可以直接把 Word 文档传上来，翻完下载的还是 docx，加粗和表格都按原文位置还原，个别词的加粗可能对不齐。"}
	if g := e.guardReplyInstructionEcho(ctx, "zh", unrelated); g.Content != unrelated.Content {
		t.Fatalf("无关长正文被误删：%q → %q", unrelated.Content, g.Content)
	}
}

// TestGuardReplyInstructionEchoNeverEmpties ★ fail-soft：整条回复全与提示词高重合（多半是模型
// 把整段 tone_rules 吐了出来）时，宁可原样发出也不发一个空气泡——误发长旁白难看，发空泡是功能故障。
func TestGuardReplyInstructionEchoNeverEmpties(t *testing.T) {
	e := newTestEngine(t)
	// 用真实 tone_rules 原文当正文：每一句都会命中重合度腿。
	all := &Reply{Source: "llm", Content: strings.TrimSpace(defaultToneRules)}
	got := e.guardReplyInstructionEcho(context.Background(), "zh", all)
	if strings.TrimSpace(got.Content) == "" {
		t.Fatal("全段被判抄时把回复清空了（应原样 fail-soft 保留）")
	}
}

// TestSanitizeStripsTrailingNarration ★ P2 尾部旁白词面腿：钉现网 2026-10-02 抓到的那条
// **括号不成对**的尾部自述（开头「（」被吃在半空、只剩收尾「）」），并压一排误伤对照。
//
// 为什么重合度腿不够、还要这条词面腿：现网那句是**改写**出来的（换了词、缩了句），
// 够不到「连续 ≥14 字命中提示词」的阈值；而它又不带成对括号，dropParentheticals 也管不着。
// 唯一能收它的是「尾部同段出现 ≥2 个内核自述词」这条组合特征。
func TestSanitizeStripsTrailingNarration(t *testing.T) {
	// 现网原形：正文完整收尾，空一行后追加一段交代作答策略的旁白
	prod := "单个文件最大支持 40MB，超了可以分片或用批量翻译。需要我教你怎么操作吗？\n\n，需简短承认后引回业务，这里用「建议分片上传或用批量翻译」自然衔接，同时提问引导上传文件，符合规则要求。）"
	got := sanitizeVisitorText(prod)
	for _, leaked := range []string{"简短承认", "引回业务", "自然衔接", "提问引导", "符合规则要求"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("尾部旁白没剥净（残留 %q）：%q", leaked, got)
		}
	}
	if !strings.Contains(got, "需要我教你怎么操作吗？") {
		t.Fatalf("剥旁白把正文一起吃掉了：%q", got)
	}

	// 误伤对照：这些合法正文一个字都不许动（内核词不足 2 / 或根本不在尾部）。
	for _, keep := range []string{
		"上下文自然衔接，术语也能对齐。",    // 单个内核词（自然衔接），客户真会这么说
		"文件名需符合规则。",          // 「符合规则」不属内核词表，且只有一句
		"这里用批量翻译更快，你先传文件就行。", // 弱词「这里用」不算内核词
		"第一步：先上传。第二条：等翻完下载。", // 分条说明，无合规措辞共现
	} {
		if g := sanitizeVisitorText(keep); g != strings.TrimSpace(keep) {
			t.Errorf("合法正文被误伤：%q → %q", keep, g)
		}
	}

	// 反证：内核词只有 1 个的尾段绝不剥（判据是「≥2 共现」，不是「见词即剥」）。
	one := "好的，这就开始处理。顺便一提引回业务。"
	if g := sanitizeVisitorText(one); !strings.Contains(g, "引回业务") {
		t.Fatalf("单个内核词却把尾段删了（判据应要求 ≥2 共现）：%q → %q", one, g)
	}
}

// TestSanitizeStripsRuleSelfReference ★ P2 规则自指词面腿：「第 N 条 × 合规措辞」同句共现即判旁白。
// 与尾部腿互补——这类自述可能出现在正文中段，且不带括号。
func TestSanitizeStripsRuleSelfReference(t *testing.T) {
	prod := "这是合法的一句答复。我在第 3 条里符合规则要求，所以照此输出标记。下面是收尾。"
	got := sanitizeVisitorText(prod)
	if strings.Contains(got, "符合规则要求") || strings.Contains(got, "输出标记") || strings.Contains(got, "第 3 条") {
		t.Fatalf("规则自指句没剥净：%q", got)
	}
	for _, want := range []string{"这是合法的一句答复", "下面是收尾"} {
		if !strings.Contains(got, want) {
			t.Fatalf("剥自指把正文吃了 %q：%q", want, got)
		}
	}

	// 误伤对照：只有「第 N 条」而没有合规措辞共现＝合法分条说明，一律不剥。
	for _, keep := range []string{
		"分三步：第 1 条上传文件，第 2 条选目标语种，第 3 条下载译文。",
		"第一条说的是术语库，按套餐规则计费。", // 「按套餐规则」不等值于任何合规措辞词条
	} {
		if g := sanitizeVisitorText(keep); g != strings.TrimSpace(keep) {
			t.Errorf("合法分条被误伤：%q → %q", keep, g)
		}
	}
}

// TestTrailingNarrationCandidatesObservesOnly ★ P3 观测腿第四条：词表外的裸句旁白**只进日志候选、不改正文**。
// 反证方向与删除腿相反：这里断言「报了但留着」——谁把它误接成清洗腿，这一条当场红。
func TestTrailingNarrationCandidatesObservesOnly(t *testing.T) {
	// 「引导上传」在内核/强标记里，但只此一个、不成对括号 → 删除腿不该动它，观测腿该报它。
	s := "价格按源字符算。后面记得引导上传样本文件即可。"
	if g := sanitizeVisitorText(s); !strings.Contains(g, "引导上传") {
		t.Fatalf("单个强标记被删除腿误剥：%q → %q", s, g)
	}
	cands := trailingNarrationCandidates(s)
	if len(cands) == 0 {
		t.Fatalf("词表外的裸句旁白没进观测候选，下一批就没有词条证据：%q", s)
	}
}

// cloneReply 测试侧浅拷贝（重合度腿会就地改 rep.Content，对照用例要各拿各的原稿）。
func cloneReply(r *Reply) *Reply {
	cp := *r
	return &cp
}
