// ============ seed_gate_test.go · 职责说明 ============
// seed.json 的「承诺事实源」闸门（★ 082x，2026-09-29）。
//
// 为什么非要一条静态锁：
//
//	用户指令是「涉及价格、套餐、能力的东西严格按系统能力与承诺来，不造额外承诺」。
//	promise.go 的【承诺边界】与 system_values.go 的【系统现值】管的是**模型开口那一刻**，
//	但知识条目本身还有一层独立的漏口：撞关键词的中文话术（scripts）和流程（flows）
//	**根本不经过模型**，直接把库里那段字原样发给访客（见 engine.Respond 的第 1、3 道）。
//	所以知识文案里写着「积分永久有效」「上下文审校」这类话时，语言闸再严也拦不住——
//	那句话压根没进 prompt。这条锁守的就是这个源头。
//
// 三类口径各自的事实源（2026-09-29 逐个核过代码，不是凭印象）：
//   - 积分有效期：quota_grants 的 trial/plan 两档都带 expires_at 且扣减按到期日升序，
//     永久的是 balance_accounts.balance（充值/买断）⇒「积分永久有效」属超承诺；
//   - 对话翻译：ChatRequest 只有 {message, skill, options}，engine.HandleText 没有历史入参
//     ⇒「上下文审校／跨条消息连贯」不存在；options 只有 mode/lang/max_length/target_langs
//     ⇒ 没有「风格指令」这个开关；
//   - 语种数：TranslateLangs 34、HunyuanMTLangSet 38，且会随调档变 ⇒ 不许把数字抄进文案，
//     现值由 system_values.go 每次进 prompt。
//
// ★ 判据写法（AGENTS §一·5 那条「负向锁必须配正向对照」）：
// 命中禁词不等于违规——「不跨消息带上文」「没有单独的风格开关」「别用『40+』这类说法」
// 这些**否定式**才是合规形态，所以每个命中点都要在附近看到否定标记才算过；
// 同时下面 TestSeedPromiseGateSelfProof 会喂一条假文案进同一个判定函数，证明它真能红。
// =============================================
package seed

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// maxRunes 一条知识/话术里需要扫的正文长度上限（防御用：正常文案都在几百字内，
// 真出现超长串说明数据变形，宁可报错也不要静默扫不完留下盲区）。
const maxRunes = 20000

// bannedPhrase 禁词 + 它的"合法出现形态"标记。
//
// allowMarkers 为空＝这个串在文案里**任何位置**都不许出现；
// 非空＝命中点前后 windowRune 个字符内必须出现其中一个标记，否则算违规
//
// （即"这是在被明令禁止，不是在承诺"）。
type bannedPhrase struct {
	phrase      string
	allowMarker []string
	why         string
}

var seedBanned = []bannedPhrase{
	{"积分永久有效", nil, "订阅额度与体验额度都有到期日，只有充值/买断不过期"},
	{"永久有效不过期", nil, "同上"},
	{"充值永久有效", nil, "同上"},
	{"上下文审校", []string{"不跨消息", "没有", "别", "不许"}, "对话翻译每条消息单独翻，没有跨消息上下文能力"},
	{"风格指令", []string{"没有", "别", "不许"}, "没有风格开关，要求要随内容一起发"},
	{"支持上下文", nil, "同上"},
	{"支持 40+ 语种", nil, "语种数会随调档变，数字只走【系统现值】"},
	{"40+ 语种", []string{"别用", "不许"}, "同上"},
	{"上百种", []string{"别用", "不许"}, "同上"},
	{"任意其他语言", nil, "覆盖范围以语言选择器现值为准"},
	// 公式类只禁"能力承诺"那几种说法（现网截图里模型编的就是「Excel 翻译后公式坐标自动重算」），
	// 裸「公式」两字不禁：/compare 页的「公开算价公式」是真有的东西，一刀切会把正确文案判红。
	{"公式自动", nil, "没有公式重算/坐标保持能力，Excel 交付的是单元格文本"},
	{"公式坐标", nil, "同上"},
	{"公式重算", []string{"不"}, "同上（否定式提法可以，用来澄清不做这件事）"},
}

// windowRune 命中点前后各看这么多个字符找否定标记。
// 取 24 是实测值：现网合规写法「也别用『40+』『上百种』这类说法」的否定词离命中点 2~6 个字，
// 而"这句在承诺"的形态里附近不会出现否定词；再放大就会把不相邻的否定词算进来（假放行）。
const windowRune = 24

