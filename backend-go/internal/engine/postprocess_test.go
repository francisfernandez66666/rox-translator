// ============ 本文件职责中文说明 ============
// 后处理清洗链（postprocess）的单元测试：审校模板标记剥离与空占位方括号清扫，
// 覆盖需求3「单句翻译误带入【原文】/【待審校譯文】等标记」与 2026-09-02 实测的
// 『【】Please perform…』符号残留根因，防止回归。
// ============================================
package engine

import "testing"

// TestStripReviewMarkers 验证各类审校模板残留都能被剥离成干净译文。
func TestStripReviewMarkers(t *testing.T) {
	cases := []struct{ in, want string }{
		// 完整模板：取最后标记之后的译文
		{"【原文】你好\n【待審校譯文】Hello", "Hello"},
		{"【原文】你好\n【译文】Hello", "Hello"},
		{"【原文】你好\n【待审校译文】你好\n【译文】Hello", "Hello"},
		{"[原文]你好\n[译文]Hello", "Hello"},
		// 仅一个标记 + 译文：直接取译文
		{"【译文】Hello", "Hello"},
		{"【待審校譯文】Hello", "Hello"},
		{"【原文】Please perform the translation", "Please perform the translation"},
		// 无后续译文：删除全部标记，保留残余文本
		{"【原文】你好【待審校譯文】", "你好"},
		{"【原文】你好", "你好"},
		// 无标记：原样返回（不误删正常内容）
		{"【贵宾】请进", "【贵宾】请进"},
		{"普通文本", "普通文本"},
	}
	for _, c := range cases {
		if got := stripReviewMarkers(c.in); got != c.want {
			t.Errorf("stripReviewMarkers(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

// TestStripEmptyPlaceholderBrackets 验证成对空占位方括号（含内部空白/换行）全部剥离。
func TestStripEmptyPlaceholderBrackets(t *testing.T) {
	cases := []struct{ in, want string }{
		{"【】Please perform the translation", "Please perform the translation"},
		{"【 】Please perform", "Please perform"},
		{"【\n】Please perform", "Please perform"},
		{"[]Please", "Please"},
		{"[ ]Please", "Please"},
		{"Hello 【】", "Hello "},
		{"【】【】Please", "Please"},
		// 带内容的方括号不受影响
		{"【贵宾】请进", "【贵宾】请进"},
		{"【2】", "【2】"},
	}
	for _, c := range cases {
		if got := stripEmptyPlaceholderBrackets(c.in); got != c.want {
			t.Errorf("stripEmptyPlaceholderBrackets(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

// TestPostProcessSymbolRemnant 端到端回归：2026-09-02 实测 xlsx 译文单元格
// 『【】Please perform…』式残留，以及中文删除后新生成空占位括号（【原文】→【】）的二次清扫。
func TestPostProcessSymbolRemnant(t *testing.T) {
	cases := []struct {
		in, lang, want string
	}{
		// en 目标：实测残留 → 必须完全干净
		{"【】Please perform the translation", "en", "Please perform the translation"},
		{"【原文】Please perform the translation", "en", "Please perform the translation"},
		{"【译文】Please perform the translation", "en", "Please perform the translation"},
		// en 目标：中文删除后不得残留空方括号
		{"【原文】你好", "en", ""},
		// zh_hant 目标：标记整块剥离，保留译文
		{"【原文】你好\n【待審校譯文】您好", "zh_hant", "您好"},
		// 带内容的正常方括号不受影响
		{"【贵宾】請進", "zh_hant", "【贵宾】請進"},
	}
	for _, c := range cases {
		if got := PostProcessTranslation(c.in, c.lang); got != c.want {
			t.Errorf("PostProcessTranslation(%q,%q)=%q, want %q", c.in, c.lang, got, c.want)
		}
	}
}

// TestStripTrailingCJKNotesStrict CJK 目标语（zh_hant/ja/ko）注释残留截断：
// 译文本身就是中文，无法用「去中文后剩骨架」识别，仅以严格行首特征（开括号+全角冒号）截断。
func TestStripTrailingCJKNotesStrict(t *testing.T) {
	cases := []struct{ in, want string }{
		// zh_hant：结尾注释块以（：开头 → 截断
		{"請先進行驗證碼校驗，通過後再進行該操作。\n\n（：1. \"verification marks\"\"驗證碼\"；2. \"passed\"\"驗證通過\"；3. \"confirmed\"\"通過\"。）",
			"請先進行驗證碼校驗，通過後再進行該操作。"},
		// zh_hant：正常行首括号（不带冒号）→ 保留
		{"（此為備註）請填寫以下資訊。\n下一行內容。", "（此為備註）請填寫以下資訊。\n下一行內容。"},
		// ja：正常正文（含全角括号但不以（：开头）→ 保留
		{"認証コードを入力してください。\n補足（はんこ）を参照。", "認証コードを入力してください。\n補足（はんこ）を参照。"},
		// zh_hant：真实模型注释块且第一行就是纯注释 → 整体截断为空译文主体
		{"（：1. 術語對照 2. 解析說明。）", ""},
	}
	for _, c := range cases {
		if got := stripTrailingCJKNotesStrict(c.in); got != c.want {
			t.Errorf("stripTrailingCJKNotesStrict(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

// TestExtractContractTranslation 输出契约白名单提取：
// <t>…</t> 包裹时只取标签内内容（标签外注释块丢弃）；未命中契约时原样返回（零回归）。
func TestExtractContractTranslation(t *testing.T) {
	cases := []struct{ in, want string }{
		// 标准契约：标签内为译文，标签外注释被丢弃
		{"<t>Please wait for the verification code.</t>\n（术语对照：verification=验证码）",
			"Please wait for the verification code."},
		// 仅标签内容，无歧义
		{"<t>这是最终译文。</t>", "这是最终译文。"},
		// 多段 <t> 块按行拼接保留
		{"<t>第一段</t>\n<t>第二段</t>", "第一段\n第二段"},
		// 未命中契约（模型违约）：原样返回
		{"Please wait for the verification code.\n（术语对照）", "Please wait for the verification code.\n（术语对照）"},
		{"请先进行验证码校验，通过后再进行该操作。", "请先进行验证码校验，通过后再进行该操作。"},
		// 标签带属性 / 大小写容忍
		{"<T lang=\"en\">Hello world</T>", "Hello world"},
		// 未闭合标签：视为违约，原样返回
		{"<t>未闭合内容", "<t>未闭合内容"},
		// 标签内为空 → 原样返回
		{"<t></t>noink", "<t></t>noink"},
	}
	for _, c := range cases {
		if got := extractContractTranslation(c.in); got != c.want {
			t.Errorf("extractContractTranslation(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

// TestPostProcessContractEndToEnd 端到端回归：模型输出「契约译文 + 标签外中文注释」时，
// 各目标语（en / zh_hant / zh）都能得到干净译文——白名单提取 + CJK 严格截断共同兜底。
func TestPostProcessContractEndToEnd(t *testing.T) {
	cases := []struct {
		in, lang, want string
	}{
		// en：契约命中，标签外注释整体丢弃
		{"<t>Please perform verification before proceeding.</t>\n（：1. \"驗證碼\"；2. \"通過\"。）",
			"en", "Please perform verification before proceeding."},
		// zh_hant：契约命中，注释被丢弃
		{"<t>請先進行驗證碼校驗。</t>（：1. 術語對照 2. 解析。）",
			"zh_hant", "請先進行驗證碼校驗。"},
		// en：模型违约未用标签 → 走既有黑名单清洗（行首（：注释残留截断）
		{"Please perform verification before proceeding.\n\n（：1. \"verification\"；2. \"passed\"。）",
			"en", "Please perform verification before proceeding."},
		// zh_hant：模型违约未用标签 → CJK 严格截断
		{"請先進行驗證碼校驗。\n（：1. 術語對照 2. 解析。）",
			"zh_hant", "請先進行驗證碼校驗。"},
		// zh：互译目标为简体中文，注释块截断 + 正文保留
		{"<t>请先进行验证码校验。</t>（：1. 术语 2. 解析。）",
			"zh", "请先进行验证码校验。"},
	}
	for _, c := range cases {
		if got := PostProcessTranslation(c.in, c.lang); got != c.want {
			t.Errorf("PostProcessTranslation(%q,%q)=%q, want %q", c.in, c.lang, got, c.want)
		}
	}
}

// TestZhTargetNotWiped 回归：互译（如 en→zh）目标为简体中文时，译文不得被清空——
// StripChineseInNonZh 守卫曾遗漏 "zh"，把整段中文删成只剩标点（实测 "…该操作。"→"，。"）。
func TestZhTargetNotWiped(t *testing.T) {
	cases := []struct{ in, want string }{
		{"请先进行验证码校验，通过后再进行该操作。", "请先进行验证码校验，通过后再进行该操作。"},
		{"请填写以下信息并提交。", "请填写以下信息并提交。"},
	}
	for _, c := range cases {
		if got := PostProcessTranslation(c.in, "zh"); got != c.want {
			t.Errorf("PostProcessTranslation(%q,'zh')=%q, want %q", c.in, got, c.want)
		}
	}
}

// TestBrandReplaceVariants 验证极石汽车品牌拼音变体（jishi/jieshi/jixi 等）全部替换为 ROX。
func TestBrandReplaceVariants(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Jishi Auto", "ROX Auto"},
		{"Jieshi Auto Center", "ROX Auto Center"},
		{"Jixi Auto", "ROX Auto"},
		{"jieshi Auto", "ROX Auto"},
		{"ji shi Auto", "ROX Auto"},
		{"ji-shi Auto", "ROX Auto"},
		{"去极石汽车服务中心", "去ROX汽车服务中心"},
		{"no brand here", "no brand here"},
	}
	for _, c := range cases {
		if got := brandReplace(c.in); got != c.want {
			t.Errorf("brandReplace(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

// TestStripTrailingCJKNotes 验证译文末尾的中文「编辑注释/术语对照」残留块被整体截断，
// 修复用户反馈的快速翻译英文遗留乱码问题（（：，：1. ：… 骨架）。
func TestStripTrailingCJKNotes(t *testing.T) {	cases := []struct{ in, want string }{
		// 末尾注释块起始行：纯全角标点无字母无数字 → 截断
		{"VIII. Component protection.\n\n（：，：\n1. ：\"\"brake rotors\"\"\n2. ：（±2%）",
			"VIII. Component protection."},
		// ★ 讲解式残留（工单 T20260909111254JZ7）：去汉字后含英文+序号数字+全角标点，
		//   以全角括号开头 → 截断（旧判定不含字母不含数字不命中，QA 数字检查误报 1 2 3）
		{"Please wait for the verification code to appear before proceeding. Only perform the operation after verification is confirmed.\n\n（：1. \"verification marks\"\"verification code\"；2. \"passed the verification\"\"verification is confirmed\"；3. ，\"before proceeding\"\"after verification\"，\"confirmed\"\"passed\"。）",
			"Please wait for the verification code to appear before proceeding. Only perform the operation after verification is confirmed."},
		// 无注释块：原样保留
		{"Normal English line 1\nAnother line 10 km/h.", "Normal English line 1\nAnother line 10 km/h."},
		// 正常英文以半角符号开头不受影响（全角标点才是注释残留特征）
		{"(quoted) English line 1\nmore here.", "(quoted) English line 1\nmore here."},
	}
	for _, c := range cases {
		if got := stripTrailingCJKNotes(c.in); got != c.want {
			t.Errorf("stripTrailingCJKNotes(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

// TestCollapseRepeatedConjunctions 验证重复连词折叠功能：
// 模型偶发连续输出 "and and and" 等重复连词，折叠为单次。
func TestCollapseRepeatedConjunctions(t *testing.T) {
	cases := []struct{ in, want string }{
		{"and and and", "and"},
		{"and and", "and and"},             // 仅 2 次不折叠
		{"or or or or", "or"},              // 4 次也折叠
		{"The mountains and and and seas", "The mountains and seas"},
		{"with with with benefits", "with benefits"},
		{"the the the quick", "the quick"},
		{"Normal text without repetition", "Normal text without repetition"},
		{"and and AND", "and"},             // 大小写混合
	}
	for _, c := range cases {
		if got := collapseRepeatedConjunctions(c.in); got != c.want {
			t.Errorf("collapseRepeatedConjunctions(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

// TestDetectSourceLang 源语言检测：CJK >25% 为中文；韩/日/阿/俄/纯拉丁各有分支。
func TestDetectSourceLang(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"中文", "你好世界", "zh"},
		{"英文", "Hello World", "en"},
		{"韩文", "안녕하세요", "ko"},
		{"俄文", "Привет мир", "ru"},
		{"阿拉伯文", "مرحبا بالعالم", "ar"},
		{"空字符串", "", "en"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DetectSourceLang(c.in); got != c.want {
				t.Errorf("DetectSourceLang(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestStripLangPrefix 语言名前缀剥离：各语言「语言名：」前缀应被去除。
// 注意：regex 匹配的是英文/中文名（如 "English:"、"英语："），不是原生语言名。
func TestStripLangPrefix(t *testing.T) {
	cases := []struct {
		lang, in, want string
	}{
		{"en", "English: Hello World", "Hello World"},
		{"en", "英语：Hello World", "Hello World"},
		{"ru", "Russian: Привет", "Привет"},
		{"ru", "俄语：Привет", "Привет"},
		{"ar", "Arabic: مرحبا", "مرحبا"},
		{"ar", "阿拉伯语：مرحبا", "مرحبا"},
		{"en", "Hello World", "Hello World"}, // 无前缀原样
	}
	for _, c := range cases {
		t.Run(c.lang+"_"+c.in, func(t *testing.T) {
			if got := StripLangPrefix(c.in, c.lang); got != c.want {
				t.Errorf("StripLangPrefix(%q,%q) = %q, want %q", c.in, c.lang, got, c.want)
			}
		})
	}
}
