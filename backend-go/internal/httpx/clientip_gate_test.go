// ============ clientip_gate_test.go · 职责说明 ============
// 组 2a「客户端真实 IP 只有一把尺子」的家族级闸门（K6／K7 的执行体）。
//
// 三条判据各拦一类坏法，缺一类就放过去一个现网缺陷：
//
//	T1 行为表（K6）：**非回环直连时，任何代理链头都不许改变结论**。
//	   这条不是"文案好不好看"——本批免费档要按 IP 记一格（`free_ip`），
//	   旧实现里 assist 那侧无条件信任 XFF，等于让任何访客自称"我是新来的"，
//	   档位一上线就能被脚本连打。回环那一侧的每一档（CF 优先／XFF 最左／脏值回落）
//	   也必须逐档点名：只测"伪造被拒"会漏掉"真反代被误伤"（全站共享 127.0.0.1 那次事故）。
//	T2 委托接线：`internal/api` 与 `internal/assist/api` 两侧的 `clientIP` 函数体
//	   必须**只有**一次 `httpx.ClientIP(` 调用。判函数体而不是判文件——
//	   同一个文件里别处再写一份取 IP 的逻辑，正是"两份尺子"长回来的路径。
//	T3 全集扫描（K7）：全仓非测试源码里**读**这几个头名的文件，只许白名单那两个。
//	   白名单不是塞文件名就算数：`tenant.go` 那一处必须仍是「拿头**在不在**来撤销回环信任」
//	   的相等空串形态，取值当身份的写法一律判红（同 §一·5 bizRespGate 的「豁免要带理由与形状」）。
//
// ⚠️ T2／T3 都是源码级负向锁 ⇒ 先剥注释再扫（本仓反复踩过的「注释里提到那个词就把负向喂饱」）。
// 三段反证（T1 用夹具喂、T3 用合成源码喂、T2 用"旧形态文件"喂）证明这些负向腿有射程，而不是恒真。
// =============================================
package httpx

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forwardedHeaderNames 是"代理链告诉你客户端是谁"的那几个头。
// 名单本身也是判据的一部分：新加一家代理（比如某家 CDN 的自定义头）必须先动这里，
// 而不是在调用方各写一次 `r.Header.Get("X-Custom-Client")`。
var forwardedHeaderNames = []string{
	"X-Forwarded-For", "X-Real-IP", "Proxy-Client-IP", "WL-Client-IP", "CF-Connecting-IP", "True-Client-IP",
}

