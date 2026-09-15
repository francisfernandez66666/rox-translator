// ============ cmd/anydoc-smoke · 职责说明 ============
// 部署排障工具：验证工单「纯文案模式」的 anydoc 子进程链路
// （健康探测 → 子进程转换 → 错误话术映射）。
// 用法：go run ./cmd/anydoc-smoke <文档路径>
// 环境：PATH 含装了 firecrawl-anydoc 的 Python；ANYDOC_SCRIPT 可覆盖脚本路径
// （默认取可执行文件同目录 anydoc_md.py，与 docx_translate.py 同部署约定）。
// =============================================
package main

import (
	"context"
	"fmt"
	"os"

	"translator/internal/fileproc"
)

// main 三步冒烟：① 探测 anydoc 可用性与 Python 路径；② 对入参文档实跑一次提取；
// ③ 打印结果或映射后的错误话术（与工单侧用户可见文案一致，便于现场定位）。
func main() {
	h := fileproc.CheckHealth()
	fmt.Printf("anydoc_available=%v python=%s\n", h.AnydocAvail, h.PythonPath)
	if len(os.Args) < 2 {
		fmt.Println("usage: anydoc-smoke <file>")
		os.Exit(2)
	}
	md, err := fileproc.AnydocToMarkdown(context.Background(), os.Args[1])
	if err != nil {
		fmt.Println("ERR:", err)
		os.Exit(1)
	}
	fmt.Println("=== markdown ===")
	if len(md) > 600 {
		md = md[:600] + "\n…(截断)"
	}
	fmt.Println(md)
}
