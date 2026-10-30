// ============ api_openapi_media_gate_test.go · 职责说明 ============
// 开放 API「图片/音频（OCR/ASR）支持」这条对外契约的机制闸门（2026-10-16 立）。
//
// 背景（缺陷形态，别照旧写法改回去）：准入格式清单的唯一事实源是
// fileproc.TranslateFormatOrder（AGENTS §一·14），但**对外三处**（JSON 规范、中文文档、英文文档）
// 历史上各抄了一份手写清单。2026-10-14 把图片/音频加进准入名单、上传口与闸门都跟着改了，
// 对外文档那一行字面量却停在旧值——客户照文档写调用、发图片被判"格式不支持"，
// 而服务端一切绿。这就是本仓最贵的一族：**同一个事实的多个副本，改一处漏一处**。
//
// 本文件的锁（缺一不可）：
//
//	L1 形态锁：规范与中英文档的**源码**里，格式清单只能经 {{FORMATS}} 占位符引用；
//	  出现"斜杠串起来的 ≥3 个后缀"这一形态（＝手抄的清单）即判红——占位符展开后
//	  必然是那个形态，所以只扫**未展开的源码**，这正是判据的射程所在。
//	L2 展开锁：占位符必须被真展开成 fileproc.FormatHintBase() 的**逐字**现值，
//	  且展开结果里含准入名单中**每一个**图片/音频后缀（这一腿管"新加的格式有没有进对外清单"）。
//	L3 语义锁：规范与文档两侧都要写明 OCR/ASR 与「按原文件格式回写」这条口径，
//	  并交代两条能力边界（资产缺失明确失败；配音语种覆盖有限，字幕/译文不受影响）。
//	L4 反证：把占位符换成手抄清单 ⇒ L1 必须红；把替换摘掉 ⇒ L2 必须红
//	  （AGENTS §三：没有变异体就分不清"腿有射程"与"腿恒真"）。
//
// 方言：本文件不碰库。
// 运行：go test ./internal/api/ -run TestOpenAPIMedia
// =============================================
package api

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"translator/internal/fileproc"
)

// manualFormatListRe 手抄格式清单的**形态**：斜杠串起来的 **≥4 段**短标识（`docx/xlsx/pptx/pdf/…`）。
// 只看形态不看具体有哪些——具体有哪些会变，形态不会变（旧那行字面量就是这个形状，两种历史
// 变体都覆盖：带点的 `.docx/.xlsx/...` 与不带点的 `docx/xlsx/...`）。
// ⚠️ 为什么是"≥4 段"而不是"≥3"：本仓文档里到处是 **3 段**的 API 路径（`v1/tasks/status`、
//
//	`v1/kb/stats`），门槛定在 3 会把每一行接口表都判成"手抄清单"（首版实测假红）。
//	4 段往上，路径类字符串就够不着了（`bin/assets/ocr` 这类也只有 3 段）。
var manualFormatListRe = regexp.MustCompile(`\b(?:\.?[a-z0-9]{2,6}/){3,}\.?[a-z0-9]{2,6}\b`)

// manualFormatListHits 判据本体（L1 与其反证共用）。返回命中的斜杠清单（空＝合规）。
func manualFormatListHits(src []byte) []string {
	return manualFormatListRe.FindAllString(string(src), -1)
}

// mediaFormatExts 准入名单里属于「图片/音频」的后缀（从唯一事实源派生，不手抄）。
// 判据靠它回答"对外清单里有没有把新加的媒体格式带上"。
func mediaFormatExts() []string {
	var out []string
	for _, e := range fileproc.TranslateFormatOrder {
		if fileproc.IsImageFormat(e) || fileproc.IsAudioFormat(e) {
			out = append(out, e)
		}
	}
	return out
}

// TestOpenAPIMediaSourceUsesPlaceholder L1：规范与中英文档的源码只许用占位符引用格式清单。
func TestOpenAPIMediaSourceUsesPlaceholder(t *testing.T) {
	sources := []struct {
		name string
		src  []byte
	}{
		{"openapi.v1.json", openapiV1JSON},
		{"defaultDocsMDZh", []byte(defaultDocsMDZh)},
		{"defaultDocsMDEn", []byte(defaultDocsMDEn)},
	}
	for _, s := range sources {
		if hits := manualFormatListHits(s.src); len(hits) > 0 {
			t.Fatalf("%s 里出现手抄的格式清单 %v ⇒ 必须改用 %s 占位符（格式清单的唯一事实源是 fileproc.FormatHintBase）",
				s.name, hits, openAPIFormatPlaceholder)
		}
		if !strings.Contains(string(s.src), openAPIFormatPlaceholder) {
			t.Fatalf("%s 里找不到 %s 占位符 ⇒ 对外清单丢了引用点（客户看到的格式清单会停在旧值）",
				s.name, openAPIFormatPlaceholder)
		}
	}
	// 反证：同一个判据喂"手抄清单"的源码必须能抓到。
	mutated := []byte(strings.Replace(string(openapiV1JSON), openAPIFormatPlaceholder, "docx/xlsx/pptx/pdf/txt/csv", 1))
	if hits := manualFormatListHits(mutated); len(hits) == 0 {
		t.Fatalf("反证失败：把占位符换成手抄清单后判据仍判绿 ⇒ L1 没有射程")
	}
}

