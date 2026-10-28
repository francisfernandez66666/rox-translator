// ============ 本文件职责中文说明 ============
// 图片件「单行源文 → 单行译文」收敛闸门（★ 2026-10-29 用户实证）。
//
// 现网形态：某段源文是单行 `manual`，模型回了 `"manual  \nManual"`，原位回填把两行画进**同一个框**
// ⇒ 客户看到同一个词被翻了两遍（"manual / Manual"），.md 里也成了两行。
// 图片件每个框按构造就是一行，所以单行源文的译文必须收敛成一行；多行源文（扫描件 PDF 段落）不动。
//
// 两条腿：
//
//	L1 行为腿：四档逐字点名（回声双行取末行／多行合一行／单行原样／多行源文不动／空白回落）。
//	L2 接线腿：`HandleFile` 的 image 分支必须真调用它（判据级代替不了守卫级——函数写对没人调＝白修）。
//
// ============================================
package engine

import (
	"os"
	"strings"
	"testing"
)

// TestNormalizeImageTranslations 四档行为逐字点名（回声双行取末行／多行合一行／
// 单行原样／多行源文不动／全空白回落），现网 manual/Manual 是第一条的真实形态。
func TestNormalizeImageTranslations(t *testing.T) {
	cases := []struct {
		name string
		src  string
		in   string
		want string
	}{
		{"回声双行取末行（现网 manual/Manual）", "manual", "manual  \nManual", "Manual"},
		{"真两行合一行", "hello", "hola\nmundo", "hola mundo"},
		{"单行原样（含首尾空白）", "hi", "  hola  ", "hola"},
		{"多行源文不动", "line1\nline2", "a\nb", "a\nb"},
		{"全空白回落原值", "x", " \n ", " \n "},
		{"三行回声取末行", "dragon", "dragon\ndragon\nDragon", "Dragon"},
	}
	for _, c := range cases {
		got := normalizeImageTranslations(map[string]string{c.src: c.in})[c.src]
		if got != c.want {
			t.Errorf("%s：入 %q 期望 %q 实得 %q", c.name, c.in, c.want, got)
		}
	}
}

// TestNormalizeImageTranslationsWired 接线腿：图片链必须真调用收敛函数（写对没人调＝白修）。
func TestNormalizeImageTranslationsWired(t *testing.T) {
	b, err := os.ReadFile("file.go")
	if err != nil {
		t.Fatalf("读不到 file.go：%v", err)
	}
	if !strings.Contains(string(b), "tr = normalizeImageTranslations(tr)") {
		t.Fatalf("图片链没接上 normalizeImageTranslations（函数写对但没人调＝白修）")
	}
}
