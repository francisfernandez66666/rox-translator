// ============ content_disposition_test.go · 职责说明 ============
// G5 下载文件名乱码（D6）的单测与家族级静态闸：
//
//	· 家族级静态闸＝扫 internal/api 全部非测试 .go，凡 Content-Disposition 头不经
//	  contentDisposition(...) 出口即红（"只有 3 处过 mimeEscape、17 处裸拼"那种漏网靠全集判）；
//	· RFC 5987 正锁＝filename*=UTF-8'' 存在且真名 percent 编码（中文不再以裸字节进 filename=）；
//	· 注入负向＝名里带引号/换行/路径分隔符不得破坏响应头（结构引号恰 2 个、无裸 CR/LF）。
//
// 反证：造一处直拼头 ⇒ 家族闸必须红（rawContentDispositionOffenders 非空）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run 'TestContentDisposition'
// =============================================
package api

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
)

// rawContentDispositionOffenders 返回未经 contentDisposition 出口、直接写 Content-Disposition
// 响应头的行（已剥注释）。返回空切片＝合规。
// 参数 src: 单个 .go 源文件原文。返回: "行号: 内容" 形式的违规行列表。
func rawContentDispositionOffenders(src string) []string {
	code := stripCodeComments(src)
	var out []string
	for i, ln := range strings.Split(code, "\n") {
		if !strings.Contains(ln, `"Content-Disposition"`) {
			continue
		}
		if strings.Contains(ln, "contentDisposition(") {
			continue
		}
		out = append(out, fmt.Sprintf("%d: %s", i+1, strings.TrimSpace(ln)))
	}
	return out
}

// countContentDispositionHeader 统计剥注释后 `"Content-Disposition"` 字面量出现次数（射程自证用）。
func countContentDispositionHeader(src string) int {
	return strings.Count(stripCodeComments(src), `"Content-Disposition"`)
}

// TestContentDispositionSingleExit 家族级静态闸：全包非测试 .go 的下载头只许经 contentDisposition 出口。
func TestContentDispositionSingleExit(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取包目录失败：%v", err)
	}
	total := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, rerr := os.ReadFile(name)
		if rerr != nil {
			t.Fatalf("读取 %s 失败：%v", name, rerr)
		}
		if off := rawContentDispositionOffenders(string(b)); len(off) > 0 {
			t.Errorf("%s 有未经 contentDisposition 出口的下载头：%v", name, off)
		}
		total += countContentDispositionHeader(string(b))
	}
	// 射程自证：锚点一旦漂移（有人把 Header().Set 换写法）计数会掉下来，闸门会"失明地变绿"。
	// ★ 2026-10-30 组5：下载面收敛成"取件助手 + 单一出口"（zip 改按需流式），
	//   直拼头的位置从 20+ 降到 13 处——**这是合法的收缩**，所以下限跟着现读值下调到 10（留余量）。
	// ⚠️ 下限只是"有人把写法换了、扫描器失明"的探测器，别把它抬到贴近现值（下次合法收缩会假红）。
	if total < 10 {
		t.Fatalf("射程自证失败：全包 Content-Disposition 头计数 %d < 10（锚点漂了，闸门已失明）", total)
	}
}

// TestContentDispositionGateCatchesRawHeader 反证：直拼头必须被家族闸抓到，经出口则放行。
func TestContentDispositionGateCatchesRawHeader(t *testing.T) {
	raw := "func f(w http.ResponseWriter) {\n\tw.Header().Set(\"Content-Disposition\", \"attachment; filename=x\")\n}\n"
	if len(rawContentDispositionOffenders(raw)) == 0 {
		t.Fatal("反证失效：直拼的 Content-Disposition 头没有被家族闸抓到")
	}
	ok := "func f(w http.ResponseWriter) {\n\tw.Header().Set(\"Content-Disposition\", contentDisposition(\"x\"))\n}\n"
	if off := rawContentDispositionOffenders(ok); len(off) != 0 {
		t.Fatalf("误伤：经出口的调用被判违规 %v", off)
	}
	// 注释里出现该字样不算违规（剥注释后判）。
	commented := "// 只取 filepath.Base 一个成分写进响应头：路径分隔符/换行不可能进 Content-Disposition\nfunc f() {}\n"
	if off := rawContentDispositionOffenders(commented); len(off) != 0 {
		t.Fatalf("注释里的字样被误判为违规：%v", off)
	}
}

// TestContentDispositionRFC5987 RFC 5987 正锁 + 注入负向。
func TestContentDispositionRFC5987(t *testing.T) {
	h := contentDisposition("图片卡.md")
	if !strings.Contains(h, "filename*=UTF-8''") {
		t.Fatalf("缺 RFC 5987 的 filename*：%q", h)
	}
	if !strings.Contains(h, `filename="`) {
		t.Fatalf("缺 ASCII 兜底 filename=：%q", h)
	}
	// 真名必须是 percent 编码（latin1 读回不再是裸 UTF-8 字节）
	if !strings.Contains(h, "%E5%9B%BE") { // "图" = E5 9B BE
		t.Fatalf("真名未 percent 编码：%q", h)
	}
	// filename* 能解回原始 UTF-8 真名（源码里的字符串就是 UTF-8，PathUnescape 还原）
	star := h[strings.Index(h, "UTF-8''")+len("UTF-8''"):]
	dec, uerr := url.PathUnescape(star)
	if uerr != nil || dec != "图片卡.md" {
		t.Fatalf("filename* 解回失败：star=%q dec=%q err=%v", star, dec, uerr)
	}
	// 兜底名里不得出现非 ASCII（否则老客户端又乱码）
	fallback := betweenQuotes(h)
	if fallback == "" {
		t.Fatalf("提取兜底名失败：%q", h)
	}
	for _, r := range fallback {
		if r > 127 {
			t.Fatalf("ASCII 兜底名含非 ASCII 字符 %q：%q", r, fallback)
		}
	}

	// 注入负向：引号/换行/路径分隔符都不得破坏头。
	evil := contentDisposition("evil\"\r\nX-Injected: 1/../../etc/passwd.md")
	if strings.ContainsAny(evil, "\r\n") {
		t.Fatalf("响应头被换行注入：%q", evil)
	}
	if strings.Count(evil, `"`) != 2 {
		t.Fatalf("结构引号应恰为 2（兜底名里的引号必须被剥掉）：%q", evil)
	}
	if strings.Contains(evil, "../") {
		t.Fatalf("路径分隔符未收敛：%q", evil)
	}
	// 空名回落
	if h2 := contentDisposition(""); !strings.Contains(h2, `filename="download"`) {
		t.Fatalf("空名未回落 download：%q", h2)
	}
	// 纯非 ASCII 名：主干丢空 ⇒ download.<ext>，真名仍走 filename*
	if h3 := contentDisposition("用户手册.pdf"); !strings.Contains(h3, `filename="download.pdf"`) {
		t.Fatalf("纯非 ASCII 名未回落 download.pdf：%q", h3)
	}
}

// betweenQuotes 取第一个 `filename="..."` 里括号内的兜底名（测试辅助）。
func betweenQuotes(h string) string {
	i := strings.Index(h, `filename="`)
	if i < 0 {
		return ""
	}
	rest := h[i+len(`filename="`):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	return rest[:j]
}
