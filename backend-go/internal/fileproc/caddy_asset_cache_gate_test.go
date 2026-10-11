// ============================================================================
// caddy_asset_cache_gate_test.go — ★ 2026-10-12（慢链路批·现网事故收尾）把
// 「静态资源长缓存只给真文件、错误响应一律不可缓存」这条修法接进自动化闸门。
//
// 因果链（为什么这一批必须给它加一条锁，而不是"线上改好了就算"）：
//
//	10-12 换源后主站与演示站的入口 JS 在公网上恒 404：源站磁盘上那个文件在、`?cb=1` 那条腿
//	200／605,065 B 完好，坏的是**边缘缓存**——`/assets/*` 段里那行
//	`header Cache-Control "public, max-age=31536000, immutable"` 是**无条件**发的，
//	于是我在落盘之前拿新文件名做的那一次探针（必然 404）也被贴上"一年不可变"，
//	被 Cloudflare 钉住：`cf-cache-status: HIT`、age 7000+ 秒仍在回 404。
//	真实客户侧表现＝**整站首屏脚本 404**，而源站、构建、单测、界面全部"看起来是好的"。
//
//	修法两半（三份配置各写一遍，仓库与线上同源）：
//	① `@asset file {http.request.uri.path}` ⇒ 长缓存只在**磁盘上真有这个文件**时才发；
//	② `handle_errors { @asseterr path /assets/*; header @asseterr Cache-Control "no-store" }`
//	   ⇒ 这一族的错误页一律不可缓存。
//	⚠️ ②不是锦上添花：实测源站对 404 **不带任何** cache-control 时，CF 仍按自己的默认档钉
//	   4 小时（回吐 `cache-control: max-age=14400`＋HIT）；只有显式 no-store 才翻成 BYPASS。
//	⚠️ 为什么不用最直觉的 `status 200` 匹配器：现网 Caddy v2.9.1 **没有** `http.matchers.status`，
//	   adapt 期直接报 `module not registered`（本批隔离实例现读）。所以这两档是唯一走得通的形态，
//	   而"唯一走得通的形态"最容易被下一次好心重写打回 `status 200` 或无条件档 ⇒ 需要机械锁。
//
// 三条设计（对齐仓内既有口径）：
//
//	① 判据抽成**纯函数** caddyAssetCacheWiring()，仓库里的每一份射程内配置和反证用的变异副本
//	   都走同一个函数（AGENTS §三「判据级断言代替不了守卫级」）。
//	② 三份配置里 `deploy/caddy/*.conf` 与 `scripts/bootstrap-demo.sh` 是**两条独立生成链**
//	   （演示站那份 conf 的事实源是脚本），所以射程按"哪些文件里有 /assets/* 段"**派生**，
//	   不写死名单：新增一份带 assets 段的配置就自动进射程，删掉 assets 段则整条锁出声。
//	③ 判据先剥注释行：这三份文件里解释这个事故的注释**本身就写着**那两行指令的字面形态
//	   （负向锁被说明注释误伤是本仓反复踩过的一族，见记忆「static negative locks」）。
//
// 本文件**只读仓库文本**，绝不 ssh、绝不碰现网；现网那一层由
// deploy/postdeploy_20261012.sh 的 F 段（源站直读＋公网各三档）负责。
// ============================================================================
package fileproc

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// caddyAssetScopeGlobs 射程的派生入口：这两处任一出现 `/assets/*` 托管段就进射程。
// 为什么不但 glob conf 还单独点名 bootstrap-demo.sh：演示站的 Caddyfile 不落仓库文件，
// 它是那个脚本**生成**出来的（只改线上＝下次 bootstrap 写回旧形态）。
var caddyAssetScopeGlobs = []string{
	"deploy/caddy/*.conf",
	"scripts/bootstrap-demo.sh",
}

// caddyAssetHandleOpen 托管段的开场白（判据用它数"这个文件里有几段 /assets/*"）。
const caddyAssetHandleOpen = "handle /assets/* {"

// 四条判据的靶子字面量——与配置里写的**逐字符同源**，改名式破坏必须当场红。
const (
	caddyAssetImmutableHeader = `Cache-Control "public, max-age=31536000, immutable"`
	caddyAssetFileMatcher     = "@asset file {http.request.uri.path}"
	caddyAssetErrMatcher      = "@asseterr path /assets/*"
	caddyAssetErrNoStore      = `header @asseterr Cache-Control "no-store"`
	caddyHandleErrorsOpen     = "handle_errors {"
)

// caddyNonCommentLines 剥掉**整行注释**后的代码行（缩进的 # 也算）。
// Caddyfile 与 shell 都用 # 起注释，这里一刀切；行内 # 不处理——
// 射程内这四条靶子没有一条写在行尾注释里，真写了也算它命中（宁可多判不静默放行）。
func caddyNonCommentLines(content string) []string {
	var out []string
	for _, l := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		out = append(out, l)
	}
	return out
}

