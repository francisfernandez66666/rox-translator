// ============================================================================
// caddy_security_header_gate_test.go — ★ 2026-10-17（用户报障批）把「浏览器能力
// 三件套」的修法接进自动化闸门，防止下一次好心重写把现网能力又关回去。
//
// 三件套（三条都是现网实证的「界面照常、功能静默失败」形态）：
//
//	① Permissions-Policy 里 `microphone=()` 是**禁用所有来源**——即时翻译的麦克风
//	  （getUserMedia）会被浏览器直接拒，点录音必失败；修法＝`microphone=(self)`（只放本域）。
//	② CSP `script-src` 缺 `'wasm-unsafe-eval'`——端侧 sherpa-onnx WASM 的
//	  WebAssembly.instantiateStreaming 被 CSP 判违反 unsafe-eval 整块编译失败；
//	  它是「允许 WASM 编译」的专用关键字，比放开 unsafe-eval 收敛得多（胶水层无 eval/new Function）。
//	③ CSP 缺 `media-src` ⇒ 回落 `default-src 'self'` ⇒ 译文朗读的 blob: 音频 URL 被拒
//	  ⇒ 点朗读无声、也不报错（三态回 idle）。修法＝`media-src 'self' blob:`。
//
//	★ 2026-10-17 第二批（同批用户报障：登录前页面 CSP 报错）再加三条——都是 Cloudflare 挑战平台/Turnstile
//	对**受保护页面**的硬要求（官方口径见 developers.cloudflare.com/turnstile/reference/content-security-policy
//	与 cloudflare-docs#17786；缺 'unsafe-eval' 时托管挑战可能整页白屏、把登录整条拦住）：
//	④ CSP `script-src` 增 `'unsafe-eval'`：挑战平台托管脚本用字符串求值（与 ② 的 'wasm-unsafe-eval' 分工不同：
//	  那条只服务自家 WASM 编译，这条为第三方挑战放行 JS eval）；
//	⑤ CSP 增 `worker-src 'self' blob:`：挑战平台起 worker；
//	⑥ CSP `connect-src` 增 `data:`：挑战平台 attestation 走 fetch('data:…')。
//
// 这六条修法在**三份配置**里各写一遍（主站 conf / 租户品牌域 conf / 演示站生成脚本），
// 少改一份就是「主站能录音、品牌域不能」或「下次 bootstrap 写回旧形态」，所以射程按
// 「哪些文件里有 Permissions-Policy / Content-Security-Policy」**派生**，不写死名单。
//
// 豁免口径（刻意的，别误伤）：`/assist-api/*` 那一条 CSP 是给挂件 iframe 的，不需要
// WASM/朗读能力——判据按「行内是否含 /assist-api/*」把它排除，且要求射程非空（防止
// 豁免把整条锁吃空）。演示站生成脚本没有 CSP 段，只对它跑 Permissions-Policy 那一腿。
//
// 判据先剥整行注释：这几份配置里解释本次修法的注释**本身就写着**被断言的形态字面量
// （负向锁被说明注释误伤是本仓反复踩过的一族）。本文件只读仓库文本，绝不碰现网。
// ============================================================================
package fileproc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 被断言的靶子字面量——与配置里写的**逐字符同源**，改名/改值式破坏必须当场红。
const (
	secPPOK       = "microphone=(self)"              // 期望形态：仅本域可用
	secPPBad      = "microphone=()"                  // 事故形态：全禁
	secCSPWasm    = "'wasm-unsafe-eval'"             // 允许 WASM 编译的专用关键字
	secCSPMedia   = "media-src 'self' blob:"         // 译文朗读的 blob 音频
	secCSPEval    = "'unsafe-eval'"                  // ★ 2026-10-17 二批：CF 挑战平台/Turnstile 的字符串求值
	secCSPWorker  = "worker-src 'self' blob:"        // ★ 同批：挑战平台的 worker
	secCSPConnDat = "connect-src 'self' blob: data:" // ★ 同批：挑战平台 attestation 的 data: 通道
	secCSPHeader  = "Content-Security-Policy"        //
	secPPHeader   = "Permissions-Policy"             //
	secAssistPath = "/assist-api/*"                  // 豁免：挂件 CSP 不需要这几项能力
)

