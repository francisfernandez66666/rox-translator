// ============ image_hygiene_order_gate_test.go · 职责说明 ============
// 图片链「交付卫生腿必须排在末段补译之后」的顺序锁（2026-10-29 第十一轮）。
//
// 缺陷形态（真踩）：`applySegmentGates` / `deliveryTranslations` / `normalizeImageTranslations`
// 三腿都排在 `retryImageSegments` 之前；而补译腿把结果**直接写回 tr** ⇒ 补译填出来的那几行
// 绕过了全部卫生腿（术语不覆盖、`manual→handbook` 不被拉回、`manual\nManual` 不被收敛）。
//
// 为什么用静态顺序锁而不是纯行为腿：补译腿要真拨模型（或可注入的假上游），
// 而「两个调用点的先后」这一层直接问源码就能判；两层一起写才既抓「没接线」又抓「接错顺序」。
//
// 射程：只判**图片分支内部**（`if writebackDelivery(ext) == "image" {` 到该分支的 `continue }`）——
// 全文 LastIndex 会被后面音频/普通交付分支的同名调用骗过（本批首跑真踩：delivery 在 2141 行还有一次）。
// =============================================
package engine

import (
	"os"
	"strings"
	"testing"
)

const (
	hygieneRetryAnchor  = "retryImageSegments(ctx, texts, tr, lc)"
	hygieneDelAnchor    = "deliveryTranslations(lc,"
	hygieneNormAnchor   = "normalizeImageTranslations("
	hygieneBranchHeader = `if writebackDelivery(ext) == "image" {`
	hygieneBranchTail   = "\n\t\t\t\tcontinue\n\t\t\t}"
)

// imageBranch 抠出图片交付分支的源码；找不到头/尾即返回 ("", false)（判据看不见实现时判红）。
// 取**最后一次**出现：`if writebackDelivery(ext) == "image"` 在抽取段（:660）也有一处，
// 取第一次会把「抽取分支」当成「交付分支」，retry 调用点自然不在里面（本批首跑真踩）。
func imageBranch(src string) (string, bool) {
	i := strings.LastIndex(src, hygieneBranchHeader)
	if i < 0 {
		return "", false
	}
	j := strings.Index(src[i:], hygieneBranchTail)
	if j < 0 {
		return "", false
	}
	return src[i : i+j+len(hygieneBranchTail)], true
}

// imageHygieneOrderProblems 返回图片分支内卫生腿的顺序问题（空＝合规）。
// 判据取分支内**最后一次**调用：只要最后一次卫生腿在 retry 之前，就说明补译段没有再过一遍。
func imageHygieneOrderProblems(branch string) []string {
	var out []string
	retry := strings.LastIndex(branch, hygieneRetryAnchor)
	if retry < 0 {
		return []string{"图片分支里找不到 retryImageSegments 调用点 ⇒ 这条锁没有输入"}
	}
	if del := strings.LastIndex(branch, hygieneDelAnchor); del < retry {
		out = append(out, "最后一次 deliveryTranslations(lc, …) 在 retryImageSegments 之前 ⇒ 补译段绕过标识符逐字/拒译剥离")
	}
	if norm := strings.LastIndex(branch, hygieneNormAnchor); norm < retry {
		out = append(out, "最后一次 normalizeImageTranslations 在 retryImageSegments 之前 ⇒ 补译段的两行译文不会被收敛")
	}
	return out
}

// TestImageHygieneRunsAfterRetry 交付形态：图片分支的卫生腿必须在补译之后。
func TestImageHygieneRunsAfterRetry(t *testing.T) {
	raw, err := os.ReadFile("file.go")
	if err != nil {
		t.Fatalf("读 file.go 失败：%v", err)
	}
	branch, ok := imageBranch(string(raw))
	if !ok {
		t.Fatalf("抠不出图片交付分支（头/尾锚点漂了）⇒ 这条锁没有输入")
	}
	if ps := imageHygieneOrderProblems(branch); len(ps) > 0 {
		t.Fatalf("图片链交付卫生链顺序不对：%s", strings.Join(ps, "；"))
	}
	// 反证一：改造前的顺序必须被同一判据判死。
	old := "tr = normalizeImageTranslations(deliveryTranslations(lc, tr))\n" +
		"unresolved, imgUpstreamDead := e.retryImageSegments(ctx, texts, tr, lc)\n"
	if ps := imageHygieneOrderProblems(old); len(ps) == 0 {
		t.Fatalf("判据自证不过：改造前的顺序竟然判绿")
	}
	// 反证二：正确顺序必须判绿（防判据只会判红）。
	good := "unresolved, _ := e.retryImageSegments(ctx, texts, tr, lc)\n" +
		"tr = normalizeImageTranslations(deliveryTranslations(lc, tr))\n"
	if ps := imageHygieneOrderProblems(good); len(ps) > 0 {
		t.Fatalf("判据自证不过：正确顺序被判红：%s", strings.Join(ps, "；"))
	}
	// 反证三：retry 调用点被删（尺子丢了）必须判死。
	if ps := imageHygieneOrderProblems("tr = deliveryTranslations(lc, tr)\n"); len(ps) == 0 {
		t.Fatalf("判据自证不过：缺 retry 调用点却判绿")
	}
}

// TestHygieneCompositionCleansRetryOutput R2（组合级）：末段补译写回的坏形态必须被重跑的两腿收敛。
//
// 射程说明：端到端图片分支要真 OCR 资产，单测里跑不动；这条锁的是"那两腿对补译产物有效"，
// 接线由 TestImageHygieneRunsAfterRetry 的静态顺序锁负责（两层一起才覆盖 R1+R2）。
func TestHygieneCompositionCleansRetryOutput(t *testing.T) {
	tr := map[string]string{
		"manual": "handbook",       // 英文键被补译成同义词
		"中文标签":   "manual\nManual", // 补译回两行
		"普通":     "Hello",
	}
	out := normalizeImageTranslations(deliveryTranslations("en", tr))
	if out["manual"] != "manual" {
		t.Fatalf("补译写回的英文键没被逐字恢复：%q", out["manual"])
	}
	if strings.Contains(out["中文标签"], "\n") {
		t.Fatalf("补译写回的两行没被收敛：%q", out["中文标签"])
	}
}
