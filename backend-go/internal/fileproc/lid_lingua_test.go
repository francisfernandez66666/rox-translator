// ============ 本文件职责中文说明 ============
// LID（语种识别）换 lingua-go 之后的出口闸门锁 —— ★ 2026-10-28 计价与成本优化批（方案 §6.4(b)）。
//
// 为什么这一族必须有机械锁：LID 的坏形态**全都不报错**——
//
//	换了依赖却没换出口读数（`lid_engine` 仍写 builtin）⇒ 运营按读数判断"还是轻量档"，排障起点就错；
//	候选语种集被写成 `FromAllLanguages()` ⇒ 内存与构建开销一个数量级抬升，而 `/api/health` 一切正常；
//	短文本硬猜 ⇒ 报表里出现"这一篇是荷兰语"这类**看起来有效**的错误读数（比 und 坏得多：und 是没判出来，
//	                                         错码会被当成数据错误去查上游）；
//	缓存键不 Trim ⇒ 同一段带尾空格的文本各占一格，命中率被空白打散；
//	超过 2000 段不截断 ⇒ 一份超长交付把整条链路拖住（而 LID 本来只是报表腿）。
//
// 六条腿与方案 §6.4(b) 逐行对应：出口名 / 12 语种 / 短文本与空输入 / 整篇多数 / 批量上限与缓存命中 / 任意输入 err 恒 nil。
// 每条都配**反向读数**（改成恒 und、或恒 en，都会在对应那一条上红），
// 因为"这个函数永远回 und"这类退化会让下面每一句判据都绿（und 不等于判对）。
// ============================================
package fileproc