// caddyAssetCacheWiring 判一份配置里的 /assets/* 托管段是不是"两半修法都在"的形态。
// 返回空切片＝成立。四档各管一条真实的断链方式：
//
//	L1 出现**无条件**的 immutable 头（＝本次事故的原始形态）⇒ 一次探针就能把 404 在边缘钉一年；
//	L2 `@asset file` 存在性匹配器的份数 ≠ 托管段份数 ⇒ 某一段没被条件化；
//	L3 `header @asset` 那行不见/份数不符 ⇒ 有人把匹配器摘了但把长缓存留着（等价于 L1，
//	   但形态不同：它是"看着像修过、实际还是无条件发"，最容易在重构里长出来）；
//	L4 handle_errors／@asseterr／no-store 三条腿份数不符 ⇒ 修法②缺位，
//	   表现是 404 从"钉一年"退回"钉 4 小时"（源站不带 cache-control 时 CF 自己会加档）。
//
// 托管段份数 A＝0 的文件**不在射程**（由调用方过滤，A 的派生本身就是射程定义），
// 但 A>0 时任何一档不符即记问题——不许"段在、修法不在"静默通过。
func caddyAssetCacheWiring(rel string, content string) []string {
	var problems []string
	lines := caddyNonCommentLines(content)
	joined := strings.Join(lines, "\n")

	a := strings.Count(joined, caddyAssetHandleOpen)
	if a == 0 {
		return nil
	}

	// L1：无条件档＝`header` 之后**直接**跟 immutable 字面量（中间没有 @asset）。
	// 逐行判而不是整段正则：这样"匹配器写在另一行"的花样不会被误判成无条件。
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "header ") && strings.Contains(t, caddyAssetImmutableHeader) && !strings.Contains(t, "@asset") {
			problems = append(problems, rel+": 出现无条件长缓存行「"+t+"」（L1）⇒ 404 也会带 immutable 进边缘缓存，本批现网事故就是这个形态")
		}
	}
	if n := strings.Count(joined, caddyAssetFileMatcher); n != a {
		problems = append(problems, rel+": @asset 存在性匹配器 "+itos(n)+" 处 / 托管段 "+itos(a)+" 段（L2）⇒ 有一段没被条件化")
	}
	if n := strings.Count(joined, "header @asset "+caddyAssetImmutableHeader); n != a {
		problems = append(problems, rel+": 「header @asset …immutable」"+itos(n)+" 处 / 托管段 "+itos(a)+" 段（L3）⇒ 看着像修过，实际没把长缓存挂在匹配器上")
	}
	if !strings.Contains(joined, caddyHandleErrorsOpen) {
		problems = append(problems, rel+": 没有 handle_errors 段（L4）⇒ 错误响应由 CF 按默认档缓存（实测 4 小时 HIT）")
	}
	if n := strings.Count(joined, caddyAssetErrMatcher); n != a {
		problems = append(problems, rel+": @asseterr 匹配 "+itos(n)+" 处 / 托管段 "+itos(a)+" 段（L4）⇒ /assets 的错误腿没盖全")
	}
	if n := strings.Count(joined, caddyAssetErrNoStore); n != a {
		problems = append(problems, rel+": no-store 那行 "+itos(n)+" 处 / 托管段 "+itos(a)+" 段（L4）⇒ 错误页仍可被边缘缓存")
	}
	return problems
}

// itos 小工具：判据里的份数读数要能拼进问题行。
// 单独包一层是为了**统一口径**（三档判据都写「n / 段数 a」这种形态），
// 而不是为了省一次 import——直接用 strconv 也行，但那样三处字符串拼接会各写各的。
func itos(n int) string { return strconv.Itoa(n) }

// caddyAssetScopedFiles 按"文件里有没有 /assets/* 托管段"**派生**射程，不写死名单。
// 一份都没有就 Fatal：要么配置搬家了，要么托管段被删了——两者都要人来同步改这条锁。
func caddyAssetScopedFiles(t *testing.T) map[string]string {
	t.Helper()
	repo := findDispatchRepoRoot(t)
	seen := map[string]string{}
	dirs := []string{"deploy/caddy", "scripts"}
	for _, d := range dirs {
		base := filepath.Join(repo, d)
		entries, err := os.ReadDir(base)
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
			if strings.Contains(string(b), caddyAssetHandleOpen) {
				seen[rel] = string(b)
			}
		}
	}
	if len(seen) == 0 {
		t.Fatalf("射程内（%v）一个带 %q 段的文件都没有 ⇒ 配置搬家或托管段被删，两者都要改这条锁，不许静默通过",
			caddyAssetScopeGlobs, caddyAssetHandleOpen)
	}
	return seen
}

// caddyInScopePath 只收两类：deploy/caddy 下的 .conf、以及演示站配置的事实源脚本。
func caddyInScopePath(rel string) bool {
	if rel == "scripts/bootstrap-demo.sh" {
		return true
	}
	if strings.HasPrefix(rel, "deploy/caddy/") && strings.HasSuffix(rel, ".conf") {
		return true
	}
	return false
}