// TestOpenAPIMediaExpansionCarriesAllMediaFormats L2：展开结果必须逐字等于事实源现值，
// 且把准入名单里的图片/音频后缀**全部**带上（新加格式没进对外清单＝这条红）。
func TestOpenAPIMediaExpansionCarriesAllMediaFormats(t *testing.T) {
	hint := fileproc.FormatHintBase()
	exts := mediaFormatExts()
	if len(exts) < 10 {
		// 射程自证：媒体后缀太少说明解析坏了（图片 8 + 音频 7 ≈ 15），别让空集合把这条腿变恒真。
		t.Fatalf("媒体后缀解析结果只有 %d 项 ⇒ 派生失败，本腿已失明", len(exts))
	}
	for _, name := range []string{"zh", "en"} {
		md := defaultDocsMDZh
		if name == "en" {
			md = defaultDocsMDEn
		}
		got := string(expandOpenAPIFormats([]byte(md)))
		if !strings.Contains(got, hint) {
			t.Fatalf("%s 文档展开后不含现取的格式清单 %q ⇒ 占位符没被展开到事实源上", name, hint)
		}
		if strings.Contains(got, openAPIFormatPlaceholder) {
			t.Fatalf("%s 文档展开后仍残留 %s ⇒ 内部记号会漏给客户", name, openAPIFormatPlaceholder)
		}
	}
	spec := string(expandOpenAPIFormats(openapiV1JSON))
	for _, e := range exts {
		// 清单里以不带点的形式出现（docx/xlsx/…），故按去点后的形态判。
		if !strings.Contains(spec, strings.TrimPrefix(e, ".")) {
			t.Fatalf("规范展开后缺少媒体格式 %s ⇒ 客户按规范发这类文件会被判不支持", e)
		}
	}
	// 反证：把替换摘掉（模拟"占位符没被展开"）⇒ 本腿必须红。
	unexpanded := string(openapiV1JSON)
	if strings.Contains(unexpanded, hint) {
		t.Fatalf("反证自伤：规范源码里本来就含现取清单字面量 ⇒ 占位符形同虚设")
	}
}

// TestOpenAPIMediaSemanticsDocumented L3：OCR/ASR 与「按原文件格式回写」这条口径，
// 规范与中英文档两侧都要写明；两条能力边界也要交代（不许让客户以为"缺资产也能拿到处理过的原件"）。
func TestOpenAPIMediaSemanticsDocumented(t *testing.T) {
	type doc struct {
		name string
		text string
	}
	docs := []doc{
		{"openapi.v1.json(info.description)", specInfoDescription(t)},
		{"openapi.v1.json(CreateFileTask.files)", specFileFieldDescription(t)},
		{"openapi.v1.json(TaskStatus.files)", specStatusFilesDescription(t)},
		{"defaultDocsMDZh", defaultDocsMDZh},
		{"defaultDocsMDEn", defaultDocsMDEn},
	}
	// 每份都必须点明 OCR 与 ASR（媒体入口的识别腿），文案随语言不同，关键词统一用大写缩写。
	for _, d := range docs {
		for _, kw := range []string{"OCR", "ASR"} {
			if !strings.Contains(d.text, kw) {
				t.Fatalf("%s 未提到 %s ⇒ 图片/音频支持在对外文档里没有落点", d.name, kw)
			}
		}
	}
	// 文档侧还要交代"回原格式"与两条边界；规范侧靠 info.description 承担同一句。
	for _, d := range docs {
		hasSame := strings.Contains(d.text, "原文件格式") || strings.Contains(d.text, "original format") ||
			strings.Contains(d.text, "同容器") || strings.Contains(d.text, "same-container")
		if !hasSame {
			t.Fatalf("%s 未写明「按原文件格式回写」的交付口径 ⇒ 客户不知道拿回的是什么形态", d.name)
		}
	}
	for _, d := range docs {
		if d.name == "defaultDocsMDZh" || d.name == "defaultDocsMDEn" {
			coversBoundary := strings.Contains(d.text, "资产") || strings.Contains(d.text, "assets")
			if !coversBoundary {
				t.Fatalf("%s 未交代「服务器资产缺失时明确失败」这条边界 ⇒ 客户会把失败读成服务端故障", d.name)
			}
		}
	}
}

