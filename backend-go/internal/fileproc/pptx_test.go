package fileproc

import (
	"regexp"
	"strings"
	"testing"
)

func TestTranslatePptxXML(t *testing.T) {
	slide := `<p:sld><p:spTree>
<p:sp><p:spPr><a:xfrm><a:off x="457200" y="457200"/><a:ext cx="3000000" cy="1000000"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr><p:txBody><a:bodyPr/><a:p><a:pPr><a:defRPr sz="1800" lang="zh-CN"/></a:pPr><a:r><a:rPr lang="zh-CN" sz="1800"/><a:t>刹车系统故障指示灯</a:t></a:r></a:p><a:p><a:r><a:rPr lang="zh-CN" sz="1800"/><a:t>请立即联系售后服务</a:t></a:r></a:p></p:txBody></p:sp>
</p:spTree></p:sld>`

	translations := map[string]string{
		"刹车系统故障指示灯": "Brake system malfunction indicator light is now illuminated on the dashboard",
		"请立即联系售后服务": "Please contact the after-sales service department immediately",
	}

	out := translatePptxXML([]byte(slide), translations)
	s := string(out)

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

func TestTranslatePptxCell(t *testing.T) {
	cell := `<a:tc><a:txBody><a:bodyPr/><a:p><a:pPr><a:defRPr sz="1400" lang="zh-CN"/></a:pPr><a:r><a:rPr lang="zh-CN" sz="1400"/><a:t>刹车系统故障</a:t></a:r></a:p></a:txBody></a:tc>`
	translations := map[string]string{
		"刹车系统故障": "A very long translation of brake system malfunction",
	}
	out := translatePptxCell(cell, translations)
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
