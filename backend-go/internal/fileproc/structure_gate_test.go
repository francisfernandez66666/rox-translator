package fileproc

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestParseStructureProbe 两态解析 + 版本读数（L50 的行为级判据）。
func TestParseStructureProbe(t *testing.T) {
	p := parseStructureProbe("OK: version=3.2.0 models=2\n")
	if !p.OK || p.Version != "3.2.0" || p.Models != 2 {
		t.Fatalf("OK 行解析不对：%+v", p)
	}
	p = parseStructureProbe("MISSING: paddleocr 未安装（结构腿可选，缺失不影响 OCR/翻译）\n")
	if p.OK || !strings.Contains(p.Reason, "paddleocr") {
		t.Fatalf("MISSING 行解析不对：%+v", p)
	}
	if p = parseStructureProbe("随便一行没协议的输出"); p.OK || p.Reason == "" {
		t.Fatalf("畸形输出必须判未就绪：%+v", p)
	}
}

// TestParseStructureJSONRejectsTextKeys 结构腿只许带几何：任何 text/rec_text 键即判坏（L51）。
func TestParseStructureJSONRejectsTextKeys(t *testing.T) {
	good := `{"blocks":[{"label":"table","box":[0,0,10,10],"order":0}],`
	bad := `{"blocks":[{"label":"table","box":[0,0,10,10],"order":0,"text":"识别结果"}],`
	tail := `"tables":[]}`
	if _, err := parseStructureJSON([]byte(good + tail)); err != nil {
		t.Fatalf("合法结构应解析成功：%v", err)
	}
	doc, err := parseStructureJSON([]byte(bad + tail))
	if err != ErrStructureBadInput {
		t.Fatalf("带 text 键必须判 BadInput，得 %v（doc=%+v）", err, doc)
	}
	// 嵌套在 table/cell 里也一样。
	nested := `{"blocks":[],"tables":[{"box":[0,0,10,10],"rows":1,"cols":1,`
	nested += `"cells":[{"r":0,"c":0,"rowspan":1,"colspan":1,"box":[0,0,5,5],"rec_text":"x"}]}]}`
	if _, err := parseStructureJSON([]byte(nested)); err != ErrStructureBadInput {
		t.Fatalf("嵌套 text 键必须判 BadInput，得 %v", err)
	}
}

// TestParseStructureJSONGeometry 几何映射与坏行跳过（block/cell 都要求 ≥4 值）。
func TestParseStructureJSONGeometry(t *testing.T) {
	raw := `{"blocks":[{"label":"table","box":[1,2,3,4],"order":2},{"label":"para","box":[1,2]}],`
	raw += `"tables":[{"box":[1,2,3,4],"rows":2,"cols":3,"html":"<table></table>",`
	raw += `"cells":[{"r":0,"c":0,"rowspan":1,"colspan":2,"box":[1,2,3,4]}]}]}`
	doc, err := parseStructureJSON([]byte(raw))
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if len(doc.Blocks) != 1 || doc.Blocks[0].Label != "table" || doc.Blocks[0].Order != 2 {
		t.Fatalf("块映射不对：%+v", doc.Blocks)
	}
	if len(doc.Tables) != 1 || doc.Tables[0].Rows != 2 || len(doc.Tables[0].Cells) != 1 {
		t.Fatalf("表格映射不对：%+v", doc.Tables)
	}
}

// TestStructureDegradation 默认关；打开但脚本缺失 ⇒ Unavailable（L55：降级不判失败）。
func TestStructureDegradation(t *testing.T) {
	t.Setenv("OCR_STRUCTURE", "")
	if StructureEnabled() {
		t.Fatal("未设置时必须默认关（spike 未全绿，不许在现网自动启用）")
	}
	if _, err := DetectStructure(context.Background(), "/tmp/x.png"); err != ErrStructureUnavailable {
		t.Fatalf("关闭态应回 Unavailable，得 %v", err)
	}
	t.Setenv("OCR_STRUCTURE", "on")
	t.Setenv(StructureScriptEnv, "/tmp/不存在的-structure_v3.py")
	if _, err := DetectStructure(context.Background(), "/tmp/x.png"); err != ErrStructureUnavailable {
		t.Fatalf("脚本缺失应回 Unavailable，得 %v", err)
	}
	// 骨架阶段：脚本与资产都在也只会回 Unavailable（明确报错，不许静默空结构）。
	if err := structureErrName(ErrStructureUnavailable); err != "unavailable" {
		t.Fatalf("档位名必须逐字为 unavailable，得 %q", err)
	}
}

// TestStructureBudgetIndependent 结构预算与 OCR 整件预算互不占用（L56）。
func TestStructureBudgetIndependent(t *testing.T) {
	t.Setenv("OCR_TIMEOUT_SEC", "7")
	t.Setenv("STRUCTURE_TIMEOUT_SEC", "3")
	if got := ocrTimeout(); got != 7*time.Second {
		t.Fatalf("OCR 预算被串改：%v", got)
	}
	if got := structureTimeout(); got != 3*time.Second {
		t.Fatalf("结构预算读错：%v", got)
	}
	t.Setenv("STRUCTURE_TIMEOUT_SEC", "")
	if got := structureTimeout(); got != 20*time.Second {
		t.Fatalf("默认结构预算应为 20s，得 %v", got)
	}
}

// TestAssignBlocksConservation 行数守恒：未归属必须 -1，且与输入等长（L52）。
func TestAssignBlocksConservation(t *testing.T) {
	lines := []OcrLine{
		{Text: "a", Box: []float64{10, 10, 20, 20}},
		{Text: "b", Box: []float64{100, 200, 120, 220}}, // 不在任何块内
	}
	doc := StructureDoc{Blocks: []StructureBlock{{Label: "table", Box: [4]float64{0, 0, 50, 50}}}}
	assign, conflict := AssignBlocks(lines, doc)
	if len(assign) != len(lines) {
		t.Fatalf("归属数组长度不守恒：%d != %d", len(assign), len(lines))
	}
	if assign[0] != 0 || assign[1] != -1 {
		t.Fatalf("归属不对：%v", assign)
	}
	if conflict != 0 {
		t.Fatalf("无重叠块时不该有冲突：%d", conflict)
	}
}

// TestStructureScriptProbeWithRealPython 用真实 python 跑一次 probe（无 paddle ⇒ MISSING 两态之一）。
// 本机/CI 都装了 python；没装则跳过（该腿不是能力就绪判据，只是"脚本能被解释器执行"）。
func TestStructureScriptProbeWithRealPython(t *testing.T) {
	py := pyBin()
	if py == "" {
		t.Skip("没有可用的 python 解释器")
	}
	if _, err := os.Stat("structure_v3.py"); err != nil {
		t.Fatalf("structure_v3.py 必须随仓库提交：%v", err)
	}
	p := ProbeStructure(context.Background())
	// 两种合法态：MISSING（本机无 paddleocr）或 OK（部署机）。
	if !p.OK && p.Reason == "" {
		t.Fatalf("probe 未就绪时必须带原因（不许空原因）：%+v", p)
	}
}