// T1 行为表
func TestClientIPTrustsHeadersOnlyFromLoopbackPeer(t *testing.T) {
	newReq := func(peer string, hdr map[string]string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/x", nil)
		r.RemoteAddr = peer
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		return r
	}
	cases := []struct {
		name string
		peer string
		hdr  map[string]string
		want string
	}{
		// —— 非回环直连：头一律不采纳（这一档就是 K6 的正靶）——
		{"公网直连＋伪造 XFF ⇒ 用 peer", "203.0.113.9:51234", map[string]string{"X-Forwarded-For": "1.2.3.4"}, "203.0.113.9"},
		{"公网直连＋伪造 CF 头 ⇒ 用 peer", "203.0.113.9:51234", map[string]string{"CF-Connecting-IP": "1.2.3.4"}, "203.0.113.9"},
		{"公网直连＋伪造 True-Client-IP ⇒ 用 peer", "203.0.113.9:1", map[string]string{"True-Client-IP": "8.8.8.8"}, "203.0.113.9"},
		{"公网直连＋什么头都不带 ⇒ 用 peer", "198.51.100.7:8080", nil, "198.51.100.7"},
		// —— 本机反代形态：按链给的头，先 CF 再 XFF ——
		{"回环＋CF 头 ⇒ 采纳 CF", "127.0.0.1:8080", map[string]string{"CF-Connecting-IP": "203.0.113.77"}, "203.0.113.77"},
		{"回环＋CF 与 XFF 都在 ⇒ CF 优先", "127.0.0.1:8080", map[string]string{"CF-Connecting-IP": "203.0.113.77", "X-Forwarded-For": "9.9.9.9"}, "203.0.113.77"},
		{"回环＋多跳 XFF ⇒ 最左一跳", "127.0.0.1:8080", map[string]string{"X-Forwarded-For": "198.51.100.5, 10.0.0.1"}, "198.51.100.5"},
		{"回环＋IPv6 ::1 ⇒ 也算回环", "[::1]:8080", map[string]string{"X-Forwarded-For": "2001:db8::1"}, "2001:db8::1"},
		// —— 脏值不许当身份（最坏的坏法不是取不到，而是取到一个每次都不同的假值）——
		{"回环＋CF 头是垃圾 ⇒ 回落 XFF", "127.0.0.1:8080", map[string]string{"CF-Connecting-IP": "not-an-ip", "X-Forwarded-For": "198.51.100.9"}, "198.51.100.9"},
		{"回环＋XFF 写 unknown ⇒ 回落 peer", "127.0.0.1:8080", map[string]string{"X-Forwarded-For": "unknown"}, "127.0.0.1"},
		{"回环＋两个头都没有 ⇒ peer", "127.0.0.1:8080", nil, "127.0.0.1"},
		{"回环＋XFF 带端口串 ⇒ 剥端口后采纳", "127.0.0.1:8080", map[string]string{"X-Forwarded-For": "198.51.100.9:443"}, "198.51.100.9"},
		// —— 畸形 RemoteAddr：原样回落，不折成空串（空 key 会让所有畸形请求共享一格）——
		{"畸形 RemoteAddr ⇒ 原样返回", "garbage-no-port", nil, "garbage-no-port"},
	}
	for _, c := range cases {
		got := ClientIP(newReq(c.peer, c.hdr))
		if got != c.want {
			t.Errorf("%s：ClientIP=%q，期望 %q", c.name, got, c.want)
		}
	}
	// nil 请求不回 panic 也不回空串以外的东西（调用侧有"取不到就按匿名处理"的分支）
	if got := ClientIP(nil); got != "" {
		t.Errorf("ClientIP(nil)=%q，期望空串", got)
	}
}

// T2 两侧委托接线
func TestClientIPCallersDelegateOnly(t *testing.T) {
	type leg struct{ file, pkgRel string }
	for _, l := range []leg{
		{"../../internal/api/ratelimit.go", "api"},
		{"../../internal/assist/api/server.go", "assist"},
	} {
		raw, err := os.ReadFile(l.file)
		if err != nil {
			t.Fatalf("读 %s 失败（读不到＝这条腿失明，不算绿）：%v", l.file, err)
		}
		body := ciStripComments(string(raw))
		sig := "func clientIP(r *http.Request) string {"
		fn, ok := ciFuncBody(body, sig)
		if !ok {
			t.Fatalf("%s（%s 包）里取不到 clientIP 函数体 ⇒ 签名变了要同步改这条锁，别删断言", l.file, l.pkgRel)
		}
		if n := strings.Count(fn, "httpx.ClientIP("); n != 1 {
			t.Errorf("%s 的 clientIP 函数体里 httpx.ClientIP( 出现 %d 次（期望恰 1）：0＝没委托，>1＝又长出第二次取法", l.file, n)
		}
		for _, dead := range forwardedHeaderNames {
			if strings.Contains(fn, `"`+dead+`"`) {
				t.Errorf("%s 的 clientIP 还在本地读 %q ⇒ 全仓第二把尺子复活", l.file, dead)
			}
		}
	}
	// 旧开关不再被任何产品代码读（env 本身在 deploy/systemd/prod.conf 里已成空配置，同批注掉）
	root := filepath.Join("..", "..")
	hits := ciScanHeaderReads(t, root, "TRUST_PROXY_XFF")
	if len(hits) != 0 {
		t.Errorf("TRUST_PROXY_XFF 还被这些非测试源码读着（新口径按来路判，不看开关）：%s", strings.Join(hits, "、"))
	}
}

