// ============ file_no_srt_gate_test.go · 职责说明 ============
// 组1 C3/C5 的死代码负向锁（★ 2026-10-31 补，方案 §2.3 点名的 TestNoSrt）。
//
// 两档撤销之后，这些符号**不应该再以任何形式出现在生产代码里**：
//
//	· 字幕链：WriteTranslationSrt / AudioCues / rememberAsrCues
//	· 对照表链：WriteComparisonXlsx / isComparisonTableFormat
//	· 旧提取分支：extractSubtitle / extractJSON / extractYAML
//
// 判据按**代码行**扫（先剥整行注释）：说明注释里写着这些名字是"记录为什么删"，
// 拿裸串判会把注释当实现（本仓反复踩过的那一族）。同时带射程自证与反证：
// 扫不到现役入口即判"闸门失明"，喂回一个死符号必须被抓到。
// =============================================
package fileproc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// deadSymbols 两档撤销后不许复活的名字（判据形态带调用/声明特征，防子串误伤）。
var deadSymbols = []string{
	"WriteTranslationSrt",
	"AudioCues(",
	"rememberAsrCues",
	"WriteComparisonXlsx",
	"isComparisonTableFormat",
	"extractSubtitle(",
	"extractJSON(",
	"extractYAML(",
}

// TestNoSrtDeadSymbolsGone 扫 fileproc 与 engine 两个包的生产代码（不含 _test.go）。
func TestNoSrtDeadSymbolsGone(t *testing.T) {
	dirs := []string{".", filepath.Join("..", "engine")}
	scanned := 0
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("读目录 %s 失败（静态锁必须以生产源码为靶子）: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("读 %s 失败: %v", name, err)
			}
			code := stripGoBareComments(string(b))
			scanned++
			for _, dead := range deadSymbols {
				if strings.Contains(code, dead) {
					t.Fatalf("%s/%s 的代码行里仍有 %s：该链路已随组1 撤销，任何读者都是回潮", dir, name, dead)
				}
			}
		}
	}
	// 射程自证：现役入口必须还能扫到（扫不到＝读到空文本，上面的负向恒真空转）。
	if scanned < 20 {
		t.Fatalf("射程自证失败：只扫到 %d 个生产文件，判据射程可疑", scanned)
	}
	// 反证：把死符号喂回同一个判据必须被抓到。
	mutated := stripGoBareComments("package x\nfunc WriteTranslationSrt() {}\n")
	caught := false
	for _, dead := range deadSymbols {
		if strings.Contains(mutated, dead) {
			caught = true
		}
	}
	if !caught {
		t.Fatal("反证失败：死符号喂回后没被抓到，这条锁射程为零")
	}
}