// caddySecurityScopedFiles 按「文件里有没有 Permissions-Policy 或 Content-Security-Policy」
// 派生射程，不写死名单。一份都没有就 Fatal：配置搬家或头被删都要人来同步改这条锁。
func caddySecurityScopedFiles(t *testing.T) map[string]string {
	t.Helper()
	repo := findDispatchRepoRoot(t)
	seen := map[string]string{}
	dirs := []string{"deploy/caddy", "scripts"}
	for _, d := range dirs {
		entries, err := os.ReadDir(filepath.Join(repo, d))
		if err != nil {
			t.Fatalf("读 %s 失败：%v ⇒ 目录结构变了，不许静默放行", d, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			rel := filepath.ToSlash(filepath.Join(d, e.Name()))
			if !caddyInScopePath(rel) {
				continue
			}
			b, err := os.ReadFile(filepath.Join(repo, rel))
			if err != nil {
				t.Fatalf("读 %s 失败：%v", rel, err)
			}
			s := string(b)
			if strings.Contains(s, secPPHeader) || strings.Contains(s, secCSPHeader) {
				seen[rel] = s
			}
		}
	}
	if len(seen) == 0 {
		t.Fatalf("射程内（deploy/caddy/*.conf ＋ scripts/bootstrap-demo.sh）一份带安全头的配置都没有 ⇒ 配置搬家或头被删，两者都要改这条锁，不许静默通过")
	}
	return seen
}

// caddySecurityWiring 判一份配置里的三条能力修法是不是都在。返回空切片＝成立。
//
//	L1 任何 Permissions-Policy 行出现 `microphone=()` ⇒ 麦克风被全禁（本次事故形态）；
//	L2 任何 Permissions-Policy 行缺 `microphone=(self)` ⇒ 放行腿不在（漏改/被回改）；
//	L3 任何**非挂件** CSP 行的 script-src 缺 'wasm-unsafe-eval' ⇒ WASM 编译被 CSP 拦；
//	L4 任何**非挂件** CSP 行缺 `media-src 'self' blob:` ⇒ 朗读 blob 音频被拒；
//	L5 任何**非挂件** CSP 行的 script-src 缺 'unsafe-eval' ⇒ CF 挑战/Turnstile 的字符串求值被拦；
//	L6 任何**非挂件** CSP 行缺 `worker-src 'self' blob:` ⇒ 挑战平台的 worker 被兜住；
//	L7 任何**非挂件** CSP 行的 connect-src 缺 `data:` ⇒ 挑战平台 attestation 被拦。
//
// 挂件豁免＝行内含 /assist-api/*；但整份配置里若一条非挂件 CSP 都没有（L3/L4 靶子为空）
// 也判问题——射程归零时绿灯没有意义（同 AGENTS「判据射程不足要报『锚点漂了』」）。
func caddySecurityWiring(rel, content string) []string {
	var problems []string
	lines := caddyNonCommentLines(content)
	ppSeen, strictCSPSeen := 0, 0
	for _, raw := range lines {
		l := strings.TrimSpace(raw)
		if strings.Contains(l, secPPHeader) {
			ppSeen++
			if strings.Contains(l, secPPBad) {
				problems = append(problems, rel+": Permissions-Policy 出现「"+secPPBad+"」（L1）⇒ 麦克风被全禁，点录音必失败")
			}
			if !strings.Contains(l, secPPOK) {
				problems = append(problems, rel+": Permissions-Policy 缺「"+secPPOK+"」（L2）⇒ 未放开本域麦克风")
			}
		}
		if strings.Contains(l, secCSPHeader) && !strings.Contains(l, secAssistPath) {
			strictCSPSeen++
			if !strings.Contains(l, "script-src") || !strings.Contains(l, secCSPWasm) {
				problems = append(problems, rel+": CSP script-src 缺 "+secCSPWasm+"（L3）⇒ 端侧 WASM 编译被拦")
			}
			if !strings.Contains(l, secCSPMedia) {
				problems = append(problems, rel+": CSP 缺「"+secCSPMedia+"」（L4）⇒ 译文朗读的 blob 音频被拒")
			}
			// ★ L5/L6/L7（2026-10-17 二批，用户报障「登录前页面 CSP 报错」）：
			//   CF 挑战平台/Turnstile 的托管脚本用字符串求值、起 worker、并以 fetch('data:…') 做 attestation；
			//   受保护页面缺任一条都会在控制台报被拦（托管挑战还可能整页白屏，cloudflare-docs#17786）。
			//   ⚠️ 判 'unsafe-eval' 用**带引号的整词**：'wasm-unsafe-eval' 里没有 "'unsafe-eval'" 这个子串
			//   （引号前是 '-'），所以两者互不冒充——别把它写成裸词 unsafe-eval（那样 L5 会被 L3 那条喂饱）。
			if !strings.Contains(l, secCSPEval) {
				problems = append(problems, rel+": CSP script-src 缺 "+secCSPEval+"（L5）⇒ CF 挑战/Turnstile 的 eval 被拦")
			}
			if !strings.Contains(l, secCSPWorker) {
				problems = append(problems, rel+": CSP 缺「"+secCSPWorker+"」（L6）⇒ 挑战平台的 worker 被 default-src 兜住")
			}
			if !strings.Contains(l, secCSPConnDat) {
				problems = append(problems, rel+": CSP connect-src 缺 data:（L7）⇒ 挑战平台 attestation 被 connect-src 拦")
			}
		}
	}
	if ppSeen == 0 {
		problems = append(problems, rel+": 一条 Permissions-Policy 都没读到（射程不足，锚点漂了）⇒ 闸门已失效")
	}
	if strictCSPSeen == 0 && strings.Contains(content, secCSPHeader) {
		problems = append(problems, rel+": 有 CSP 但非挂件的严格 CSP 一条都没命中（射程不足，锚点漂了）⇒ 闸门已失效")
	}
	return problems
}

// TestCaddySecurityHeadersAllowWasmMicBlob 对每一份射程内配置跑同一把尺子。
func TestCaddySecurityHeadersAllowWasmMicBlob(t *testing.T) {
	scoped := caddySecurityScopedFiles(t)
	var problems []string
	for rel, content := range scoped {
		problems = append(problems, caddySecurityWiring(rel, content)...)
	}
	if len(problems) > 0 {
		t.Errorf("浏览器能力三件套的修法不成（每一行都是一条独立断链）：\n  %s\n"+
			"要写的形态：Permissions-Policy 带「%s」；严格 CSP 的 script-src 带 %s、并含「%s」",
			strings.Join(problems, "\n  "), secPPOK, secCSPWasm, secCSPMedia)
	}
	t.Logf("射程内配置 %d 份，三件套修法全部成立", len(scoped))
}

// replaceInCodeLine 在**第一条非注释行**里替换 `old`（只替一次）。
// ⚠️ 为什么不能直接 strings.Replace：本批的说明注释里就写着被替换的目标字面量
// （如「放开 microphone=(self)」），对全文替换会先打中注释 ⇒ 变异体其实没动实现，
// 反证变成"打了但没打掉靶子"（本仓反复踩过的一族自伤）。
func replaceInCodeLine(s, old, nw string) string {
	lines := strings.Split(s, "\n")
	done := false
	for i, l := range lines {
		if done || strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		if strings.Contains(l, old) {
			lines[i] = strings.Replace(l, old, nw, 1)
			done = true
		}
	}
	return strings.Join(lines, "\n")
}

// TestCaddySecurityHeadersCounterproof 反证三枚（全在内存副本上跑，仓库原件一字不动）：
//
//	M1 把 microphone=(self) 改回 microphone=() ⇒ L1/L2 必须出声；
//	M2 摘掉 CSP 里的 'wasm-unsafe-eval' ⇒ L3 必须出声；
//	M3 摘掉 CSP 里的 media-src 'self' blob: ⇒ L4 必须出声；
//	M4 摘掉 CSP 里的 'unsafe-eval' ⇒ L5 必须出声；
//	M5 摘掉 CSP 里的 worker-src 'self' blob: ⇒ L6 必须出声；
//	M6 把 connect-src 的 data: 摘掉 ⇒ L7 必须出声。
//
// 每一枚都必须让**同一个判据函数**出声，且原件先证明全清（否则变异体的红说明不了任何事）。
func TestCaddySecurityHeadersCounterproof(t *testing.T) {
	scoped := caddySecurityScopedFiles(t)
	cases := []struct {
		name   string
		target string // 该维在文件里的靶子：不含则跳过这一枚（演示站脚本无 CSP）
		mut    func(string) string
		want   string
	}{
		{
			name:   "M1 麦克风写回全禁 microphone=()",
			target: secPPOK,
			mut:    func(s string) string { return replaceInCodeLine(s, secPPOK, "microphone=()") },
			want:   "L1",
		},
		{
			name:   "M2 摘掉 CSP 的 'wasm-unsafe-eval'",
			target: secCSPWasm,
			mut:    func(s string) string { return replaceInCodeLine(s, secCSPWasm+" ", "") },
			want:   "L3",
		},
		{
			name:   "M3 摘掉 CSP 的 media-src",
			target: secCSPMedia,
			mut:    func(s string) string { return replaceInCodeLine(s, secCSPMedia+"; ", "") },
			want:   "L4",
		},
		{
			name:   "M4 摘掉 CSP 的 'unsafe-eval'（CF 挑战的字符串求值）",
			target: secCSPEval,
			mut:    func(s string) string { return replaceInCodeLine(s, secCSPEval+" ", "") },
			want:   "L5",
		},
		{
			name:   "M5 摘掉 CSP 的 worker-src",
			target: secCSPWorker,
			mut:    func(s string) string { return replaceInCodeLine(s, secCSPWorker+"; ", "") },
			want:   "L6",
		},
		{
			name:   "M6 把 connect-src 的 data: 摘掉",
			target: secCSPConnDat,
			mut: func(s string) string {
				return replaceInCodeLine(s, "connect-src 'self' blob: data: https://challenges.cloudflare.com",
					"connect-src 'self' blob: https://challenges.cloudflare.com")
			},
			want: "L7",
		},
	}
	for rel, content := range scoped {
		rel, content := rel, content
		// 对照腿：原件必须全清。
		if base := caddySecurityWiring(rel, content); len(base) != 0 {
			t.Fatalf("%s 原件在这条尺子下就不成（%v）⇒ 对照腿失效", rel, base)
		}
		for _, c := range cases {
			c := c
			if !strings.Contains(content, c.target) {
				continue // 该文件不含这一维（演示站脚本无 CSP）：skip 这一枚，不判红
			}
			t.Run(rel+"/"+c.name, func(t *testing.T) {
				variant := c.mut(content)
				if variant == content {
					t.Fatalf("变异体与原文一字不差 ⇒ 靶子没打掉（这一枚反证无效）")
				}
				problems := caddySecurityWiring("variant.conf", variant)
				if len(problems) == 0 {
					t.Fatalf("打掉『%s』之后这条锁**一行问题都没出** ⇒ 射程是空气，绿灯是假的", c.name)
				}
				var joined string
				for _, p := range problems {
					joined += p + "\n"
				}
				if !strings.Contains(joined, c.want) {
					t.Fatalf("问题清单没点到预期的那条腿 %s（实际：\n%s）⇒ 抓到的是别的事，反证无效", c.want, joined)
				}
			})
		}
	}
}