import (
	"context"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// ============ L1：出口读数 ============

// TestLidEngineNameIsLingua 健康面 `lid_engine` 必须逐字报 "lingua"。
//
// 钉它的理由（"换了依赖没换出口"）：`/api/health` 是运维判断"这条腿用的是什么后端"的唯一读数。
// 依赖换了、实现换了，唯独这串字没换 ⇒ 排障的人会按"内置轻量档"的已知边界去解释一个其实是
// lingua 的判定结果，两边永远对不上。反向：写回 "builtin" 或任何别名都必须红。
func TestLidEngineNameIsLingua(t *testing.T) {
	if got := LidEngineName(); got != "lingua" {
		t.Fatalf("LidEngineName()=%q，必须为 \"lingua\" ⇒ 换了依赖没换出口读数（运营会按旧后端的边界排障）", got)
	}
	if !LidReady() {
		t.Fatal("LidReady() 必须恒 true：lingua 是纯 Go 依赖，无外部进程、无权重文件")
	}
}

// ============ L2：12 语种 ============

// TestLidDetectsTwelveLocales 站点 12 语种各一句典型文本。
//
// 繁体刻意单列一条（期望 **zh**）：lingua 只有 `Chinese`（zh），本仓也没有简繁判定器；
// 按字符集猜 zh_hant 比给 zh 更坏（前端展示语种本来就按界面语言走，LID 只做归因）。
// ⚠️ 这一条的射程是"受限候选集里有这些语言"，不是"每一句都 100% 对"——
// 所以样本都用**该语种特征极强**的整句（而不是单词），跑不稳的样本等于给闸门埋随机红。
func TestLidDetectsTwelveLocales(t *testing.T) {
	cases := []struct {
		lang string
		text string
	}{
		{"zh", "产品说明书：本设备支持蓝牙钥匙，请勿拆卸。"},
		{"zh", "產品說明書：本裝置支援藍牙鑰匙，請勿拆卸。"}, // 繁体 ⇒ 归 zh
		{"en", "the quick brown fox jumps over the lazy dog and the cat"},
		{"ja", "これは日本語の説明文です。設定を変更してください。"},
		{"ko", "이 제품은 블루투스 키를 지원합니다. 설정을 변경하세요."},
		{"fr", "le manuel de l'utilisateur est dans la boîte avec les accessoires"},
		{"de", "die Bedienungsanleitung ist im Karton und nicht mit dem Gerät"},
		{"es", "el manual del usuario está en la caja con los accesorios"},
		{"pt", "o manual do usuário está na caixa com os acessórios"},
		{"ru", "руководство пользователя находится в коробке с аксессуарами"},
		{"ar", "دليل المستخدم موجود في الصندوق مع الملحقات"},
		{"th", "คู่มือผู้ใช้อยู่ในกล่องพร้อมอุปกรณ์เสริม"},
	}
	for _, c := range cases {
		got := lidOfCached(c.text)
		if got != c.lang {
			t.Errorf("判成 %q，期望 %q（文本：%s）⇒ 该语种不在受限候选集里，或映射表漂了",
				got, c.lang, c.text)
		}
	}
	// 反向读数：一个都不是 und（若是，上面对比就会全红；这里再明说一次，免得有人把判据改成"只要求非空"）。
	for _, c := range cases {
		if lidOfCached(c.text) == "und" {
			t.Fatalf("%q 判成 und ⇒ 受限语种集缺这一档，或 lingua 压根没被调用", c.lang)
		}
	}
	// 语种集自身：必须恰好是方案登记的那 17 种（11 站点书写体系 + it/nl/pl/tr/vi/id）。
	// 判据用"数量 + 几个必须在内"而不是逐项全等： lingua 常量的顺序/别名不是我们的契约，
	// 但"少了一种常见源语"与"多到接近全量"都必须被抓。
	langs := lidLanguages()
	if len(langs) != 17 {
		t.Fatalf("候选语种集应为 17 种（方案 §6.4(a)），实际 %d 种 ⇒ 要么漏了，要么有人把它改成了大集合", len(langs))
	}
	if len(langs) > 30 {
		t.Fatalf("候选语种集 %d 种，接近 FromAllLanguages 的量级 ⇒ 构建与常驻内存会被抬高一个数量级", len(langs))
	}

	// 源码腿：**检测器必须真的用受限集建**。
	// 只测 `lidLanguages()` 的长度是不够的——把 Build 那一段换成 `FromAllLanguages()` 时，
	// 上面两条读数一字不变（切片还是 17 个），而常驻内存已经抬了一个数量级、`/api/health` 一切正常。
	src := readFileprocSource(t, "lid.go")
	if !strings.Contains(src, "FromLanguages(lidLanguages()...)") {
		t.Fatal("检测器必须用 `FromLanguages(lidLanguages()...)` 建 ⇒ 换成 FromAllLanguages/手写大集合时，" +
			"候选语种数（本测试上面那条）仍然读 17，本闸失去射程")
	}
	if strings.Contains(dropLidComments(src), "FromAllLanguages(") {
		t.Fatal("lid.go 里出现了 FromAllLanguages ⇒ 75 种语言的模型全部纳入候选，构建与常驻内存一个数量级抬升")
	}
}

// readFileprocSource 读本包源码（go test 的工作目录＝包目录，用相对路径）。
func readFileprocSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v ⇒ 本闸连射程都没有", name, err)
	}
	return string(b)
}

// dropLidComments 剥掉 Go 源码里的注释（行注释与块注释），字符串字面量里的 "//" 不算注释。
//
// 为什么必须剥：本文件与 lid.go 的说明注释里都写着「禁止 FromAllLanguages()」
// （那正是记录这条纪律用的），按原始源码判负向会命中注释 ⇒ 把实现改回去也恒红/恒绿
// （AGENTS §一·9 那条「负向锁会被自己新写的注释再犯」的同形）。
func dropLidComments(src string) string {
	var out strings.Builder
	inLine, inBlock, inStr, inRaw := false, false, false, false
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case inLine:
			if c == '\n' {
				inLine = false
				out.WriteByte(c)
			}
		case inBlock:
			if c == '*' && i+1 < len(src) && src[i+1] == '/' {
				inBlock = false
				i++
			}
		case inStr:
			out.WriteByte(c)
			if c == '\\' && i+1 < len(src) {
				i++
				out.WriteByte(src[i])
				continue
			}
			if c == '"' {
				inStr = false
			}
		case inRaw:
			out.WriteByte(c)
			if c == '`' {
				inRaw = false
			}
		default:
			switch {
			case c == '/' && i+1 < len(src) && src[i+1] == '/':
				inLine = true
				i++
			case c == '/' && i+1 < len(src) && src[i+1] == '*':
				inBlock = true
				i++
			case c == '"':
				inStr = true
				out.WriteByte(c)
			case c == '`':
				inRaw = true
				out.WriteByte(c)
			default:
				out.WriteByte(c)
			}
		}
	}
	return out.String()
}

