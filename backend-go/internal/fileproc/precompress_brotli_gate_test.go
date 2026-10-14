// ============================================================================
// precompress_brotli_gate_test.go — ★ 2026-10-14（现网白屏事故）把
// 「预压缩侧车必须能被浏览器解开」这条修法接进自动化闸门。
//
// 因果链（为什么这一条非加机械锁不可）：
//
//	scripts/precompress_dist.mjs 里那行 `[zlibConstants.BROTLI_PARAM_LARGE_WINDOW]: 1`
//	让 brotli 流头写进 WBITS>24（超大窗口）。**浏览器解码器把窗口上限钉在 16 MiB**，
//	于是 Edge/Chrome 拿到侧车直接判 `ERR_CONTENT_DECODING_FAILED`：
//	`<script type="module">` 这一步载入失败 ⇒ React 根本没挂载 ⇒ 整站纯黑、标签正常、零提示。
//	用户两次带截图报「edge 还是没打开」就是这个形态。
//
//	为什么既有闸门一条都没红（这一族的坏从来不是"报错"，是"没有症状"）：
//	① 侧车**照样存在**、照样**比原文小得多**（那一代 160,719 B 对 608,625 B），
//	   T1／precompress --check 这两道按"在不在、多大、内容对不对"判的档全部通过；
//	② gzip 与原文两档回落链完全正常，只有 br 这一档坏，而 Caddy 会按 Accept-Encoding 优先给 br；
//	③ vite build／前端单测／后端单测问的都是源码与字节，不是"客户端能不能解开"。
//	⇒ 修法有两层，缺任何一层都会复发：
//	   第一层＝压缩出口**自己解回来逐字节等值**（scripts/precompress_dist.mjs 的 decodeOrThrow），
//	   它让"浏览器解不开"在落盘那一刻就变成异常而不是静默产物；
//	   第二层＝交付包侧再判一次（check_frontend_tar.sh 的 T6／T6b），
//	   因为它判的是**最终要推上去的那批文件**，与脚本当天跑没跑过无关。
//	⚠️ 参数本身不是判据，**解码往返才是**：将来若有人为某个超大资产重新抬窗口，
//	   只要 Node 能解、浏览器不能解，仍然只有"浏览器侧上限"这一维能抓住它——
//	   所以这条锁同时钉「参数没写回来」与「往返腿还在」，两条都不能少。
//
// 三条设计（对齐仓内既有口径）：
//
//	① 判据抽成纯函数，仓库真文件与反证变异副本走**同一个**函数（AGENTS §三「判据级代替不了守卫级」的反面教材）；
//	② 判据先剥整行注释：解释这次事故的注释**本身就写着** BROTLI_PARAM_LARGE_WINDOW 这个字样
//	   （记忆「static negative locks」那一族——负向 grep 会被自己新写的说明注释再犯）；
//	③ 每条负向腿都配一条**正向对照**（同一份文本里必须认得出压缩/解码的正常形态），
//	   否则"文件搬家/整段没读到"也会被判成"没问题"。
//
// 本文件**只读仓库文本**，绝不 ssh、绝不碰现网；现网那一层由
// deploy/postdeploy_*.sh 的公网段（br/gzip/identity 三档各拨一次）负责。
// ============================================================================
package fileproc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 两份射程内文件：压缩器本体＋交付包自检。两条链任一退化都会把事故带回现网。
const (
	precompressScriptRel = "scripts/precompress_dist.mjs"
	precompressTarChkRel = "scripts/check_frontend_tar.sh"
)

// 判据靶子字面量——与脚本里写的**逐字符同源**，改名式破坏必须当场红。
const (
	// 负向靶：这一行写回来，侧车就会再次变成"浏览器解不开但体积很漂亮"的产物。
	pbLargeWindowParam = "BROTLI_PARAM_LARGE_WINDOW"
	// 正向靶（压缩器侧）：三件事缺一不可——两种解码器都被调用、逐字节等值比过。
	pbBrotliCompress = "brotliCompressSync("
	pbBrotliDecode   = "brotliDecompressSync("
	pbGunzip         = "gunzipSync("
	pbBufferCompare  = "Buffer.compare("
	pbDecodeOrThrow  = "decodeOrThrow(out, buf, fmt)" // 只数**调用点**：函数定义那一行也含 decodeOrThrow(，算进来会让"少一档往返"仍计数达标
	pbCompressBuf    = "function compressBuf("
	// 正向靶（交付包侧）：T6 真解码 + T6b 用同一函数自证射程（好侧车放行／坏侧车判死）。
	pbTarT6Decode   = "brotliDecompressSync("
	pbTarT6SelfGood = "T6_GOOD=$?"
	pbTarT6SelfBad  = "T6_BAD=$?"
	pbTarT6FailMark = "BAD  T6 "
	pbTarT6BMark    = "T6b 解码判据自证"
)