// TestCaddyAssetCacheHeadersAreConditional 对每一份射程内配置跑同一把尺子。
func TestCaddyAssetCacheHeadersAreConditional(t *testing.T) {
	scoped := caddyAssetScopedFiles(t)
	var problems []string
	for rel, content := range scoped {
		problems = append(problems, caddyAssetCacheWiring(rel, content)...)
	}
	if len(problems) > 0 {
		t.Errorf("/assets/* 的缓存头修法不成（每一行都是一条独立断链）：\n  %s\n"+
			"要写的形态：%s ＋「header @asset %s」，再配 %s 段里的「%s」＋「%s」",
			strings.Join(problems, "\n  "),
			caddyAssetFileMatcher, caddyAssetImmutableHeader,
			caddyHandleErrorsOpen, caddyAssetErrMatcher, caddyAssetErrNoStore)
	}
	t.Logf("射程内配置 %d 份（%v），两半修法全部成立", len(scoped), caddyAssetScopeGlobs)
}

// TestCaddyAssetCacheGateCounterproof 反证三枚（全在内存副本上跑，仓库原件一字不动）：
//
//	M1 把「header @asset …」写回**无条件**档（＝本次事故的原始形态）⇒ L1 必须出声；
//	M2 摘掉 @asset 存在性匹配器那一行（＝"匹配器没了、长缓存还挂着"）⇒ L2/L3 必须出声；
//	M3 摘掉整个 handle_errors 段（＝只修了一半，404 退回被 CF 钉 4 小时）⇒ L4 必须出声。
//
// 每一枚都必须让**同一个判据函数**出声，且原件作为对照腿必须先证明"在全清状态"，
// 否则变异体的红说明不了任何事（AGENTS §三 反证打在靶子上）。
func TestCaddyAssetCacheGateCounterproof(t *testing.T) {
	scoped := caddyAssetScopedFiles(t)
	cases := []struct {
		name string
		mut  func(string) string
		want string
	}{
		{
			name: "M1 写回无条件 immutable（事故原始形态）",
			mut: func(s string) string {
				return strings.Replace(s, "header @asset "+caddyAssetImmutableHeader,
					"header "+caddyAssetImmutableHeader, 1)
			},
			want: "L1",
		},
		{
			name: "M2 摘掉 @asset 存在性匹配器",
			// 按**行内容**摘而不是按固定缩进：三份配置的缩进不同（站点块 2 tab、
			// 生成脚本里的块也是 2 tab，但一旦有人重排缩进，写死 "\t…" 的靶子就命中 0 次，
			// 反证会在"没打掉靶子"那条 Fatal 上出声——那是自伤，不是这条锁有射程）。
			mut: func(s string) string {
				return dropLineContaining(s, caddyAssetFileMatcher)
			},
			want: "L2",
		},
		{
			name: "M3 摘掉整个 handle_errors 段",
			mut: func(s string) string {
				return dropHandleErrorsBlock(s)
			},
			want: "L4",
		},
	}
	for rel, content := range scoped {
		rel, content := rel, content
		// 对照腿：原件必须全清。
		if base := caddyAssetCacheWiring(rel, content); len(base) != 0 {
			t.Fatalf("%s 原件在这条尺子下就不成（%v）⇒ 对照腿失效", rel, base)
		}
		for _, c := range cases {
			c := c
			t.Run(rel+"/"+c.name, func(t *testing.T) {
				variant := c.mut(content)
				if variant == content {
					t.Fatalf("变异体与原文一字不差 ⇒ 靶子没打掉（这一枚反证无效）")
				}
				problems := caddyAssetCacheWiring("variant.conf", variant)
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

// dropLineContaining 摘掉**第一条**含该字面量且不是注释的行。
// 为什么排除注释行：这几份配置的解释性注释里就带着被摘对象的字面量（负向锁误伤注释
// 是本仓反复踩过的一族），按"第一条命中"删的话变异体可能只是删掉了一行注释，
// 判据照样全清 ⇒ 反证变成"打了但没打掉靶子"。
func dropLineContaining(s, marker string) string {
	lines := strings.Split(s, "\n")
	var out []string
	dropped := false
	for _, l := range lines {
		if !dropped && strings.Contains(l, marker) && !strings.HasPrefix(strings.TrimSpace(l), "#") {
			dropped = true
			continue
		}
		out = append(out, l)
	}
	if !dropped {
		// 原样返回，让调用方那条"变异体与原文一字不差"的 Fatal 出声（反证无效必须红）。
		return s
	}
	return strings.Join(out, "\n")
}

// dropHandleErrorsBlock 按**大括号配平**摘掉 handle_errors 整段。
// 为什么不用固定缩进的字符串替换：这三份配置里该段的缩进不同（站点块 vs 生成脚本里的块），
// 写死 "\thandle_errors {" 会对其中一份命中 0 次——反证就打偏了（本仓抓过同形自伤）。
func dropHandleErrorsBlock(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		if strings.Contains(strings.TrimSpace(lines[i]), caddyHandleErrorsOpen) {
			depth := strings.Count(lines[i], "{") - strings.Count(lines[i], "}")
			for depth > 0 && i+1 < len(lines) {
				i++
				depth += strings.Count(lines[i], "{") - strings.Count(lines[i], "}")
			}
			continue
		}
		out = append(out, lines[i])
	}
	return strings.Join(out, "\n")
}
