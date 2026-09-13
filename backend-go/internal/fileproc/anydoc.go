// ============ anydoc.go · 职责说明 ============
// firecrawl/anydoc（Rust 核心，MIT）集成：任意办公文档 → 结构化 Markdown。
// 工单「纯文案模式」（delivery=text）的提取层：doc/xls/ppt 老格式、odt/ods/odp、
// rtf/epub 等 anydoc 独占格式经此转为干净 GFM 后进入既有 MD 翻译管线。
// 边界（刻意不做）：① 无 MD→office 反向转换（版式/字体必丢，保真度劣于原生原位回写）；
// ② 无 hosted OCR（文档出境与私有化承诺冲突）——扫描版 PDF 返回友好错误。
// 调用方式：Python binding（venv firecrawl-anydoc）经 anydoc_md.py 子命令壳，
// 复用 runSubprocess 治理（资源闸 + nice + 超时整组击杀）。
// =============================================
package fileproc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// AnydocFormats anydoc 独占（现有 Go 原生提取器不支持）的准入格式白名单——
// 仅纯文案模式（delivery=text）且 anydoc 依赖可用时放行。
// .pdf 不在此列：Go 侧既有 pdf2docx 高保真链，纯文案模式对 PDF 的 anydoc 判定在
// 引擎层 anydocSourceExt() 单独纳入。
var AnydocFormats = map[string]bool{
	".doc": true, ".docm": true,
	".ppt": true, ".pps": true, ".pot": true, ".pptm": true, ".ppsx": true, ".ppsm": true,
	".xls": true, ".xlsm": true, ".xlsb": true,
	".odt": true, ".ods": true, ".odp": true,
	".rtf": true, ".epub": true,
}

// anydocTimeout anydoc 转换墙钟预算（Rust 本地转换中位毫秒级；60s 富余含闸排队）。
func anydocTimeout() time.Duration {
	if v := os.Getenv("ANYDOC_TIMEOUT_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 60 * time.Second
}

// anydocScriptPath 定位 anydoc_md.py（与可执行文件同目录，同 docx_translate.py 部署约定；
// ANYDOC_SCRIPT 环境变量覆盖，供开发/测试环境使用）。
func anydocScriptPath() string {
	if v := strings.TrimSpace(os.Getenv("ANYDOC_SCRIPT")); v != "" {
		return v
	}
	return filepath.Join(filepath.Dir(os.Args[0]), "anydoc_md.py")
}

// AnydocAvailable anydoc Python 依赖是否可用（healthcheck 缓存结果）。
func AnydocAvailable() bool {
	return CheckHealth().AnydocAvail
}

// AnydocToMarkdown 任意文档 → GFM Markdown 字符串。
// 失败返回的 error 文本已是面向用户的友好话术（工单直接透传）。
func AnydocToMarkdown(ctx context.Context, path string) (string, error) {
	if !AnydocAvailable() {
		return "", fmt.Errorf("纯文案模式依赖未就绪：服务器未安装 firecrawl-anydoc（pip install firecrawl-anydoc）")
	}
	if _, err := os.Stat(anydocScriptPath()); err != nil {
		return "", fmt.Errorf("纯文案模式脚本缺失：anydoc_md.py 未随二进制部署到 %s", filepath.Dir(os.Args[0]))
	}
	bin, argv := wrapNice(pyBin(), []string{anydocScriptPath(), "convert", path})
	stdout, stderr, err := runSubprocess(ctx, anydocTimeout(), bin, argv, nil)
	if err != nil {
		msg := friendlyAnydocError(string(stderr))
		if msg != "" {
			return "", fmt.Errorf("%s", msg)
		}
		return "", fmt.Errorf("anydoc 转换失败: %w\n%s", err, truncateTail(stderr))
	}
	md := string(stdout)
	if strings.TrimSpace(md) == "" {
		return "", fmt.Errorf("文档无有效内容（转换结果为空）")
	}
	return md, nil
}

// friendlyAnydocError 解析 anydoc_md.py stderr 的 ERR:<Variant>:<msg> 行 → 面向用户话术；
// 无法识别时返回 ""（由调用方给原始错误）。
func friendlyAnydocError(stderr string) string {
	line := ""
	for _, l := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(l, "ERR:") {
			line = l
			break
		}
	}
	if line == "" {
		return ""
	}
	variant := strings.SplitN(strings.TrimPrefix(line, "ERR:"), ":", 2)[0]
	switch variant {
	case "EncryptedError":
		return "文件已加密或带密码，无法提取文案，请解除密码后重试"
	case "NeedsOcrError":
		return "扫描版/图片型文档：服务器不提供文字识别（OCR），无法提取文案，请转存为 Word 后重试"
	case "MalformedError", "MissingPartError":
		return "文件结构损坏：未能提取到有效内容"
	case "UnsupportedError", "ValueError":
		return "不支持的文件格式，无法转换为文案"
	case "ResourceLimitError":
		return "文件超出安全限制（解压比/嵌套深度/节点数），转换被拒绝"
	case "Import":
		return "纯文案模式依赖未就绪：服务器未安装 firecrawl-anydoc"
	case "Io":
		return "文件不可读取或已丢失"
	default:
		return ""
	}
}

// AnydocDetectFormat 按文件内容魔数探测真实格式（返回如 "docx"；探测不出返回 ""）。
// 用途：建单时校验扩展名与内容一致性（伪扩展名拦截），P3 接入。
func AnydocDetectFormat(ctx context.Context, path string) string {
	if !AnydocAvailable() {
		return ""
	}
	if _, err := os.Stat(anydocScriptPath()); err != nil {
		return ""
	}
	bin, argv := wrapNice(pyBin(), []string{anydocScriptPath(), "detect", path})
	stdout, _, err := runSubprocess(ctx, anydocTimeout(), bin, argv, nil)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(stdout))
}