// precompressStripComments 剥掉**整行注释**（JS 的 // 与 shell 的 #，含缩进行）。
// 行内尾注释刻意不处理：射程内这几条靶子没有一条写在行尾，真写在那儿也算命中（宁多判不放过）。
func precompressStripComments(content string) string {
	var out []string
	for _, l := range strings.Split(content, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// precompressCompressorLegs 判压缩器脚本（scripts/precompress_dist.mjs）。返回空＝合格。
//
//	P1 大窗口参数不许出现在代码行（注释里写它＝记录事故，允许）；
//	P2 两种格式的解码往返都必须在位（br 与 gz 一侧都不能只压不验——只验 br 会把 gzip 侧的
//	   同类缺陷放出去，而 Caddy 的回落链是 br→gzip→identity，任何一档坏都算半条链坏）；
//	P3 解码后的**逐字节等值**必须比过（只 catch 异常不够：能解开但不等于原文＝投错内容，
//	   现网表现是"界面正常但脚本是旧的"这类更难查的形态）；
//	P4 compressBuf 这个唯一出口必须在位，且两个格式各调一次 decodeOrThrow
//	   （往返一旦被挪出 compressBuf，--check 模式就不再复算压缩 ⇒ 判据退化成只比对字节）。
func precompressCompressorLegs(rel, raw string) []string {
	var problems []string
	code := precompressStripComments(raw)

	// 正向对照先做：这份文件若压根不是那个压缩器（搬家／改名／读空），
	// 后面三条负向与往返判据全部会因为"什么都没找到"而通过 ⇒ 必须当场判死。
	if !strings.Contains(code, pbBrotliCompress) || !strings.Contains(code, pbCompressBuf) {
		problems = append(problems, rel+": 认不出压缩器本体（"+pbBrotliCompress+"／"+pbCompressBuf+" 任一缺失）⇒ 文件搬家或判据写漂，负向腿此刻没有射程，不许读成通过")
	}
	if strings.Contains(code, pbLargeWindowParam) {
		problems = append(problems, rel+": 代码行里出现 "+pbLargeWindowParam+"（P1）⇒ 流头 WBITS 超过浏览器 16 MiB 上限，Edge/Chrome 判 ERR_CONTENT_DECODING_FAILED，整站白屏")
	}
	if !strings.Contains(code, pbBrotliDecode) {
		problems = append(problems, rel+": 没有 brotli 解码往返（P2）⇒ 压完不自证，坏侧车静默落盘")
	}
	if !strings.Contains(code, pbGunzip) {
		problems = append(problems, rel+": 没有 gzip 解码往返（P2）⇒ br→gzip 回落链的另一档没人验")
	}
	if !strings.Contains(code, pbBufferCompare) {
		problems = append(problems, rel+": 没有逐字节等值比对（P3）⇒ 只 catch 异常挡不住「能解开但不是原文」")
	}
	if n := strings.Count(code, pbDecodeOrThrow); n < 2 {
		problems = append(problems, rel+": decodeOrThrow 只被调 "+itos(n)+" 次（P4，应 ≥2＝br 与 gz 各一次）⇒ 某一档的压缩结果不再自证")
	}
	return problems
}

// precompressTarCheckLegs 判交付包自检（scripts/check_frontend_tar.sh）。返回空＝合格。
//
//	T6 档必须真在包里跑解码（这一档判的是**最终推上去的那批文件**，与压缩器当天跑没跑过无关）；
//	T6b 射程自证必须在位：只留"坏的判死"不够——若整段压根没走到（root 传错／node 不在），
//	   坏的也判不死，那才是最难发现的空转锁。好侧车必须先放行，才证明这一档真读过文件。
func precompressTarCheckLegs(rel, raw string) []string {
	var problems []string
	code := precompressStripComments(raw)

	if !strings.Contains(code, pbTarT6Decode) {
		problems = append(problems, rel+": 包级自检里没有侧车解码腿（T6）⇒ 「在位且比原文小」判不出「浏览器解不开」")
	}
	if !strings.Contains(code, pbTarT6FailMark) {
		problems = append(problems, rel+": T6 失败分支没往 RC 上记 BAD ⇒ 判了红也不影响退码，等于这条档恒过")
	}
	if !strings.Contains(code, pbTarT6SelfGood) || !strings.Contains(code, pbTarT6SelfBad) {
		problems = append(problems, rel+": 缺 T6b 射程自证（T6_GOOD／T6_BAD 任一不见）⇒ 无法区分「判死了坏侧车」与「这一档压根没跑」")
	}
	if !strings.Contains(code, pbTarT6BMark) {
		problems = append(problems, rel+": T6b 的读数行缺失 ⇒ 自证腿被摘而主判据还在，正是本档最容易被「顺手简化」掉的形态")
	}
	return problems
}

// TestPrecompressSidecarMustBeBrowserDecodable 逐文件判上述各档，并给每档配一枚反证。
func TestPrecompressSidecarMustBeBrowserDecodable(t *testing.T) {
	repo := findDispatchRepoRoot(t)
	cases := []struct {
		rel   string
		check func(string, string) []string
	}{
		{precompressScriptRel, precompressCompressorLegs},
		{precompressTarChkRel, precompressTarCheckLegs},
	}
	contents := map[string]string{}
	for _, c := range cases {
		b, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(c.rel)))
		if err != nil {
			t.Fatalf("读 %s 失败：%v ⇒ 交付链的文件搬家了，本闸门要跟着改路径，不许改成 skip", c.rel, err)
		}
		contents[c.rel] = string(b)
		if problems := c.check(c.rel, string(b)); len(problems) > 0 {
			for _, p := range problems {
				t.Errorf("现网白屏闸门（%s）：%s", c.rel, p)
			}
		}
	}

	// ---- 反证：每枚都喂**同一个**判据函数，且各自只点破自己那条腿 ----
	t.Run("反证", func(t *testing.T) {
		comp := contents[precompressScriptRel]

		// M1 把大窗口参数装回代码行（事故原始形态）⇒ 只有 P1 红。
		m1 := strings.Replace(comp,
			"  [zlibConstants.BROTLI_PARAM_QUALITY]: 11,",
			"  [zlibConstants.BROTLI_PARAM_LARGE_WINDOW]: 1,\n  [zlibConstants.BROTLI_PARAM_QUALITY]: 11,", 1)
		if m1 == comp {
			t.Fatal("反证 M1 夹具没替换成功＝靶子写漂了")
		}
		got := precompressCompressorLegs(precompressScriptRel, m1)
		if len(got) != 1 || !strings.Contains(strings.Join(got, "\n"), pbLargeWindowParam) {
			t.Errorf("M1 应当且只当点破 P1，实读 %+v", got)
		}

		// M2 把 br 那一档的往返摘掉（只压不验）⇒ P4 计数腿必须红。
		m2 := strings.Replace(comp, "    return decodeOrThrow(out, buf, fmt)\n", "    return out\n", 1)
		if m2 == comp {
			t.Fatal("反证 M2 夹具没替换成功＝decodeOrThrow 那一行的形状变了")
		}
		if got := precompressCompressorLegs(precompressScriptRel, m2); len(got) == 0 {
			t.Error("M2 摘掉 br 档往返后仍判绿 ⇒ P4 这条计数腿没有射程")
		}

		// M3 连 Buffer.compare 一起抹掉（只剩"能抛就抛"）⇒ 等值比对腿必须红。
		m3 := strings.Replace(comp, "if (Buffer.compare(back, original) !== 0) {", "if (false) {", 1)
		if m3 == comp {
			t.Fatal("反证 M3 夹具没替换成功＝等值比对那一行写漂了")
		}
		if got := precompressCompressorLegs(precompressScriptRel, m3); len(got) == 0 ||
			!strings.Contains(strings.Join(got, "\n"), "P3") {
			t.Errorf("M3 抹掉逐字节等值后必须点破 P3，实读 %+v", got)
		}

		tar := contents[precompressTarChkRel]
		// M4 把 T6 的解码调用整段摘掉（「只数侧车在不在」的旧形态）⇒ T6 腿红。
		m4 := strings.Replace(tar, "z.brotliDecompressSync(enc)", "enc", 1)
		if m4 == tar {
			t.Fatal("反证 M4 夹具没替换成功＝T6 解码那一行写漂了")
		}
		if got := precompressTarCheckLegs(precompressTarChkRel, m4); len(got) == 0 {
			t.Error("M4 摘掉包级解码腿后仍判绿 ⇒ T6 这一档没有射程")
		}
		// M5 只摘射程自证（T6_GOOD 那条腿不见）⇒ 必须红：
		//    这一枚证明的是"有主判据不等于有射程"，同 AGENTS §三 那条"新读数腿要双态自证"。
		m5 := strings.Replace(tar, "node -e \"$T6_JS\" \"$T6_SELF\" >/dev/null 2>&1; T6_GOOD=$?", ": # T6b 的真读数被换成常量（自证腿没跑）", 1)
		if m5 == tar {
			t.Fatal("反证 M5 夹具没替换成功＝T6b 自证那段写漂了")
		}
		if got := precompressTarCheckLegs(precompressTarChkRel, m5); len(got) == 0 {
			t.Error("M5 把 T6b 的真读数换成常量后仍判绿 ⇒ 自证腿被摘也抓不到")
		}
	})
}