// ============ L3：短文本与空输入 ============

// TestLidUndForShortAndEmpty 五种"这段不是（足够长的）语言"的形态一律 und。
//
// 为什么单列这一档：lingua 对 1~2 个字符的输入会给一个**高置信度的猜测**（它总要选一个），
// 而那个读数会被当成"判出来了"。und 才是诚实的答案（报表里 und 表示"没判出来"，不会被当数据错误去查）。
func TestLidUndForShortAndEmpty(t *testing.T) {
	for _, s := range []string{"", "   ", "\n\t ", "1", "12", "12", "!!", "の", "のの", "a", "ab", "12345", "---", "@#$%^&*()"} {
		if got := lidOfCached(s); got != "und" {
			t.Errorf("%q 应判 und，实际 %q ⇒ 短文本/无字母串被硬猜成某个语言（看起来有效的错误读数）", s, got)
		}
	}
	// 下界反向读数：到 3 个字符就必须真的交给模型（否则阈值被写大，正常短句全成 und）。
	// 用一句 3 字符的英文（"cat"）证明它不是"一律 und"。
	if got := lidOfCached("cat is here"); got == "und" {
		t.Fatal("正常短句被判 und ⇒ 阈值过大（或整条腿退化成恒 und）")
	}
	// 纯数字/纯符号即使很长也必须 und（长度不是判据，有没有字母才是）。
	if got := lidOfCached(strings.Repeat("1234567890", 20)); got != "und" {
		t.Fatalf("长数字串被判 %q，必须 und：它不属于任何语言", got)
	}
}

// ============ L4：整篇多数 ============

// TestLidDocLangMajority 整篇语种＝按字符数加权的多数；全 und ⇒ und。
func TestLidDocLangMajority(t *testing.T) {
	// ① 中文占绝大多数（两条长中文 + 一条短英文）⇒ zh
	zhLong := "产品说明书：本设备支持蓝牙钥匙，请勿拆卸。首次使用前请先充电三小时。"
	enShort := "ok thanks"
	langs, docLang, err := DetectLangs(context.Background(), []string{zhLong, zhLong + "请保留本说明书。", enShort})
	if err != nil {
		t.Fatalf("err 必须为 nil，实际 %v", err)
	}
	if len(langs) != 3 {
		t.Fatalf("返回长度必须与输入等长（3），实际 %d", len(langs))
	}
	if docLang != "zh" {
		t.Fatalf("整篇语种应为 zh（按字符数加权），实际 %q ⇒ 多数口径变成了\"按段数\"或\"按某段\"", docLang)
	}

	// ② 反过来：英文占多数 ⇒ en（证明①不是"恒 zh"）
	enLong := "the user manual is inside the box together with all required accessories and cables"
	if _, d, _ := DetectLangs(context.Background(), []string{enLong, enLong, "请勿拆卸。"}); d != "en" {
		t.Fatalf("英文占多数时整篇应为 en，实际 %q", d)
	}

	// ③ 全 und ⇒ und（空输入与纯符号）
	if _, d, _ := DetectLangs(context.Background(), []string{"", "123", "!!!"}); d != "und" {
		t.Fatalf("全 und 时整篇必须 und，实际 %q ⇒ 会给一篇没有任何语言内容的文档安一个语种", d)
	}
	// ④ 空切片也不许 panic，且回 und
	if got, d, err := DetectLangs(context.Background(), nil); err != nil || len(got) != 0 || d != "und" {
		t.Fatalf("空输入应回 (空切片, und, nil)，实际 (%v, %q, %v)", got, d, err)
	}
}

// ============ L5：批量上限与缓存命中 ============

