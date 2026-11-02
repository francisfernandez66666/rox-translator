// ============ swap_cf_purge_gate_test.go · 职责说明 ============
// CF purge 腿的**形态锁**（2026-10-29 第十一轮）。
//
// 为什么要有它：T8d（「第一枪 000、第二枪通 ⇒ 重拨会救」）的射程完全取决于两件事——
//  1. 执行体把 curl 失败读成「这一跳没走完」（旧写法 `|| echo 000` + `[ "$code" = "000" ]`
//     在真 curl 失败时读到 "000000"，重拨永不触发）；
//  2. 桩的失败形态与真 curl 同形（打 000 **且** 退出码非零；桩 `exit 0` 会让 T8d 在坏执行体上假绿）。
//
// 这两件事**各改一处**就能让 T8d 常绿 ⇒ 一条行为腿（T8d）配两条形态腿（C1/C2），任一侧退化当场红。
// =============================================
package fileproc

import (
	"strings"
	"testing"
)

// shellFuncBody 抠出 shell 函数体（`name() {` 到配对的 `}`），按花括号配平切。
//
// 括号在 `${…}` 与 JSON 字面量里都是成对的，配平不受影响；找不到即返回 ("", false)，
// 调用方按「尺子不见了」判红，**不许**当成合规。
func shellFuncBody(src, header string) (string, bool) {
	i := strings.Index(src, header)
	if i < 0 {
		return "", false
	}
	rest := src[i:]
	j := strings.Index(rest, "{")
	if j < 0 {
		return "", false
	}
	depth := 0
	for k := j; k < len(rest); k++ {
		switch rest[k] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return rest[:k+1], true
			}
		}
	}
	return "", false
}

// stripShellComments 剥掉 shell 行注释（`#` 到行尾），单/双引号内的 `#` 不当注释。
//
// 为什么必须先剥：本函数体的说明注释里就写着旧写法 `|| echo 000`；不剥的话负向腿会被
// **自己的注释**喂饱（本仓老坑：注释冒充实现，反证与正向一起假红/假绿）。
func stripShellComments(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		inSingle, inDouble := false, false
		cut := len(line)
		for i := 0; i < len(line); i++ {
			switch line[i] {
			case '\'':
				if !inDouble {
					inSingle = !inSingle
				}
			case '"':
				if !inSingle {
					inDouble = !inDouble
				}
			case '#':
				if !inSingle && !inDouble && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
					cut = i
					i = len(line)
				}
			}
		}
		b.WriteString(line[:cut])
		b.WriteString("\n")
	}
	return b.String()
}

// cfAPIProblems 返回 cf_api 函数体的形态问题（空＝合规）。判之前先剥注释。
func cfAPIProblems(body string) []string {
	body = stripShellComments(body)
	var out []string
	if !strings.Contains(body, "|| rc=$?") {
		out = append(out, "cf_api 没把 curl 的退出码收下来（`|| rc=$?`）⇒ 真失败时读不到「这一跳没走完」")
	}
	if strings.Contains(body, "|| echo 000") {
		out = append(out, "cf_api 里还有 `|| echo 000`：真 curl 失败时 -w 已打 000，再补一个就是 000000 ⇒ 重拨永不触发")
	}
	if !strings.Contains(body, "000*") {
		out = append(out, "cf_api 的归一没按前缀认 000（`000*`）⇒ 000000 这种形态仍漏判")
	}
	return out
}

// C1：执行体形态 —— curl 退出码必须被收下来，旧的「再补一个 000」必须清零。
func TestSwapCfPurgeRetryShape(t *testing.T) {
	src := readProbeScript(t, "swap_web_and_binary.sh")
	body, ok := shellFuncBody(src, "cf_api() {")
	if !ok {
		t.Fatalf("找不到 cf_api 函数体 ⇒ 这条锁此刻没有输入，是空转的")
	}
	if ps := cfAPIProblems(body); len(ps) > 0 {
		t.Fatalf("CF purge 重拨腿形态不合规：%s", strings.Join(ps, "；"))
	}
	// 反证一：旧形态必须被同一判据判死（判据有射程的自证）。
	if ps := cfAPIProblems("  curl \"${args[@]}\" \"$url\" 2>/dev/null || echo 000\n"); len(ps) == 0 {
		t.Fatalf("判据自证不过：旧形态喂进去竟然判绿")
	}
	// 反证二：空函数体必须判死（「尺子丢了」与「这次很干净」不能同形）。
	if ps := cfAPIProblems(""); len(ps) == 0 {
		t.Fatalf("判据自证不过：空函数体判绿")
	}
	// 反证三：只在注释里写齐形态、正文没有 ⇒ 必须判死（注释不算实现）。
	if ps := cfAPIProblems("# || rc=$? 000*\n  curl x\n"); len(ps) == 0 {
		t.Fatalf("判据自证不过：注释里的形态被当成实现")
	}
}

