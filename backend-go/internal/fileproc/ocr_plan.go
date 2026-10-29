// ============ ocr_plan.go · 职责说明 ============
// OCR 三跳（① 识别 → ② 清洗 → ③ AI 裁决 → ④ 送翻）之间的**唯一载体**与**交接契约**。
//
// 为什么需要这一层（主方案 §9）：识别在 fileproc（提取期）、AI 裁决在 engine（有模型路由），
// 两侧生命周期不在同一个函数里；而 ExtractTexts 有三个调用方，改签名会同时动三处。
// 所以：
//   - 提取期把 OcrDoc 挂进本文件的**按路径在途队列**（与 ocr_image.go 的坐标缓存同形）；
//   - engine/api 取用（PendingOcrDoc）→ 跑 ③ → 回写 Final → CommitOcrDoc 重建映射并返回送翻文本；
//   - 从不跑 ③ 的调用方（单测、尚未接线的路径）仍拿到 ② 清洗后的文本与映射（兼容腿，见 CommitOcrDoc）。
//
// 三条不变式（改本文件前必读）：
//  1. Text 永远是 ① 的识别值（仅去首尾空白），Final 才允许被 ②/③ 改写 —— 事后可追溯"谁改了这个字"；
//  2. Final=="" 表示本行不交付（判非文字/重复框），它既不进 texts 也不进回填映射；
//  3. 队列里的文档带在途钉（pinned），写回结束后由调用方 ReleaseOcrDoc 解除 —— 否则高并发下会被挤掉。
package fileproc

import (
	"math"
	"strings"
	"sync"

	"translator/internal/textscript"
)

// OcrLine 是 OCR 一行在「清洗 → AI 裁决 → 送翻」三跳之间唯一的载体。
type OcrLine struct {
	Index       int       // 文档内 0 起行号（阅读顺序），日志与未译出清单都用它 +1 显示
	Text        string    // ① 的原始识别结果（仅去首尾空白；任何阶段都不许覆盖）
	Box         []float64 // 原图像素坐标，四值矩形或八值多边形（口径同 OcrBox.Box）
	Conf        float64   // 逐框识别置信度；ConfMissing=true 时无意义
	ConfMissing bool      // 逐框置信度读数缺失（老脚本/长度不符）

	Verdict string   // clean | uncertain | quarantine
	Reason  string   // 单值原因码（见 ocr_clean.go 的常量与优先序）
	Alts    []string // 候选集（可为空）：同列众数
	Final   string   // 最终交付文本；空 = 本行不交付

	Block  int // 结构块归属；-1 = 未归属（无结构数据的常态，不是错误）
	Column int // 列归属；-1 = 未聚出列
}

// OcrColumn ② 的列聚类结果，同时是 ③ 载荷里 column 字段的来源。
type OcrColumn struct {
	Index       int
	Lines       []int   // OcrLine 下标（阅读顺序）
	ModalText   string  // 该列出现次数最多的文本（众数）
	ModalCount  int     // 众数出现次数
	SecondCount int     // 第二众数出现次数（用于"两个势均力敌众数即放弃"）
	MedianConf  float64 // 该列置信度中位数（ConfMissing 行不参与）
	Valid       bool    // 中位置信度是否可用（该列一行读数都没有时为 false）
}

// OcrDoc 一份文件（图片或扫描件 PDF）的 OCR 全景。Version 由本文件维护，
// ③ 的回执必须带同版本号，防旧回执错配到新一轮文档。
type OcrDoc struct {
	SrcPath   string
	Lines     []OcrLine
	Columns   []OcrColumn
	Profile   textscript.Profile
	CleanedOK bool // ② 是否真跑过（false ⇒ 全部按 clean 处理，等于回旧行为）
	Version   int  // 当前固定为 1
	// Base ② 清洗完成那一刻的交付文本快照（去重、阅读顺序），此后**永不改写**。
	//
	// ★ 2026-10-29 立：它是 ④ 判「文本有没有被别的链路换掉」的唯一基准。
	// 旧形态拿"调用方手里的 texts"和"③ 改完的 Final"比 —— 而 ③ 的修正必然让两者不等，
	// 于是每一次采纳的修正都被判成 Stale 丢掉（现网实证：`manua` 换几版都修不掉，
	// 因为修好的字在 Commit 那一步被整份退回）。基准必须是「③ 之前」的读数。
	Base []string
}

// OcrDocVersion 当前结构版本（改 OcrLine/OcrDoc 的字段语义时必须 +1）。
const OcrDocVersion = 1