// seedTexts 把 seed.json 里所有会直接发给访客的文本摊平成 (定位, 文本)。
// 覆盖 kb.content/title/keywords、features.description、scripts.content、flows 的 steps_json 与 description
// ——话术与流程是**不经模型**直出的，漏扫一处就等于这条锁有个没灯的角落。
func seedTexts(t *testing.T) map[string]string {
	t.Helper()
	var doc struct {
		KB []struct {
			Key      string `json:"key"`
			Title    string `json:"title"`
			Content  string `json:"content"`
			Keywords string `json:"keywords"`
		} `json:"kb"`
		Features []struct {
			Key         string `json:"key"`
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"features"`
		Scripts []struct {
			Key     string `json:"key"`
			Content string `json:"content"`
		} `json:"scripts"`
		Flows []struct {
			Key          string `json:"key"`
			Description  string `json:"description"`
			StepsJSON    string `json:"steps_json"`
			TriggerWords string `json:"trigger_keywords"`
		} `json:"flows"`
	}
	if err := json.Unmarshal(SeedJSON, &doc); err != nil {
		t.Fatalf("seed.json 解析失败：%v", err)
	}
	out := map[string]string{}
	for _, e := range doc.KB {
		out["kb/"+e.Key+"/content"] = e.Content
		out["kb/"+e.Key+"/title"] = e.Title
	}
	for _, e := range doc.Features {
		out["features/"+e.Key+"/description"] = e.Description
	}
	for _, e := range doc.Scripts {
		out["scripts/"+e.Key+"/content"] = e.Content
	}
	for _, e := range doc.Flows {
		out["flows/"+e.Key+"/description"] = e.Description
		out["flows/"+e.Key+"/steps_json"] = e.StepsJSON
	}
	if len(out) == 0 {
		t.Fatal("seed 文本摊平后为 0 条（键名或结构变了会让这条锁恒绿）")
	}
	return out
}

// scanBanned 对一段文本跑禁词判定，返回违规说明（合规则 nil）。
// 单独抽成函数是为了让"反证"用例能喂假文案进**同一个**判定，而不是另写一份简化逻辑。
func scanBanned(where, text string) []string {
	var bad []string
	if utf8.RuneCountInString(text) > maxRunes {
		return []string{where + ": 文本超过 maxRunes 字符，本闸不扫超长变形数据"}
	}
	for _, bp := range seedBanned {
		from := 0
		for {
			i := strings.Index(text[from:], bp.phrase)
			if i < 0 {
				break
			}
			abs := from + i // 字节下标（strings.Index 的口径）
			if len(bp.allowMarker) == 0 {
				bad = append(bad, where+": 出现「"+bp.phrase+"」（"+bp.why+"）")
			} else if !nearNegation(text, abs, bp.phrase, bp.allowMarker) {
				bad = append(bad, where+": 出现「"+bp.phrase+"」且附近没有否定标记（"+bp.why+"）")
			}
			from = abs + len(bp.phrase)
		}
	}
	return bad
}

// nearNegation 命中点前后各 windowRune 个**字符**内是否能找到否定标记。
//
// ★ 窗口边界必须按 rune 走再换算回字节：中文一个字符占 3 字节，
//
//	直接拿字节下标加减会把窗口缩成 1/3 宽，否定词落在字符窗口内却排在字节窗口外，
//	于是合规的否定式写法被误判成违规（这类"锁自己切错位置"的形态最容易长期误红）。
func nearNegation(text string, hitByte int, phrase string, markers []string) bool {
	rs := []rune(text)
	hitRune := utf8.RuneCountInString(text[:hitByte]) // 字节下标 → rune 下标（hitByte 来自 strings.Index，必在字符边界上）
	endRune := hitRune + utf8.RuneCountInString(phrase)
	lo, hi := hitRune-windowRune, endRune+windowRune
	if lo < 0 {
		lo = 0
	}
	if hi > len(rs) {
		hi = len(rs)
	}
	win := string(rs[lo:hi])
	for _, m := range markers {
		if strings.Contains(win, m) {
			return true
		}
	}
	return false
}

// TestSeedContentMakesNoExtraPromises 主判据：seed 里每一条直出文案都不得含未被否定的禁词。
func TestSeedContentMakesNoExtraPromises(t *testing.T) {
	texts := seedTexts(t)
	scanned := 0
	var all []string
	for w, s := range texts {
		if strings.TrimSpace(s) == "" {
			continue
		}
		scanned++
		all = append(all, scanBanned(w, s)...)
	}
	// 覆盖面腿（等值算式，防止"扫到几条算几条"）：
	//   kb 30 条 ×(content+title)=60 ＋ features 11 ×description ＋ scripts 5 ×content
	//   ＋ flows 3 ×(description+steps_json)=6 ⇒ 82 个文本位（空串的上面已跳过，seed 里这些字段都有值）。
	// 低于 70 说明某一段的摊平口径变了（改键名/换结构），那时这条锁就是在"扫空气"，必须红灯。
	if scanned < 70 {
		t.Fatalf("只扫到 %d 条文本（应 ≥70）——seed 结构或本锁的摊平口径变了，锁正在失明", scanned)
	}
	if len(all) > 0 {
		t.Fatalf("seed 里有 %d 处超承诺表述：\n  %s", len(all), strings.Join(all, "\n  "))
	}
	t.Logf("✅ 已扫 %d 条直出文案，零超承诺表述", scanned)
}

// TestSeedPromiseGateSelfProof 反证：同一判定必须真的抓得住坏文案（否则上面的绿灯是摆设）。
func TestSeedPromiseGateSelfProof(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		isBad bool
	}{
		{"裸承诺永久有效", "积分永久有效，随时充值随时用", true},
		{"裸承诺上下文审校", "对话翻译支持上下文审校", true},
		{"带否定标记的合规写法", "系统不跨消息带上文，别再用「上下文审校」这个说法", false},
		{"公式只在否定与对照表语境", "字幕文件以对照表交付，不做公式重算", false},
		{"公式被当能力承诺", "Excel 翻译后公式自动重算", true},
		// 「算价公式」是真有的东西（/compare 页公开扣费公式），禁词收窄时必须仍然放过它，
		// 否则这条锁会有一天把正确文案一起抹掉。
		{"算价公式属合规", "公开算价公式，当场按你的字数算钱", false},
	}
	for _, c := range cases {
		bad := scanBanned("selfproof/"+c.name, c.text)
		if c.isBad && len(bad) == 0 {
			t.Errorf("反证失败：%q 应被抓到却判为合规", c.text)
		}
		if !c.isBad && len(bad) > 0 {
			t.Errorf("误伤：%q 本应合规，实际 %v", c.text, bad)
		}
	}
}