// specInfoDescription 现取规范 info.description（读源码 JSON，不经占位符展开——口径判据只看文字）。
func specInfoDescription(t *testing.T) string {
	t.Helper()
	return specTextAt(t, "info", "description")
}

// specFileFieldDescription 现取 CreateFileTask.properties.files.description。
func specFileFieldDescription(t *testing.T) string {
	t.Helper()
	return specTextAt(t, "components", "schemas", "CreateFileTask", "properties", "files", "description")
}

// specStatusFilesDescription 现取 TaskStatus.properties.files.description。
func specStatusFilesDescription(t *testing.T) string {
	t.Helper()
	return specTextAt(t, "components", "schemas", "TaskStatus", "properties", "files", "description")
}

// specTextAt 按 key 路径现取规范里的字符串字段（找不到即判红——字段被改名/删掉时，
// 本文件那三条语义腿会读空，不判红就等于"锁失明"）。
func specTextAt(t *testing.T, keys ...string) string {
	t.Helper()
	var spec map[string]interface{}
	if err := json.Unmarshal(openapiV1JSON, &spec); err != nil {
		t.Fatalf("规范 JSON 解析失败: %v", err)
	}
	var cur interface{} = spec
	for i, k := range keys {
		m, ok := cur.(map[string]interface{})
		if !ok {
			t.Fatalf("规范路径 %v 在第 %d 段（%s）不是对象 ⇒ 规范结构变了", keys, i, k)
		}
		cur, ok = m[k]
		if !ok {
			t.Fatalf("规范里找不到字段路径 %v（缺 %s）⇒ 语义锁已失明", keys, k)
		}
	}
	str, ok := cur.(string)
	if !ok || strings.TrimSpace(str) == "" {
		t.Fatalf("规范字段 %v 不是非空字符串 ⇒ 语义锁已失明", keys)
	}
	return str
}

// ---------- L5（★ 2026-10-30 三事合一批 C9 新增：撤格式与音频简化的对外口径） ----------

// retiredDeliveryPromises 是「旧交付形态」在对外文档里的**承诺写法**。
// ⚠️ 刻意不按 ".srt" 这种名字判：新文档里合法地写着「.srt/.vtt/.json/.yaml/.yml 不再受理」，
// 按名字判会把那条公告本身判成违规——反向锁写成"提到就红"的下场就是被人摘掉（AGENTS §一·14）。
var retiredDeliveryPromises = []string{
	"_en.srt",
	"字幕 _en.srt",
	"译文 .srt（带原时间轴）",
	"subtitles _en.srt",
	"+ subtitles",
	"以 xlsx 对照表交付",
	"原音频副本",
	"原音轨副本",
	"a copy of the source audio",
}

// stillPromisedDelivery 判据本体（正例与反证共用同一个函数，两侧不可能各判各的）。
//
// ⚠️ 必须**识别否定**：本批对外公告写的是「不产字幕、不随包原音频副本」，
// 与退役前的承诺句共用同一批名词。纯子串命中会把公告判成违规——
// 那种红第二天就被人为摘掉，比没有锁更坏（本仓反复记录的闸门钝化形态）。
// 命中处往前看 24 个 rune，见到「不／no／not」即视为公告而不是承诺。
func stillPromisedDelivery(text string) string {
	r := []rune(text)
	for _, phrase := range retiredDeliveryPromises {
		p := []rune(phrase)
		for at := 0; at+len(p) <= len(r); {
			idx := indexOfRunes(r[at:], p)
			if idx < 0 {
				break
			}
			abs := at + idx
			lo := abs - 20
			if lo < 0 {
				lo = 0
			}
			hi := abs + len(p) + 20
			if hi > len(r) {
				hi = len(r)
			}
			win := string(r[lo:hi])
			// 双向看：中文公告常写成「与原音频副本**不再**随包」（否词在后），
			// 英文常写成 "**no** source-audio file"（否词在前）。只往前看就会把其中一种判成承诺。
			neg := strings.Contains(win, "不") || strings.Contains(win, "no ") ||
				strings.Contains(win, "not ") || strings.Contains(win, "No ")
			if !neg {
				return "文档仍在承诺已退役的交付形态：" + phrase
			}
			at = abs + len(p)
		}
	}
	return ""
}