// C2：桩的失败形态 —— 两处失败分支都必须「打 000 + 非零退出」，且不许再出现 exit 0。
func TestSwapCfPurgeStubFailureShape(t *testing.T) {
	src := readProbeScript(t, "swap_web_and_binary_selftest.sh")
	if n := strings.Count(src, "echo 000; exit 7"); n != 2 {
		t.Fatalf("桩的失败分支应是 2 处 `echo 000; exit 7`，实读 %d ⇒ 有一处还是 exit 0（T8d 会假绿）", n)
	}
	if strings.Contains(src, "echo 000; exit 0") {
		t.Fatalf("桩里还有 `echo 000; exit 0`：真 curl 失败是**非零退出**，这样写测不到执行体的缺陷")
	}
}

// cfTailProblems 返回 cf_purge_tail 的「阻塞」问题（空＝合规）。
// 判据只问一件事：函数体里有没有 `bad` 调用（那会把一次 purge 抖动升级成整次发版判死，
// 而此刻 web 已落位 ⇒ 留下「web 新 + bin 旧」的混合半态）。先剥注释，防说明文字喂饱负向腿。
func cfTailProblems(body string) []string {
	clean := stripShellComments(body)
	if strings.Contains(clean, "bad \"") || strings.Contains(clean, " bad ") {
		return []string{"cf_purge_tail 里出现 bad：尾随 purge 必须非阻塞（此刻已落位）"}
	}
	return nil
}

// C4：尾随 purge 非阻塞 + 接在 commit_site 之后。
func TestSwapCfPurgeTailIsNonBlocking(t *testing.T) {
	src := readProbeScript(t, "swap_web_and_binary.sh")
	body, ok := shellFuncBody(src, "cf_purge_tail() {")
	if !ok {
		t.Fatalf("找不到 cf_purge_tail 函数体 ⇒ 尾随 purge 没落地")
	}
	if ps := cfTailProblems(body); len(ps) > 0 {
		t.Fatalf("%s", strings.Join(ps, "；"))
	}
	commit := strings.Index(src, `for d in $DEPLOY_DIRS; do commit_site `)
	call := strings.LastIndex(src, "cf_purge_tail")
	if commit < 0 || call < 0 || call < commit {
		t.Fatalf("尾随 purge 没接在 commit_site 循环之后（加函数不接线＝白修）")
	}
	// 反证：带 bad 的函数体必须判死；干净函数体必须判绿。
	if ps := cfTailProblems("  bad \"x\"\n"); len(ps) == 0 {
		t.Fatalf("判据自证不过：带 bad 的尾随 purge 竟然判绿")
	}
	if ps := cfTailProblems("  echo \"CF_PURGE_TAIL=ok\"\n"); len(ps) > 0 {
		t.Fatalf("判据自证不过：干净函数体被判红")
	}
}

