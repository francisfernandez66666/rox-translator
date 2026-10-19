// ============ 本文件职责中文说明 ============
// PPTX 提取与写回的单元测试：段落键提取、译文回写与 XML 实体对齐回归。
// ============================================
package fileproc

import (
	"archive/zip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestTranslatePptxXML 验证 translatePptxXML 写入译文、设置 autofit=shrink 并自动缩小字号。
func TestTranslatePptxXML(t *testing.T) {
	slide := `<p:sld><p:spTree>
<p:sp><p:spPr><a:xfrm><a:off x="457200" y="457200"/><a:ext cx="3000000" cy="1000000"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr><p:txBody><a:bodyPr/><a:p><a:pPr><a:defRPr sz="1800" lang="zh-CN"/></a:pPr><a:r><a:rPr lang="zh-CN" sz="1800"/><a:t>刹车系统故障指示灯</a:t></a:r></a:p><a:p><a:r><a:rPr lang="zh-CN" sz="1800"/><a:t>请立即联系售后服务</a:t></a:r></a:p></p:txBody></p:sp>
</p:spTree></p:sld>`

	translations := map[string]string{
		"刹车系统故障指示灯": "Brake system malfunction indicator light is now illuminated on the dashboard",
		"请立即联系售后服务": "Please contact the after-sales service department immediately",
	}

	out, hits := translatePptxXML([]byte(slide), translations)
	s := string(out)
	if hits == 0 {
		t.Fatal("G17：命中段数不该为 0（翻译确实写入了）")
	}

	// 1. 文本替换成功
	if !strings.Contains(s, "Brake system malfunction indicator light") {
		t.Fatal("译文未写入")
	}
	if !strings.Contains(s, "after-sales service department") {
		t.Fatal("第二段译文未写入")
	}
	// 2. autofit=shrink 已设置
	if !strings.Contains(s, `autofit="shrink"`) {
		t.Fatal("autofit=shrink 未设置")
	}
	// 3. 字号已缩小（原文 1800，译文变长应缩小）
	m := regexp.MustCompile(`<a:rPr lang="zh-CN" sz="(\d+)"`).FindAllStringSubmatch(s, -1)
	if len(m) == 0 {
		t.Fatal("未找到 rPr 字号")
	}
	fit := m[0][1]
	t.Logf("适配后字号: %s (原文1800)", fit)
	if fit == "1800" {
		t.Fatal("字号未缩小")
	}
	// 4. XML 平衡
	if strings.Count(s, "<a:t") != strings.Count(s, "</a:t>") {
		t.Fatal("a:t 标签不平衡")
	}
}

// TestTranslatePptxCell 验证表格单元格译文写入与译文变长时的字号缩小。
func TestTranslatePptxCell(t *testing.T) {
	cell := `<a:tc><a:txBody><a:bodyPr/><a:p><a:pPr><a:defRPr sz="1400" lang="zh-CN"/></a:pPr><a:r><a:rPr lang="zh-CN" sz="1400"/><a:t>刹车系统故障</a:t></a:r></a:p></a:txBody></a:tc>`
	translations := map[string]string{
		"刹车系统故障": "A very long translation of brake system malfunction",
	}
	out, hits := translatePptxCell(cell, translations)
	if hits == 0 {
		t.Fatal("G17：单元格命中段数不该为 0")
	}
	if !strings.Contains(out, "A very long translation") {
		t.Fatal("单元格译文未写入")
	}
	m := regexp.MustCompile(`<a:rPr lang="zh-CN" sz="(\d+)"`).FindStringSubmatch(out)
	if m == nil {
		t.Fatal("无 run 字号")
	}
	t.Logf("单元格字号: %s (原文1400)", m[1])
	if m[1] == "1400" {
		t.Fatal("单元格字号未缩小")
	}
}

// makeTestPptx 造一个最小 pptx（只含 ppt/slides/slide1.xml），返回路径。
func makeTestPptx(t *testing.T, dir, slide string) string {
	t.Helper()
	p := filepath.Join(dir, "in.pptx")
	f, err := os.Create(p)
	if err != nil {
		t.Fatalf("创建测试 pptx 失败: %v", err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("ppt/slides/slide1.xml")
	if err != nil {
		t.Fatalf("创建 slide1.xml 失败: %v", err)
	}
	if _, err := w.Write([]byte(slide)); err != nil {
		t.Fatalf("写 slide1.xml 失败: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("收敛测试 zip 失败: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("关闭测试 pptx 失败: %v", err)
	}
	return p
}

// pptxSlideText 读回产物的 slide1.xml。
func pptxSlideText(t *testing.T, path string) string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("打开产物失败: %v", err)
	}
	defer zr.Close()
	data, err := readZipEntry(zr, "ppt/slides/slide1.xml")
	if err != nil {
		t.Fatalf("读产物 slide1.xml 失败: %v", err)
	}
	return string(data)
}

// TestApplyPptxHitCount 命中与零命中两档各判一次（G17）。
func TestApplyPptxHitCount(t *testing.T) {
	const slide = `<p:sld><p:spTree><p:sp><p:spPr><a:xfrm><a:off x="457200" y="457200"/><a:ext cx="3000000" cy="1000000"/></a:xfrm></p:spPr><p:txBody><a:bodyPr/><a:p><a:r><a:rPr lang="zh-CN" sz="1800"/><a:t>刹车系统</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:sld>`
	dir := t.TempDir()
	src := makeTestPptx(t, dir, slide)

	out := filepath.Join(dir, "hit.pptx")
	hits, err := ApplyPptx(src, out, map[string]string{"刹车系统": "Brake system"})
	if err != nil {
		t.Fatalf("命中档不该出错: %v", err)
	}
	if hits == 0 {
		t.Fatal("命中段数不该为 0（翻译确实写入了）")
	}
	if body := pptxSlideText(t, out); !strings.Contains(body, "Brake system") {
		t.Fatalf("命中档段落未被替换: %s", body)
	}

	out2 := filepath.Join(dir, "miss.pptx")
	hits2, err2 := ApplyPptx(src, out2, map[string]string{"根本不存在的原文": "whatever"})
	if err2 != nil {
		t.Fatalf("零命中不是错误: %v", err2)
	}
	if hits2 != 0 {
		t.Fatalf("零命中应回 0，实得 %d ⇒ 未翻译件会被静默当成功交付", hits2)
	}
}
