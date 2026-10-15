// ============ cmd/capability-smoke · 职责说明 ============
// 部署排障工具：对**图片（OCR）与音频（ASR）**两条能力腿实跑一次产品提取入口
// （`fileproc.ExtractTexts`），将「就绪位」与「真跑通」分开取证。
//
// 为什么必须有这个探针（★★ 与 cmd/fpdprobe 同一条理由，不是锦上添花）：
//
//	`/api/health` 的 `ocr_ready`/`asr_ready` 只回答「依赖装了没、权重落盘没」，
//	它证明不了 Go→子进程→stdout JSON→提取器 这一整条腿。本批现网实证：
//	sherpa_onnx 1.13.8 **根本没有 read_wave**（只有 write_wave），
//	而 asr_transcribe.py 拿它读 wav ⇒ probe 回 OK、`asr_ready=true`、
//	界面照常、日志零红，只有真提一单音频才退 ERR:Decode——
//	正是 AGENTS §一·12 那条「有自动降级的链路，产物能打开不算验收」。
//	⇒ 现网复验一律以本探针的**逐件读数**为准，健康位只当辅证。
//
// 用法（在部署机上跑，脚本与资产都取现网那一份）：
//
//	OCR_SCRIPT=/opt/translator/bin/ocr_recognize.py \
//	ASR_SCRIPT=/opt/translator/bin/asr_transcribe.py \
//	  ./capability-smoke <图片/音频路径> [更多文件…]
//
// ★ 为什么脚本路径要显式给：`ocrScriptPath()`/`asrScriptPath()` 默认取**可执行文件同目录**
//
//	（那是二进制部署约定），而 `go run` 或把探针丢在 /tmp 时 os.Args[0] 的目录里没有脚本，
//	表现就是「OCR 脚本缺失」——那是探针自己的接线错，不是现网缺件，别据此判现网坏。
//	环境变量覆盖是 fileproc 既有约定（同 ANYDOC_SCRIPT），不动产品代码。
//
// 判绿口径（只认这些读数）：每件文件一行 `OK <path> lines=N first="…"`，
// 末行 `CAPSMOKE_ALL_OK=1`；任一件失败即逐件打 `BAD <path> err=…` 并退非零。
// 零件输入即判红（不许把「没跑到」读成「通过」，AGENTS §三 那条）。
// =============================================
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"translator/internal/fileproc"
)

// main 先报一次本机能力位（辅证），再逐件实跑提取（主证）。
// 参数：os.Args[1:] = 待提取的图片/音频路径；返回：无（结果走 stdout，失败退非零）。
func main() {
	h := fileproc.CheckHealth()
	fmt.Printf("python=%s ocr_ready=%v asr_ready=%v tts_ready=%v anydoc_ready=%v\n",
		h.PythonPath, h.OcrAvail, h.AsrAvail, h.TtsAvail, h.AnydocAvail)
	// 脚本与资产的定位读数一并打出：现网最常见的坏法是「脚本在 A 目录、资产在 B 目录」，
	// 这两个读数能把「配置错」和「链路线坏」分开，不必再靠人猜。
	fmt.Printf("ocr_script=%s asr_script=%s\n", os.Getenv(fileproc.OcrScriptEnv), os.Getenv(fileproc.AsrScriptEnv))

	paths := os.Args[1:]
	if len(paths) == 0 {
		fmt.Println("CAPSMOKE_ALL_OK=0 用法：capability-smoke <图片/音频路径> [更多…]（零件输入不算通过）")
		os.Exit(2)
	}

	failed := 0
	for _, p := range paths {
		start := time.Now()
		texts, err := fileproc.ExtractTexts(p)
		cost := time.Since(start).Round(10 * time.Millisecond)
		if err != nil {
			failed++
			fmt.Printf("BAD %s cost=%s err=%v\n", p, cost, err)
			continue
		}
		if len(texts) == 0 {
			// 「跑通了但零行」同样是失败：提取器返回空数组时工单会翻成空译文，
			// 界面侧看起来却像成功——所以这一档必须判红，不许只报 lines=0 就放行。
			failed++
			fmt.Printf("BAD %s cost=%s err=提取到零行（识别链通但没有任何文本）\n", p, cost)
			continue
		}
		first := strings.TrimSpace(texts[0])
		if len([]rune(first)) > 60 {
			first = string([]rune(first)[:60]) + "…"
		}
		fmt.Printf("OK %s cost=%s lines=%d first=%q\n", p, cost, len(texts), first)
	}

	if failed > 0 {
		fmt.Printf("CAPSMOKE_ALL_OK=0 bad=%d\n", failed)
		os.Exit(1)
	}
	fmt.Println("CAPSMOKE_ALL_OK=1")
}