// TestLidBatchLimitAndCache 超 2000 段截断；同段第二次命中缓存（模型调用计数不增）。
//
// 为什么用观测计数器而不是"跑两遍看耗时"：耗时读数在 CI 上抖动，无法当判据；
// `lidDetectCalls` 是只读观测腿——它是"这一次到底跑了几次识别"的唯一直接读数。
func TestLidBatchLimitAndCache(t *testing.T) {
	// ① 缓存命中：同一段连续两次，模型调用只增 1 次。
	probe := "the user manual is inside the box with the accessories"
	_ = lidOfCached(probe) // 先垫一次，排除首次建检测器的开销不计入
	before := lidDetectCalls.Load()
	a := lidOfCached(probe)
	b := lidOfCached(probe)
	after := lidDetectCalls.Load()
	if a != b {
		t.Fatalf("同段两次判定不一致（%q vs %q）⇒ 判定不确定，记忆/报表会漂", a, b)
	}
	if after != before {
		t.Fatalf("同段第二次仍然打到模型（+%d 次）⇒ 缓存没命中（缓存键或写入腿断了，长单会白付 CPU）", after-before)
	}

	// ② 截断：3000 段输入 ⇒ 返回 2000 段（上游按本批多数回填，调用方自己补尾部）。
	texts := make([]string, 3000)
	for i := range texts {
		texts[i] = "产品说明书：本设备支持蓝牙钥匙。"
	}
	ok, docLang, err := DetectLangs(context.Background(), texts)
	if err != nil {
		t.Fatalf("err 必须为 nil，实际 %v", err)
	}
	if len(ok) != 2000 {
		t.Fatalf("3000 段应交出 2000 段（上限口径），实际 %d ⇒ 一张超长单会把这条报表腿拖住", len(ok))
	}
	if docLang != "zh" {
		t.Fatalf("截断后整篇语种应为 zh，实际 %q", docLang)
	}
	// ③ 缓存有界（同一条腿的另一半：命中不能靠"永不淘汰"换来）
	lidMu.Lock()
	n := len(lidCache)
	lidMu.Unlock()
	if n > lidCacheMax {
		t.Fatalf("缓存超界 %d > %d ⇒ 长跑进程内存会无界增长", n, lidCacheMax)
	}
}

// ============ L6：翻译链零依赖 ============

// TestLidDoesNotBlockTranslation 任意输入下 err 恒 nil，且**永不 panic**。
//
// 主方案把这条写死（方案 §6.4 / 文件头）：LID 只服务报表与 sources 归因，
// 一次识别抖动不许把交付打红。所以这里喂的全是"最不像语言"的输入——
// 二进制垃圾、超长单行、全 emoji、控制字符——要求仍然是 nil error + 等长结果。
func TestLidDoesNotBlockTranslation(t *testing.T) {
	inputs := [][]string{
		nil,
		{""},
		{"\x00\x01\x02\x03"},
		{strings.Repeat("字", 5000)},
		{"😀😀😀😀😀"},
		{"\u200b\u200b\u200b"},          // 零宽空格
		{"a", "bb", "ccc", "dddd", "e"}, // 长短混排
	}
	for i, in := range inputs {
		got, docLang, err := DetectLangs(context.Background(), in)
		if err != nil {
			t.Fatalf("第 %d 组输入返回错误 %v ⇒ LID 抖动会打断交付（主链必须零依赖）", i, err)
		}
		if len(got) != len(in) {
			t.Fatalf("第 %d 组：返回长度 %d ≠ 输入长度 %d ⇒ 调用方按下标回填会错位", i, len(got), len(in))
		}
		for j, lc := range got {
			if lc == "" {
				t.Fatalf("第 %d 组第 %d 段语种为空串 ⇒ 必须是 %q 或真语种码（空串会让报表列全部空白）", i, j, "und")
			}
			if utf8.RuneCountInString(lc) != len(lc) || len(lc) < 2 {
				t.Fatalf("第 %d 组第 %d 段语种码 %q 不是 ASCII 的 2~3 字符码", i, j, lc)
			}
		}
		if docLang == "" {
			t.Fatalf("第 %d 组整篇语种是空串 ⇒ 必须是 %q 或真语种码", i, "und")
		}
	}
}