// tokenSourceProblems 返回「purge 凭据来源」这一层的形态问题（空＝合规）。
//
// 判的是 2026-11-02「每次发版都真清」那一条：把"真清"从**人记得带 env** 改成
// **执行体所在那台机器上的一份 600 文件**，于是它必须同时满足六件事——
//
//	R1 只有一个解析口，且 env 优先于文件（顺序就是优先级，写反了文件会盖掉临时凭据）；
//	R2 两个调用点都不许自带凭据短路（各自判一次＝第二把尺子：一处走文件、一处只认 env，
//	   表现是"尾随那一枪永远 skipped 而屏幕上看不出来"）；
//	R3 四种"清没清"在屏幕上必须不同形（ok／skipped／off／rejected）——
//	   把"忘了带凭据"与"凭据文件权限不合格"混成一行，就是把事故读成常态；
//	R4 token 本体一律不许进屏幕（含它的变量不许被 echo／拼进读数行）；
//	R5 属主判据必须三档都放行（root／本进程 uid／部署根 data 目录的属主＝服务账号）——
//	   只认 root 会让"root 跑发版、而 0600 投影是服务进程写的"这个**常规形态**永远 rejected，
//	   常规形态坏了＝这条腿在现网恒空转（屏幕读数与"没配凭据"不同形，但同样清不了缓存）；
//	R6 成功读数必须带 from= 标签（来源是对外排障契约，同 §一·3 那条 `from` 档位）。
//
// 判之前一律先剥注释：函数头注释里逐字写着旧写法与"只认 root"这类反面词，不剥就被自己喂饱。
func tokenSourceProblems(resolveBody, headBody, tailBody, acceptLine, tailOkLine string) []string {
	var out []string
	rc := stripShellComments(resolveBody)
	hc := stripShellComments(headBody)
	tc := stripShellComments(tailBody)
	if rc == "" || hc == "" || tc == "" {
		return append(out, "读不到 resolve_cf_token／cf_purge／cf_purge_tail 的函数体（判据此刻没有输入，是空转的）")
	}
	// R1 env 先、文件后
	iEnv := strings.Index(rc, `CF_PURGE_FROM="env"`)
	iFile := strings.Index(rc, `head -n 1 "$CF_TOKEN_FILE"`)
	if iEnv < 0 || iFile < 0 {
		out = append(out, "解析口读不到 env 档或文件档（其中一档没了＝来源退化回单一 env 或单一文件）")
	} else if iEnv > iFile {
		out = append(out, "env 档排在文件档之后 ⇒ 优先级写反，临时凭据会被磁盘上那份盖掉")
	}
	// R2 两个调用点都必须经解析口
	for name, body := range map[string]string{"cf_purge": hc, "cf_purge_tail": tc} {
		if !strings.Contains(body, "resolve_cf_token") {
			out = append(out, name+" 没走 resolve_cf_token ⇒ 第二把尺子（凭据来源在两个调用点各判一次）")
		}
	}
	if strings.Contains(hc, `[ -z "${CLOUDFLARE_API_TOKEN:-}" ];`) {
		out = append(out, `cf_purge 里还有裸的 [ -z "${CLOUDFLARE_API_TOKEN:-}" ]; 短路 ⇒ 文件源永远读不到`)
	}
	if strings.Contains(tailBody, "absent") && !strings.Contains(tc, "! resolve_cf_token") {
		out = append(out, "cf_purge_tail 的裸 -z 判据没跟着解析口 ⇒ 上一枪读到的标签会被这一枪覆盖或漏读")
	}
	// R3 四态不同形
	for _, lit := range []string{"CF_PURGE=off", "CF_PURGE=skipped", "拒读凭据文件", "CF purge 已受理"} {
		if !strings.Contains(stripShellComments(hc), lit) && !strings.Contains(stripShellComments(resolveBody), lit) {
			out = append(out, "四态读数少一档："+lit+" 不在位（\"没清\"的几种形态混成一行，事故就被读成常态）")
		}
	}
	// R4 token 本体不进屏幕
	for _, body := range []string{rc, hc, tc} {
		for _, line := range strings.Split(body, "\n") {
			l := stripShellComments(line)
			// 只抓**展开形态** `${CLOUDFLARE_API_TOKEN}`：skipped 那句要写出"变量名"给人看，
			// 按裸名判会把自己那条提示判成泄漏（本批首跑真踩）。
			if !strings.Contains(l, "${CLOUDFLARE_API_TOKEN}") {
				continue
			}
			if strings.Contains(l, "echo ") || strings.Contains(l, "ok \"") || strings.Contains(l, "bad \"") {
				out = append(out, "有一行把凭据变量拼进了可读输出（token 本体不许进屏幕）："+strings.TrimSpace(l))
			}
		}
	}
	// R5 属主判据三档都放行（root／本进程／部署根 data 目录的属主＝服务账号）
	if !strings.Contains(rc, `"$(id -u)"`) || !strings.Contains(rc, `!= "0"`) {
		out = append(out, "属主判据缺了 root 或本进程 uid 其中一档 ⇒ 要么运维账号永远 rejected，要么谁都放行")
	}
	if !strings.Contains(rc, "cf_service_uids") || !strings.Contains(rc, `[ "$found" != "1" ]`) {
		out = append(out, "属主判据缺了服务账号那一档 ⇒ 常规形态（root 跑发版、投影归 translator）永远 rejected："+
			"屏幕读数与「根本没配凭据」不同形，但同样清不了缓存")
	}
	// R6 成功读数带来源标签
	if !strings.Contains(acceptLine, "from=${CF_PURGE_FROM}") {
		out = append(out, "受理行没有 from= 来源标签 ⇒ \"带 token\"与\"从文件读到\"在屏幕上无法区分")
	}
	if !strings.Contains(tailOkLine, "from=${CF_PURGE_FROM}") {
		out = append(out, "尾随 ok 行没有 from= 来源标签（同一把尺子只量了第一枪）")
	}
	return out
}

