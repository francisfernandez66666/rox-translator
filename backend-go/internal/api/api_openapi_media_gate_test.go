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