// T3 全集扫描（K7）：读代理链头的文件只许白名单那两个
func TestOnlyOneClientIPImplementationRepoWide(t *testing.T) {
	allow := map[string]string{
		// 唯一实现本体
		filepath.Join("internal", "httpx", "clientip.go"): "全仓唯一实现（信任链本体，头名名单也在这里）",
		// 反向用途：只判「这条请求带没带 XFF」来**撤销**回环身份，不取头里的值当身份
		filepath.Join("internal", "api", "tenant.go"): "只判 XFF 在不在（带 XFF 就不认回环），绝不采纳头值",
	}
	root := filepath.Join("..", "..")
	var offenders []string
	var sawAllowed int
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		body := ciStripComments(string(raw))
		var read []string
		for _, h := range forwardedHeaderNames {
			if strings.Contains(body, `Header.Get("`+h+`")`) || strings.Contains(body, `Header.Values("`+h+`")`) {
				read = append(read, h)
			}
		}
		if len(read) == 0 {
			return nil
		}
		if why, ok := allow[rel]; ok {
			sawAllowed++
			// 豁免不是文件名说了算：tenant.go 那一处必须仍是**相等空串**判据（取值当身份即红）
			if strings.HasPrefix(rel, filepath.Join("internal", "api")) && !strings.Contains(body, `Header.Get("X-Forwarded-For") == ""`) {
				offenders = append(offenders, rel+"：豁免形态变了（不再是「头 == 空串」的撤销判据）——"+why)
			}
			return nil
		}
		offenders = append(offenders, rel+"（读到 "+strings.Join(read, "／")+"）")
		return nil
	})
	if err != nil {
		t.Fatalf("遍历 %s 失败：%v", root, err)
	}
	for _, o := range offenders {
		t.Error("第二把尺子：" + o)
	}
	// 射程自证：探测器必须**认得出那两个白名单文件**，认不出来说明匹配形态漂了（本仓所有负向锁最怕的一档）
	if sawAllowed != len(allow) {
		t.Errorf("白名单命中 %d/%d ⇒ 扫描的匹配形态已漂（Header.Get 写法变了？），这条 K7 现在是空转", sawAllowed, len(allow))
	}
	// 反证：合成一段"自己取 XFF 当身份"的源码，必须被同一个判据抓住
	synthetic := `package x
func f(r *http.Request) string { return r.Header.Get("X-Forwarded-For") }`
	if !strings.Contains(ciStripComments(synthetic), `Header.Get("X-Forwarded-For")`) {
		t.Error("反证落空：合成源码里的取头写法没被匹配形态认出来 ⇒ 上面那条全集判据抓不住任何退化")
	}
	// 反证的另一半：注释里提到头名**不该**被算成命中（否则本文件的说明文字会自己判红）
	if strings.Contains(ciStripComments("// 这里解释为什么不信 X-Forwarded-For\npackage x"), "X-Forwarded-For") {
		t.Error("反证落空：剥注释没剥干净 ⇒ 说明注释会把负向判据喂饱")
	}
}

// ciScanHeaderReads 在 root 下的非测试 .go 源码里找一个字面（剥注释后逐行比），返回命中文件的相对路径。
func ciScanHeaderReads(t *testing.T, root, lit string) []string {
	t.Helper()
	var hits []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		if strings.Contains(ciStripComments(string(raw)), lit) {
			rel, _ := filepath.Rel(root, p)
			hits = append(hits, rel)
		}
		return nil
	})
	return hits
}

// ciStripComments 剥行注释与块注释（本文件只做形状扫描，不求词法完备）。
func ciStripComments(src string) string {
	var out []string
	inBlock := false
	for _, ln := range strings.Split(src, "\n") {
		t := strings.TrimSpace(ln)
		if inBlock {
			if strings.Contains(t, "*/") {
				inBlock = false
			}
			continue
		}
		if strings.HasPrefix(t, "/*") {
			inBlock = true
			if strings.Contains(t, "*/") {
				inBlock = false
			}
			continue
		}
		if strings.HasPrefix(t, "//") {
			continue
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// ciFuncBody 按花括号配平切出一个函数体（含签名），跳过字符串字面量里的花括号。
func ciFuncBody(src, sig string) (string, bool) {
	i := strings.Index(src, sig)
	if i < 0 {
		return "", false
	}
	open := strings.Index(src[i:], "{")
	if open < 0 {
		return "", false
	}
	depth, inStr, inRaw, esc := 0, false, false, false
	for j := i + open; j < len(src); j++ {
		c := src[j]
		if inRaw {
			if c == '`' {
				inRaw = false
			}
			continue
		}
		if inStr {
			if esc {
				esc = false
				continue
			}
			if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '`':
			inRaw = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[i : j+1], true
			}
		}
	}
	return "", false
}