// Texts 返回按阅读顺序去重后的交付文本（Final 非空者）。
//
// 去重语义与 Extractor.add 严格一致（trim → 跳过空串 → 首次出现保留）：
// 回填映射的键与翻译表的键都由它派生，两处一旦不同形，就会"图上那行没换掉且不报错"。
func (d *OcrDoc) Texts() []string {
	if d == nil {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(d.Lines))
	for _, ln := range d.Lines {
		t := strings.TrimSpace(ln.Final)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// 在途队列（形制与语义照 ocr_image.go 的 imageBoxCache：路径键 + 在途钉 + 上限淘汰）。
const ocrDocCacheMax = 64

// ocrDocEntry 一条缓存：某文件路径 → 它这一版 OCR 全景。
type ocrDocEntry struct {
	path   string
	doc    *OcrDoc
	pinned bool // 已提取、尚未写回：在途条目不参与淘汰
}

// ocrDocMu 保护在途队列；ocrDocCache 是"路径 → OcrDoc"的有界切片（在途钉不参与淘汰）。
var (
	ocrDocMu    sync.Mutex
	ocrDocCache []ocrDocEntry
)

// RememberOcrDoc 记一份 OcrDoc 并钉住（标记在途）。同一路径重复提取覆盖旧值。
func RememberOcrDoc(path string, doc *OcrDoc) {
	if doc == nil {
		return
	}
	ocrDocMu.Lock()
	defer ocrDocMu.Unlock()
	for i := range ocrDocCache {
		if ocrDocCache[i].path == path {
			ocrDocCache[i].doc = doc
			ocrDocCache[i].pinned = true
			evictOcrDocsLocked()
			return
		}
	}
	ocrDocCache = append(ocrDocCache, ocrDocEntry{path: path, doc: doc, pinned: true})
	evictOcrDocsLocked()
}

// PendingOcrDoc 取回某路径这次 OCR 的清洗结果（只读，不解除在途标记）。
// 没有 OCR 发生过（普通 docx/带文本层 PDF）⇒ ok=false，调用方无需任何改动。
func PendingOcrDoc(path string) (*OcrDoc, bool) {
	ocrDocMu.Lock()
	defer ocrDocMu.Unlock()
	for i := len(ocrDocCache) - 1; i >= 0; i-- {
		if ocrDocCache[i].path == path {
			return ocrDocCache[i].doc, true
		}
	}
	return nil, false
}

// ReleaseOcrDoc 解除某路径的在途钉（写回阶段结束后调用）。
// 不调不会立即出错，只会让这条记录长期占着"不可淘汰"名额（同坐标缓存的资源释放口径）。
func ReleaseOcrDoc(path string) {
	ocrDocMu.Lock()
	defer ocrDocMu.Unlock()
	for i := range ocrDocCache {
		if ocrDocCache[i].path == path {
			ocrDocCache[i].pinned = false
			return
		}
	}
}

// evictOcrDocsLocked 超上限时优先淘汰未钉住的旧条目；全是钉住条目时淘汰最旧一条
// （牺牲那一条的代价只是它退回"仅译文 .md"，不是数据丢失）。调用方须持锁。
func evictOcrDocsLocked() {
	for len(ocrDocCache) > ocrDocCacheMax {
		idx := -1
		for i := range ocrDocCache {
			if !ocrDocCache[i].pinned {
				idx = i
				break
			}
		}
		if idx < 0 {
			idx = 0
		}
		ocrDocCache = append(ocrDocCache[:idx], ocrDocCache[idx+1:]...)
	}
}

// OcrCommitStats ④ 的读数（与 ②③ 的读数分开记，便于分辨"谁丢了行"）。
type OcrCommitStats struct {
	Lines     int  // 输入行数
	Delivered int  // 进入 texts 的行数（Final 非空）
	Dropped   int  // Final 为空（判非文字/重复框）的行数
	Mapping   bool // 是否成功写入行↔框映射
	Stale     bool // texts 与 OcrDoc 不同源，未重建映射（见 CommitOcrDoc 规则 5）
}

// BuildOcrDoc 把脚本输出（ocrResult）构造成 OcrDoc，并跑 ② 清洗。
//
// 容错口径：脚本输出缺 boxes/confs、或长度不齐时，对应行记 ConfMissing / Box=nil，
// 仍然交付文本（译文第一）；映射阶段会因 Box 缺失整体跳过，不让译文盖错位置。
func BuildOcrDoc(srcPath string, res ocrResult) (doc *OcrDoc) {
	rawLines := strings.Split(res.Text, "\n")
	lines := make([]OcrLine, 0, len(rawLines))
	haveBoxes := len(res.Boxes) == len(rawLines)
	haveConfs := len(res.Confs) == len(rawLines)
	for i, raw := range rawLines {
		text := strings.TrimSpace(raw)
		if text == "" {
			continue
		}
		ln := OcrLine{Index: len(lines), Text: text, Final: text, Block: -1, Column: -1}
		if haveBoxes {
			ln.Box = res.Boxes[i]
		}
		if haveConfs && !math.IsNaN(res.Confs[i]) {
			ln.Conf = res.Confs[i]
		} else {
			ln.ConfMissing = true
		}
		lines = append(lines, ln)
	}
	doc = &OcrDoc{SrcPath: srcPath, Lines: lines, Version: OcrDocVersion}
	// ② 清洗：任何异常都降级为"全 clean"，绝不让清洗层的 bug 把整单判死。
	func() {
		defer func() {
			if recover() != nil {
				doc.CleanedOK = false
				for i := range doc.Lines {
					doc.Lines[i].Verdict = "clean"
					doc.Lines[i].Reason = ""
					doc.Lines[i].Alts = nil
					doc.Lines[i].Final = doc.Lines[i].Text
				}
			}
		}()
		cleaned, prof, cols := CleanOcrDoc(doc.Lines)
		doc.Lines = cleaned
		doc.Profile = prof
		doc.Columns = cols
		doc.CleanedOK = true
	}()
	// ② 之后的交付文本快照：③（engine）拿到 doc 时就以它为基线判"有没有被别的链路换掉"。
	doc.Base = doc.Texts()
	return doc
}

// CommitOcrDoc 在 ③ 回写 Final 之后调用：按 Final 重建行↔框映射，返回去重后的送翻文本。
//
// 六条规则（与主方案 §9.3 逐条对应）：
//  1. 数据源只认 Final；Final=="" 的行既不进 texts 也不进映射；
//  2. 去重与 trim 与 Extractor.add 同一语义；texts 顺序＝阅读顺序首次出现；
//  3. 映射不去重：重复文本的多个框都该被画；
//  4. 任一行 Box 缺失/不成对 ⇒ 不写映射（回退"仅译文 .md"），但 texts 照常返回；
//  5. 不同源保护：currentTexts 非空且与「② 之后的快照 doc.Base」不等 ⇒ Stale，
//     不重建映射、不覆盖 texts。**基准绝不是 finalTexts**——那样 ③ 的每一次修正都会自己触发 Stale。
//  6. 幂等：同一路径重复调用等价（覆盖同一份映射）。
func CommitOcrDoc(path string, doc *OcrDoc, currentTexts []string) ([]string, OcrCommitStats) {
	st := OcrCommitStats{}
	if doc == nil {
		return currentTexts, st
	}
	st.Lines = len(doc.Lines)
	finalTexts := doc.Texts()
	// 行级计数：Delivered 数的是"进入映射的非空 Final 行"，与去重后的 texts 数不是一回事。
	for _, ln := range doc.Lines {
		if strings.TrimSpace(ln.Final) == "" {
			st.Dropped++
		} else {
			st.Delivered++
		}
	}
	// ★ 2026-10-29：基准取 doc.Base（② 完成时的快照）。调用方手里的 currentTexts 只有在
	//   **没有**被别的链路（PDF overlay 等）替换过时才等于 Base；不等即说明这次 commit 对不上
	//   当前这份 texts，按旧口径退回避让。Base 缺失（手工构造的 doc）时退回 finalTexts 比较，
	//   即"没有基线就不敢改"——宁可退回旧行为，也不拿猜测覆盖文本。
	base := doc.Base
	if base == nil {
		base = finalTexts
	}
	if currentTexts != nil && !sameStrings(currentTexts, base) {
		st.Stale = true
		return currentTexts, st
	}
	// 映射：每个非空 Final 行一条（不去重）。
	boxes := make([]OcrBox, 0, st.Delivered)
	for _, ln := range doc.Lines {
		t := strings.TrimSpace(ln.Final)
		if t == "" {
			continue
		}
		if len(ln.Box) < 4 || len(ln.Box)%2 != 0 {
			// 坐标缺失/不成对：宁可整体不写映射（回填退回仅 .md），也不把译文盖到错误的框上。
			return finalTexts, st
		}
		boxes = append(boxes, OcrBox{Text: t, Box: ln.Box})
	}
	if len(boxes) > 0 {
		RememberImageBoxes(path, boxes)
		st.Mapping = true
	}
	return finalTexts, st
}

// sameStrings 等长逐项比较（两处都是去重后的阅读顺序，顺序不同即视为不同源）。
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