// C5：purge 凭据来源只有一个解析口，四种"清没清"不同形，token 本体不进屏幕。
func TestSwapCfPurgeTokenSourceIsSingleResolver(t *testing.T) {
	src := readProbeScript(t, "swap_web_and_binary.sh")
	get := func(header string) string {
		b, _ := shellFuncBody(src, header)
		return b
	}
	resolve := get("resolve_cf_token() {")
	head := get("cf_purge() {")
	tail := get("cf_purge_tail() {")
	accept := lineWith(src, "CF purge 已受理")
	tailOk := lineWith(src, "CF_PURGE_TAIL=ok")
	if ps := tokenSourceProblems(resolve, head, tail, accept, tailOk); len(ps) > 0 {
		t.Fatalf("凭据来源层不合规：%s", strings.Join(ps, "；"))
	}

	// 反证一：把 env 档整条摘掉（退化回"只认磁盘文件"）⇒ R1 必须红。
	if ps := tokenSourceProblems(strings.Replace(resolve, `CF_PURGE_FROM="env"; return 0; fi`, "return 0; fi", 1),
		head, tail, accept, tailOk); len(ps) == 0 {
		t.Fatalf("反证一不过：env 档没了竟然判绿")
	}
	// 反证二：cf_purge 走回裸的 -z 短路（＝文件源读不到）⇒ R2/R3 必须红。
	bare := strings.Replace(head, "if ! resolve_cf_token; then",
		`if [ -z "${CLOUDFLARE_API_TOKEN:-}" ]; then
        echo "CF_PURGE=skipped"; return 0
      fi
      if false; then`, 1)
	if ps := tokenSourceProblems(resolve, bare, tail, accept, tailOk); len(ps) == 0 {
		t.Fatalf("反证二不过：调用点自带短路竟然判绿")
	}
	// 反证三：属主只认 root ⇒ R5 必须红。
	onlyRoot := strings.Replace(resolve, `&& [ "$uid" != "$(id -u)" ]`, "", 1)
	if ps := tokenSourceProblems(onlyRoot, head, tail, accept, tailOk); len(ps) == 0 {
		t.Fatalf("反证三不过：只认 root（运维账号永远拒读）竟然判绿")
	}
	// 反证三b：把"服务账号那一档"摘掉（root + 本进程仍留着）⇒ R5 必须红。
	noSvc := strings.Replace(resolve, ` && [ "$found" != "1" ]`, "", 1)
	if ps := tokenSourceProblems(noSvc, head, tail, accept, tailOk); len(ps) == 0 {
		t.Fatalf("反证三b不过：摘掉服务账号那一档竟然判绿（常规形态会恒 rejected，而读数看起来只是「没配」）")
	}
	// 反证四：受理行去掉 from 标签 ⇒ R6 必须红。
	if ps := tokenSourceProblems(resolve, head, tail,
		strings.Replace(accept, "，from=${CF_PURGE_FROM}", "", 1), tailOk); len(ps) == 0 {
		t.Fatalf("反证四不过：来源标签丢了竟然判绿")
	}
	// 反证五：把 token 拼进读数行 ⇒ R4 必须红（这一档的坏法是"为了好排查把凭据打出来"）。
	if ps := tokenSourceProblems(resolve+"\n  echo \"token=${CLOUDFLARE_API_TOKEN}\"\n",
		head, tail, accept, tailOk); len(ps) == 0 {
		t.Fatalf("反证五不过：凭据进屏幕竟然判绿")
	}
	// 空输入必须判死（"尺子丢了"与"这次很干净"不许同形）。
	if ps := tokenSourceProblems("", "", "", "", ""); len(ps) == 0 {
		t.Fatalf("判据自证不过：空函数体判绿")
	}
}

