package fileproc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// ============ 附：manifest / probe 同尺（结构权重台账） ============

// structManifest 权重台账形状：`assets/structure/manifest.json`。
// 字段名与 sync_fileproc_assets.sh 的核对口径同源（name 相对 assets/structure，sha256 小写十六进制）。
type structManifest struct {
	Files []struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
	} `json:"files"`
}

// verifyStructureManifest 校验台账：逐条存在 + 指纹等值 + 磁盘上**没有台账外的权重**。
// 三条缺一不可：只查"列出的都在"会让"多丢了一份权重"漏过去，而多出来的那份恰恰是新模型上线时最常见的漂移形态。
func verifyStructureManifest(root string) (checks int, err error) {
	raw, rerr := os.ReadFile(filepath.Join(root, "manifest.json"))
	if rerr != nil {
		return 0, fmt.Errorf("读台账失败: %w", rerr)
	}
	var m structManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0, fmt.Errorf("台账不是合法 JSON: %w", err)
	}
	if len(m.Files) == 0 {
		return 0, errors.New("台账里一条权重都没有（空台账会让所有漂移都通过）")
	}
	listed := map[string]bool{}
	for _, f := range m.Files {
		if f.Name == "" || strings.Contains(f.Name, "..") || filepath.IsAbs(f.Name) {
			return checks, fmt.Errorf("台账条目路径非法: %q", f.Name)
		}
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.Name)))
		if err != nil {
			return checks, fmt.Errorf("台账列出的权重不存在: %s（%v）", f.Name, err)
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); !strings.EqualFold(got, f.SHA256) {
			return checks, fmt.Errorf("权重指纹不符: %s（台账 %s，实际 %s）", f.Name, f.SHA256, got)
		}
		listed[f.Name] = true
		checks++
	}
	var onDisk []string
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(strings.ToLower(info.Name()), ".onnx") {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		onDisk = append(onDisk, filepath.ToSlash(rel))
		return nil
	})
	for _, p := range onDisk {
		if !listed[p] {
			return checks, fmt.Errorf("磁盘上有台账外的权重: %s（漏登记 ⇒ 换件后没人核得到它）", p)
		}
	}
	return checks, nil
}

// TestStructureManifestMatchesAssets 两态：
//   - 部署机形态（`assets/structure/manifest.json` 在位）⇒ 逐条核存在与指纹，且不许有台账外的权重；
//   - 仓库形态（权重不进 git ⇒ 无台账）⇒ 退回 **probe 同尺**：Go 侧的 `structureONNXCount()`
//     与脚本 probe 的 `models=` 必须给出同一个数。
//
// 为什么第二态也要有：两把尺子各数各的，表现是"Go 说 0 份 ⇒ engine 报 paddle、
// 脚本说 2 份 ⇒ probe 说 OK"，健康面于是同时出现 onnx 与 paddle 两个互斥读数。
//
// 台账校验函数本身配四枚夹具（绿/指纹不符/缺件/多出未登记件），否则"本机没有台账"这一态
// 会让整条锁恒绿——那正是 AGENTS 反复点名的"空转锁"。
func TestStructureManifestMatchesAssets(t *testing.T) {
	root := structureAssetsDir()
	if _, err := os.Stat(filepath.Join(root, "manifest.json")); err == nil {
		n, err := verifyStructureManifest(root)
		if err != nil {
			t.Fatalf("结构权重台账校验失败：%v", err)
		}
		if n == 0 {
			t.Fatal("台账校验通过但一条都没核 ⇒ 判据空转")
		}
		t.Logf("台账校验通过：%d 份权重（%s）", n, root)
	} else {
		// 仓库形态：没有台账是**正常**的（权重不进 git），但要证明"同尺"这条腿有射程。
		goCount := structureONNXCount()
		structWirePy(t)
		layoutDir, tableDir := structWireWeights(t)
		structWireEnv(t, structureScriptFile, layoutDir, tableDir)
		p := ProbeStructure(context.Background())
		// temp 权重不在脚本同级的 assets/structure 里 ⇒ 两边都应为 0（这本身即同尺的读数）。
		if got := countONNXUnder(layoutDir) + countONNXUnder(tableDir); got != 2 {
			t.Fatalf("夹具权重份数应为 2，实际 %d", got)
		}
		if goCount != 0 || p.Models != 0 {
			// 出现非零并不可疑（部署机上权重已落盘），但那时**两边必须相等**。
			if goCount != p.Models {
				t.Fatalf("Go 侧数到 %d 份、脚本 probe 数到 %d 份 ⇒ 两把尺子各数各的（健康面会同时出现 onnx 与 paddle）",
					goCount, p.Models)
			}
		}
	}

	// 台账校验函数的四枚夹具：绿 / 指纹不符 / 缺件 / 多出未登记件。
	t.Run("台账夹具", func(t *testing.T) {
		dir := t.TempDir()
		mk := func(name, content string) string {
			p := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatalf("建目录失败：%v", err)
			}
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				t.Fatalf("写文件失败：%v", err)
			}
			sum := sha256.Sum256([]byte(content))
			return hex.EncodeToString(sum[:])
		}
		sha := mk("layout/inference.onnx", "L")
		writeManifest := func(files string) {
			if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"files":[`+files+`]}`), 0o644); err != nil {
				t.Fatalf("写台账失败：%v", err)
			}
		}
		good := `{"name":"layout/inference.onnx","sha256":"` + sha + `"}`

		writeManifest(good)
		if n, err := verifyStructureManifest(dir); err != nil || n != 1 {
			t.Fatalf("正测应过（1 份），实际 n=%d err=%v", n, err)
		}
		// ① 指纹不符
		writeManifest(`{"name":"layout/inference.onnx","sha256":"` + strings.Repeat("0", 64) + `"}`)
		if _, err := verifyStructureManifest(dir); err == nil {
			t.Fatal("指纹不符必须判红")
		}
		// ② 缺件
		writeManifest(good + `,{"name":"table/inference.onnx","sha256":"` + sha + `"}`)
		if _, err := verifyStructureManifest(dir); err == nil {
			t.Fatal("台账列了但磁盘上没有，必须判红")
		}
		// ③ 台账外的权重
		mk("table/extra.onnx", "E")
		writeManifest(good)
		if _, err := verifyStructureManifest(dir); err == nil {
			t.Fatal("磁盘上有台账外的权重必须判红（漏登记＝换件后没人核得到它）")
		}
		// ④ 空台账
		writeManifest(``)
		if _, err := verifyStructureManifest(dir); err == nil {
			t.Fatal("空台账必须判红（否则所有漂移都通过）")
		}
	})
}

// countONNXUnder 现数某目录下的 .onnx 份数（只用于夹具自证：证明"2 份"这个前提真成立）。
func countONNXUnder(dir string) int {
	n := 0
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".onnx") {
			n++
		}
	}
	return n
}
