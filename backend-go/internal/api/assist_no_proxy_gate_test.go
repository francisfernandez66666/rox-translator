// ============ assist_no_proxy_gate_test.go · 职责说明 ============
// 四期 §4.5.3 **A4**：跨进程头零残留（源码扫描型负向锁）。
// 旧代码（attachAssistProfile / attachSalesFacts / strip*Headers / assistClient /
// assistBaseURL）原样保留在各自文件里，但**新链路两个文件**不得再触碰它们：
// 本地 handler 只做 ctx 直注 + la.handler.ServeHTTP，零出网、零签名头。
//
// 反证（M0301）：在 assist_local.go 里写回一处
//
//	Header.Set(mainengine.SalesFactsHeader, ...) ⇒ 本锁判红。
//
// ★ 射程刻意只扫「新链路文件」：旧文件是回滚档与 A1 对照面，允许保留。
//
//	把整仓一起扫会恒红（现有 sales_outbound_gate_test.go 等本来就引这些名字），
//	那才是空转锁。A6 那条扫全 api 目录的锁负责「别处不许加回 import」。
//
// ================================================
package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// a4CodeOnly 剥掉行注释，避免把「说明里提到的头名」误判成「代码里写了头」。
// 只处理行注释：本仓这两个新文件不含块注释、也不含带 // 的字符串字面量。
func a4CodeOnly(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			b.WriteString("\n")
			continue
		}
		if i := strings.Index(line, " //"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// a4CrossProcHeaderRe 任何把画像/事实跨进程头写进请求的形态。
var a4CrossProcHeaderRe = regexp.MustCompile(`Header\.Set\([^)]*(SalesFacts|HeaderProfile|HeaderTs|HeaderSig|AssistProfile)`)

// a4Forbidden 旧跨进程链的代码形态（新链路里一处都不许出现）。
var a4Forbidden = []string{
	"assistClient.Do(",
	"http.NewRequestWithContext(",
	"s.assistBaseURL()",
	"attachAssistProfile(",
	"attachSalesFacts(",
	"stripAssistProfileHeaders(",
	"stripSalesFactsHeaders(",
}

// a4Scan 对一段（已剥注释的）源码做跨进程残留判定，返回命中的坏形态。
func a4Scan(code string) []string {
	hits := []string{}
	if a4CrossProcHeaderRe.MatchString(code) {
		hits = append(hits, "Header.Set(跨进程头)")
	}
	for _, f := range a4Forbidden {
		if strings.Contains(code, f) {
			hits = append(hits, f)
		}
	}
	return hits
}

// a4Read 读一个新链路源文件（剥注释后）。
func a4Read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("A4 读 %s 失败: %v", name, err)
	}
	return a4CodeOnly(string(b))
}

// TestA4NoCrossProcHeaders 新链路两个文件零跨进程头 / 零远程转发。
func TestA4NoCrossProcHeaders(t *testing.T) {
	retire := a4Read(t, "assist_crossproc_retire.go")
	local := a4Read(t, "assist_local.go")
	for _, item := range []struct{ name, code string }{
		{"assist_local.go", local},
		{"assist_crossproc_retire.go", retire},
	} {
		if hits := a4Scan(item.code); len(hits) > 0 {
			t.Fatalf("A4 %s 出现跨进程链残留 %v：本地链路不得再写签名头或走 assistClient（M0301）", item.name, hits)
		}
	}

	// 本地替代物必须在位：事实集直注 WithSalesFacts + 本地 handler 转调。
	if !strings.Contains(retire, "mainengine.WithSalesFacts(ctx, set)") {
		t.Fatal("A4 本地事实集注入点 localFactsContext 的 WithSalesFacts 不在位")
	}
	if !strings.Contains(local, "la.handler.ServeHTTP(cw, r2)") {
		t.Fatal("A4 本地四端点转调不在位：回落到 HTTP 转发就退回跨进程了")
	}
	// 退役清单本身被钉住（不许悄悄把某一项从清单里拿掉当作「已退役」）。
	if len(retiredCrossProcReads) != 7 {
		t.Fatalf("A4 退役清单 %d 项（期望恰 7）", len(retiredCrossProcReads))
	}

	// ★ 反证（射程自证）：写回一处 Header.Set(SalesFactsHeader) 的坏样本，
	//   必须被 a4Scan 判红；若扫描恒返回空，上面两条就是空转锁。
	badSample := `r2.Header.Set(mainengine.SalesFactsHeader, "x")` + "\n"
	if hits := a4Scan(a4CodeOnly(badSample)); len(hits) == 0 {
		t.Fatal("A4 反证失明：写回一处 SalesFactsHeader 没被判红 ⇒ 扫描器空转")
	}
	goodSample := `ctx := mainengine.WithSalesFacts(r.Context(), set)` + "\n"
	if hits := a4Scan(a4CodeOnly(goodSample)); len(hits) != 0 {
		t.Fatalf("A4 判据过敏：合法的 ctx 直注被误判成跨进程头 %v", hits)
	}
}