// serviceUidAnchorProblems 返回「服务账号那一档拿什么当锚」的形态问题（空＝合规）。
//
// 为什么要单列一条锁：这一档的**错误形态是静默的**——写错锚点不会报错，只会让这一档恒不成立，
// 屏幕上读起来与「凭据没配」不同形（rejected vs skipped），但结果同样是**永远清不了缓存**，
// 而那正是这条腿存在的理由（AGENTS §一·5 批④「读数看起来正常」那一族）。
// 锚点只能是 `<deploy_dir>/data`：它是**服务进程**写投影的落点、归服务账号所有；
// 而 `bin/translator-server` 在现网实测是 `root:root 0755`（换件由 root 用 cp+mv 落）⇒ 拿它当锚＝空转。
func serviceUidAnchorProblems(body string) []string {
	var out []string
	c := stripShellComments(body)
	if strings.TrimSpace(c) == "" {
		return append(out, "读不到 cf_service_uids 的函数体（判据没有输入＝空转）")
	}
	if !strings.Contains(c, `"$d/data"`) {
		out = append(out, "服务账号锚点不是各部署根的 data/ 目录（不是「谁在写这份投影」的那个账号）")
	}
	if strings.Contains(c, "bin/translator-server") {
		out = append(out, "服务账号锚点用了 bin/translator-server：它换件后归 root（实测 root:root 0755），这一档会静默空转")
	}
	if !strings.Contains(c, "$DEPLOY_DIRS") {
		out = append(out, "服务账号锚点没遍历 DEPLOY_DIRS（两站各写各的 data 目录，写死一份就有一站恒 rejected）")
	}
	return out
}

// C6：服务账号那一档的锚点必须是各部署根的 data/ 目录（★ 2026-11-02 现网实证：
// 默认投影路径因此落在 /opt/translator/data/，而不是服务写不进去的 /etc/translator/）。
func TestSwapCfPurgeServiceUidAnchorIsDataDir(t *testing.T) {
	src := readProbeScript(t, "swap_web_and_binary.sh")
	body, ok := shellFuncBody(src, "cf_service_uids() {")
	if !ok {
		t.Fatalf("执行体里没有 cf_service_uids ⇒ 服务账号那一档只能是空转")
	}
	if ps := serviceUidAnchorProblems(body); len(ps) > 0 {
		t.Fatalf("服务账号锚点不合规：%s", strings.Join(ps, "；"))
	}
	// 反证一：锚点换成二进制路径（现网归 root）⇒ 必须红。
	if ps := serviceUidAnchorProblems(strings.Replace(body, `"$d/data"`, `"$d/bin/translator-server"`, 1)); len(ps) == 0 {
		t.Fatalf("反证一不过：锚点改成 root 拥有的二进制路径竟然判绿")
	}
	// 反证二：只认一个写死的目录（不遍历 DEPLOY_DIRS）⇒ 必须红（两站形态只覆盖一站）。
	if ps := serviceUidAnchorProblems(strings.Replace(body, `for d in $DEPLOY_DIRS; do`, `for d in /opt/translator; do`, 1)); len(ps) == 0 {
		t.Fatalf("反证二不过：写死单站目录竟然判绿")
	}
	// 空输入必须判死（「尺子丢了」与「这次很干净」不许同形）。
	if ps := serviceUidAnchorProblems(""); len(ps) == 0 {
		t.Fatalf("判据自证不过：空函数体判绿")
	}
}

// lineWith 返回源码里第一条包含 frag 的行（找不到回空串，由调用方判红）。
func lineWith(src, frag string) string {
	for _, l := range strings.Split(src, "\n") {
		if strings.Contains(l, frag) {
			return l
		}
	}
	return ""
}
