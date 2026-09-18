package fileproc

// pdf_overlay.go — PDF「原版式·原字体·原地替换」新链的 Go 侧驱动（2026-09-18）。
// 替代 pdf2docx→DOCX→LibreOffice 重建链：版式 100% 原件（表格线/图片/页眉不动），
// 原字体优先复用（内嵌子集可提取且覆盖译文字形时），换行宽度受单元格矢量线钳制。
// Python 侧：pdf_overlay.py（extract / apply / selftest），部署约定同 docx_translate.py。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// overlayScriptPath 定位 pdf_overlay.py（与可执行文件同目录）。
// 部署约定与 docx_translate.py 一致：脚本随二进制同目录发布（见 docxScriptPath）。
// 注意这条口径只适用于生产进程；go test 下 os.Args[0] 是临时测试二进制目录，取不到脚本，
// 故自检用例走的是「源码目录 + cmd.Dir=.」的相对路径（见 docx_table_layout_test.go）。
func overlayScriptPath() string {
	return filepath.Join(filepath.Dir(os.Args[0]), "pdf_overlay.py")
}

// runOverlayScript 调用 pdf_overlay.py 子命令（资源闸 + nice + 受控执行，同 docx 管线）。
// 参数：args=脚本子命令与路径参数；payload=喂给脚本 stdin 的 JSON（可为 nil）。
// 返回：stdout 字节（调用方自行 Unmarshal）；错误已附 stderr 尾部 4KB 上下文。
// 译文字段可达数百 KB 且含客户原文，一律走 stdin 而非 argv：避开 ARG_MAX 截断与
// 进程列表（ps）泄露，同时让 JSON 直接取自 stdout，不再猜「最后一个 '{'」。
func runOverlayScript(ctx context.Context, args []string, payload []byte) ([]byte, error) {
	bin, argv := wrapNice(pyBin(), append([]string{overlayScriptPath()}, args...))
	stdout, stderr, err := runSubprocess(ctx, fileprocTimeout(), bin, argv, payload)
	if err != nil {
		return stdout, fmt.Errorf("pdf_overlay %v 失败: %w\n%s", args, err, truncateTail(stderr))
	}
	return stdout, nil
}

// ExtractTextsPdfOverlay 原地替换链的文本提取：块+矢量栅格切分为段（与写回目标键完全一致）。
// 参数：ctx=子进程超时/取消上下文；pdfPath=输入 PDF 路径。
// 返回 (文本键列表, 错误)。无缓存产物（apply 直接在原 PDF 上做，无需 pdf2docx 缓存）。
// 键已按内容去重（相同片段只翻一次），故段数 ≠ PDF 物理段落数——ticket_segments 的
// seg_index 就是这里返回值的下标，两侧必须同源，不要在本函数里再加排序或过滤。
func ExtractTextsPdfOverlay(ctx context.Context, pdfPath string) ([]string, error) {
	out, err := runOverlayScript(ctx, []string{"extract", pdfPath}, nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Success bool     `json:"success"`
		Texts   []string `json:"texts"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	if !r.Success || len(r.Texts) == 0 {
		// 「提取成功但零段」也判为错误而非返回空切片：空列表会让上层以为文件本就无文本，
		// 直接把整单判成空交付；判错才能触发既有的「回退既有提取键」分支。
		return nil, fmt.Errorf("overlay extract 无文本")
	}
	return r.Texts, nil
}

// OverlayApplyStats 原地替换写回的命中统计（★ P0-7，2026-09-18）。
type OverlayApplyStats struct {
	Replaced  int // 成功嵌回的段数
	Overflow  int // 越出格子下缘仍写出的段数
	Requested int // 送进写回环节的译文段数（0=提取键全部没对上）
}

// ApplyTranslatedPdfOverlay 原地替换写回：redact 抹原文 + TextWriter 同位嵌回译文。
// 参数：ctx=子进程超时/取消上下文；outPath=输出 PDF；inPath=**原始** PDF；
//
//	translations=原文→译文映射（键=ExtractTextsPdfOverlay 输出）；lang=目标语言。
//
// 返回 (统计, 错误)。★ P0-7（2026-09-18）整改：旧实现丢弃 stdout、只看退出码，
// 「译文零命中」（提取键口径漂移时整页原样输出）也被当成成功，交付未翻译件并扣费。
// 现把 stdout 的 replaced/requested 计数纳入判定：
//   - requested>0 而 replaced==0 → error「零命中」；
//   - replaced < 60%·requested → error「低命中率」；
//     两种情形调用方都会走既有降级链（WriteTranslatedPDF 版式重建），不产出半翻译件。
//
// 键能对上是因为提取与写回走同一段切分代码（_merged_segments）；若上游用别的提取键
// （如 overlay 提取失败后回退 pdftotext），这里会整体不命中 → 由本函数判错兜住。
func ApplyTranslatedPdfOverlay(ctx context.Context, outPath, inPath string, translations map[string]string, lang string) (OverlayApplyStats, error) {
	payload, _ := json.Marshal(map[string]interface{}{
		"translations": translations,
	})
	out, err := runOverlayScript(ctx, []string{"apply", inPath, outPath, lang}, payload)
	if err != nil {
		return OverlayApplyStats{}, err
	}
	st := parseOverlayApplyOutput(string(out))
	if st.Requested > 0 && st.Replaced == 0 {
		return st, fmt.Errorf("pdf_overlay 译文零命中（requested=%d，提取键与写回键口径漂移）", st.Requested)
	}
	if st.Requested > 0 && st.Replaced*10 < st.Requested*6 {
		return st, fmt.Errorf("pdf_overlay 命中率过低（replaced=%d/requested=%d <60%%）", st.Replaced, st.Requested)
	}
	return st, nil
}

// parseOverlayApplyOutput 解析 cmd_apply 收尾行：
//
//	OK: <out> replaced=N overflow=M requested=K wm_blanked=W
//
// 参数：out=子进程 stdout。返回: 统计（解析不出的字段保持 0，宁保守不误判成功——
// 全 0 时 Requested==0 判为「无可判信息」仍走成功，保持对旧版脚本输出的兼容）。
func parseOverlayApplyOutput(out string) OverlayApplyStats {
	var st OverlayApplyStats
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "OK:") {
			continue
		}
		for _, f := range strings.Fields(line) {
			k, v, ok := strings.Cut(f, "=")
			if !ok {
				continue
			}
			n, _ := strconv.Atoi(v)
			switch k {
			case "replaced":
				st.Replaced = n
			case "overflow":
				st.Overflow = n
			case "requested":
				st.Requested = n
			}
		}
	}
	return st
}