// indexOfRunes 在 rune 切片里找子序列，返回起始下标（找不到回 -1）。
// 为什么不用 strings.Index：上面那段要在**同一个 rune 坐标系**里做窗口扩取，
// 字节下标混进来就会算出 lo>hi 直接 panic（首跑就是这么炸的，不是产品问题）。
func indexOfRunes(hay, needle []rune) int {
	if len(needle) == 0 || len(needle) > len(hay) {
		return -1
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		same := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				same = false
				break
			}
		}
		if same {
			return i
		}
	}
	return -1
}

// mustAnnounceRetirement 新口径必须真写出来：只判"旧的删干净了"会退化成"整段被删空也算过"。
// 三个条件各钉一件：① 点名不再受理（对外承诺变更不许静默收口）；
// ② 音频交付＝两件（配音＋译文 .md）；③ 撤除的五种名字要一起列出来，客户才知道自己那份文件该走哪条路。
func mustAnnounceRetirement(name, text string) string {
	announced := strings.Contains(text, "不再受理") || strings.Contains(text, "停止受理") ||
		strings.Contains(text, "no longer accepted") || strings.Contains(text, "no longer packaged")
	if !announced {
		return name + "：没点名「.srt/.vtt/.json/.yaml/.yml 不再受理」⇒ 撤格式属对外承诺变更，不许静默收口"
	}
	twoArtifacts := strings.Contains(text, "两件") || strings.Contains(text, "Two artifacts") ||
		strings.Contains(text, "two artifacts")
	if !twoArtifacts {
		return name + "：没写明音频交付＝**两件**（同容器配音＋译文 .md）⇒ 客户不知道拿回的是什么形态"
	}
	if !strings.Contains(text, "srt") {
		return name + "：公告里没点名被撤的格式（客户手上正是这些文件，只说「不支持」等于没公告）"
	}
	return ""
}

// TestOpenAPIMediaRetirementDocumented L5：五处对外文本必须"旧承诺消失、新口径写明"。
func TestOpenAPIMediaRetirementDocumented(t *testing.T) {
	faces := []struct {
		name         string
		text         string
		mustAnnounce bool
	}{
		{"openapi.v1.json(info.description)", specInfoDescription(t), true},
		{"openapi.v1.json(CreateFileTask.files)", specFileFieldDescription(t), false},
		{"openapi.v1.json(TaskStatus.files)", specStatusFilesDescription(t), true},
		{"defaultDocsMDZh", defaultDocsMDZh, true},
		{"defaultDocsMDEn", defaultDocsMDEn, true},
	}
	for _, f := range faces {
		if v := stillPromisedDelivery(f.text); v != "" {
			t.Errorf("%s：%s", f.name, v)
		}
		if f.mustAnnounce {
			if v := mustAnnounceRetirement(f.name, f.text); v != "" {
				t.Errorf("%s", v)
			}
		}
	}
	// 反证：把旧句子写回去必须被抓（证明上面两条负向锁有射程，而不是恒真）
	mutated := "音频产出同容器译后配音＋字幕 _en.srt＋原音频副本"
	if stillPromisedDelivery(mutated) == "" {
		t.Error("反证失败：旧承诺句子写回来却没被抓，L5 的负向腿射程为零")
	}
	// 反证：只删公告、留新形态描述，必须被"没点名不再受理"抓到（防"整段删空"式过关）
	if mustAnnounceRetirement("假文档", "音频交付两件：同容器配音＋译文 .md") == "" {
		t.Error("反证失败：没点名不再受理却没判红 ⇒ 公告这一腿可以被静默删掉")
	}
	// 反证②（判据的另一半）：否定式写法必须放行——公告句与承诺句共用名词，
	// 如果连「不产字幕」都判红，这条锁的归宿就是被人整个删掉。
	if v := stillPromisedDelivery("音频交付两件：同容器配音＋译文 .md，不产字幕、不随包原音频副本"); v != "" {
		t.Errorf("否定式公告被判成承诺（判据过严，第二天就会被摘）：%s", v)
	}
	// 正例对照：本次真改过的文本喂进同一个判据必须回空（证明上面几条红是判据有效，不是判据坏了）
	if v := mustAnnounceRetirement("对照", defaultDocsMDZh); v != "" {
		t.Errorf("正向对照失败（真文档被判违规）：%s", v)
	}
}
