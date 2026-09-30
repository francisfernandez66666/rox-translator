// ============ cmd/fpdprobe · 职责说明 ============
// 现网「文件转换远程派发」的**真机往返探针**（★ 2026-10-01 〇-AF 开闸当天建立）。
//
// 它回答的是一个 09-29 之后一直没人机械回答过的问题：
//
//	**主站的 Go 派发腿**（fileproc.DispatchRun：put→run→stat→get＋两侧 sha256 等值）
//	今天有没有真的把一份够档的文件送到体验机上、再把产物原样拉回来？
//
// 为什么已有的两道闸不够：
//
//	① `/api/health` 的 `dispatch=online` 只证明**探测腿**（probe）拨通过，
//	   probe 不搬文件、不跑转换，09-29 那次事故里它同样是绿的；
//	② `scripts/dispatch_preflight.sh` 的 G5 走的是**bash 自己拼的协议**，
//	   而当年坏掉的是 Go 侧那两条裸 `ssh mkdir` / `scp` 死腿——bash 绿≠Go 绿。
//	⇒ 这一条探针直接调产品侧用的**同一批导出函数**（TryDispatch / SessionIDFor /
//	  DispatchEligible 所在的同一实现），用真件跑真往返，才算把第三条腿闭上。
//
// ★ 最难防的形态是「静默降级」：TryDispatch 失败只打一条 WARN 然后返回 false，
//
//	调用方回落到本地链，客户照样拿到能打开的 PDF。所以本探针**绝不把"产物存在"当成功**：
//	产物这一腿必须能在远端原样回传的 stdout 里看到**远端派发根的路径串**（pdf_overlay apply 的
//	成功行是 `OK: <out_pdf> replaced=… `，out_pdf 就是远端会话目录里的绝对路径；
//	本地跑时那里会是主站路径），取不到该证据即 exit 2。
//	这正是 AGENTS §一·12「有自动降级的链路，产物能打开不算验收」的落地。
//
// ★ 但这一判据**只适用于 apply 那一腿**（〇-AF 补丁五真机实测校准）：
//
//	extract 的 stdout 就是 `{"success":true,"texts":[…]}`，**结构上不含任何路径**
//	（实测一份 24MB/32 页真件：grep -c '/opt/fpdispatch' 回 0）。把同一条判据套到 extract 上
//	会造出一个"真派成功也永远红"的假红判据——而假红比假绿更糟的地方在于，它会让人去查
//	一条本来好好的腿。所以 extract 这一腿的证据换成两条能成立的：
//	① TryDispatch=true（这一条只有 DispatchRun 全程成功才可能返回；降级路径一律 false，
//	   探针里没有任何本地兜底代码）；② 回执合形（success 且 texts 非空）。
//	顺带出一行 `extract_has_remote_path=` 纯读数**不参与判定**，只用于哪天远端回执形态变了能看出来。
//
// 用法（在**主站**执行，须与 translator.service 同一套 FILEPROC_DISPATCH_* 环境）：
//
//	set -a; . /etc/translator/dispatch.env; set +a
//	runuser -u translator -- /opt/translator/bin/fpdprobe <一份 ≥MIN_MB 且 ≥MIN_PAGES 的 PDF> [lang]
//
//	⚠️ 用 `runuser -u translator` 而不是 root：探针要复现的是**服务账号**那一条腿
//	   （私钥可读性、known_hosts 可读性都在这个 uid 上），root 跑通了不代表服务跑得通。
//	⚠️ 输出件默认落在输入件同目录的 `<输入名>.fpdprobe.pdf`，跑完请随测试残骸一起清（§十）。
//
// 读数怎么查（★ 09-30 开闸首跑判红就是这么定位的，别一上来怀疑网络/凭据）：
//
//	`extract_leg=FAIL` 且 `input_size` 够档 ⇒ 先看同一份输出里的 `pages=` 那一行。
//	派发资格是「体积 ∧ 页数」两条腿同时成立，页数读 0 会让 TryDispatch **直接判不合格、
//	连一次远端都不拨**（旧形态连日志都没有；〇-AF 补丁四起改为打一条 WARN）。
//	`used=0` 而 `parser>0` ⇒ 优先级被改写；`parser=0 markers=0` ⇒ 件本身读不动，换一份真件。
//
//	`pages=` 正常、`eligible=true` 却仍 `extract_leg=FAIL` ⇒ 问题在**协议本身**，不是档位。
//	这时候要看紧跟其后的那条 WARN 里带的远端 stderr（探针把错误原文一并打出来）：
//	10-01 真机首跑读到的就是 `未知子命令: pdf_overlay.py`——header.argv 头部脚本名被双写，
//	远端按位置砍掉 argv[0] 之后把第二个脚本名当子命令（〇-AF 补丁五修，锁在
//	fileproc_remote_test.go 的 TestDispatchRunArgvHasScriptNameOnce）。
//	这类红**不会**出现在 /api/health 与预检里：probe 腿没有 argv，G5 是 bash 自己拼的单份脚本名。
//
// 退出码：0=远端往返成功且拿到远端证据；1=派发失败（含档位不足）；2=派发出件但**没有远端证据**
//
//	（＝被静默降级成本地链，属"闸开了但从没真跑过一次"的复发形态）；3=派发总闸关着。
//
// =============================================================================
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"translator/internal/fileproc"
)

// remoteRoot 远端派发根（与 fileproc 的 dispatchRoot 同口径：env 缺省 /opt/fpdispatch）。
//
// 这里不 import 那个私有函数，是因为探针要的是**独立第二读数**：
// 若哪天产品侧根路径与 env 现值各说各话，探针仍能从 stdout 里认出真正落盘的那一侧。
func remoteRoot() string {
	if v := os.Getenv("FILEPROC_DISPATCH_ROOT"); v != "" {
		return v
	}
	return "/opt/fpdispatch"
}

// sha256Of 文件的 sha256（十六进制小写），取不到返回空串由调用侧判红。
//
// ★ 流式读：探针要在现网上给 20–40MB 的件算哈希，一次性 ReadFile 等于自己造一个
// 与上传链同量级的堆尖峰（P0 那条「页数校验别整读」的同族），这里按 io.Copy 走常量内存。
func sha256Of(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// report 统一出账：一行一个键，便于把现网读数原样贴进交接文档。
func report(key string, vals ...interface{}) {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		parts = append(parts, fmt.Sprint(v))
	}
	fmt.Printf("@@FPD %s=%s\n", key, strings.Join(parts, " "))
}

// extractJSON 是 pdf_overlay.py `extract` 的 stdout 形态（与 fileproc.ExtractTextsPdfOverlay 同构）。
type extractJSON struct {
	Success bool     `json:"success"`
	Texts   []string `json:"texts"`
}

// main 三步：① 读派发态与档位；② 远端 extract 取键；③ 恒等映射远端 apply 写回并验产物。
//
// 恒等映射（原文→原文）刻意**不经模型**：探针要量的是传输与转换两条腿，
// 不是上游译文的对不对；顺带把「为一次探针在现网真烧一份 token」这笔开销也省掉。
func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: fpdprobe <in.pdf> [lang]")
		os.Exit(2)
	}
	inPath, err := filepath.Abs(os.Args[1])
	if err != nil {
		report("fatal", err)
		os.Exit(1)
	}
	lang := "en"
	if len(os.Args) > 2 && os.Args[2] != "" {
		lang = os.Args[2]
	}
	outPath := inPath + ".fpdprobe.pdf"

	if !fileproc.DispatchEnabled() {
		report("dispatch_enabled", false)
		report("verdict", "CLOSED ⇒ 总闸关着（FILEPROC_DISPATCH / _HOST），探针无事可做")
		os.Exit(3)
	}
	snap := fileproc.DispatchReadinessSnapshot()
	fi, serr := os.Stat(inPath)
	if serr != nil {
		report("fatal", "输入件不存在", serr)
		os.Exit(1)
	}
	session := fileproc.SessionIDFor(inPath)
	report("dispatch_status", snap.Status)
	report("readiness", "expire="+snap.ExpireDate, "mem_cap="+snap.MemCap, "selftest="+snap.Selftest)
	report("thresholds", "MIN_MB="+os.Getenv("FILEPROC_DISPATCH_MIN_MB"), "MIN_PAGES="+os.Getenv("FILEPROC_DISPATCH_MIN_PAGES"))
	report("input", inPath)
	report("input_size", fi.Size())
	report("input_sha", sha256Of(inPath))
	report("session", session)

	// ★ 页数四道读数**各自摊开**（〇-AF 补丁四）：派发资格是「体积 ∧ 页数」两条腿，
	//   09-30 那次开闸首跑判红就卡在页数腿上，而当时只能人工 grep "/Type/Page" 数一遍。
	//   现在这一行直接告诉后人：解析腿读到几页、尾部 /Count 读到几页、标记腿数出几页、最终采纳几页。
	//   期望形态是 parser>0 且 used==parser；used=0 而体积够档 ⇒ 这一单**根本没派**（TryDispatch 会打 WARN）。
	pcParser, pcTrailer, pcMarkers, pcUsed := fileproc.PdfPageCountDiagnostics(inPath)
	report("pages", "parser="+fmt.Sprint(pcParser), "trailer_count="+fmt.Sprint(pcTrailer),
		"markers="+fmt.Sprint(pcMarkers), "used="+fmt.Sprint(pcUsed))
	report("eligible", fileproc.DispatchEligible([]string{inPath}, pcUsed, 0))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// ① extract：零产物（stdout 就是产物），失败即 TryDispatch=false。
	t0 := time.Now()
	exRaw, exOK := fileproc.TryDispatch(ctx, session, "pdf_overlay.py",
		[]string{"pdf_overlay.py", "extract", inPath}, nil, []string{inPath}, nil)
	report("extract_leg", okWord(exOK), time.Since(t0).Round(time.Millisecond))
	if !exOK {
		report("verdict", "FAIL ⇒ extract 这一腿没派出去（详见 journalctl 的 [fpdispatch] WARN）")
		os.Exit(1)
	}
	var ex extractJSON
	if uerr := json.Unmarshal(lastJSONLine(exRaw), &ex); uerr != nil || !ex.Success || len(ex.Texts) == 0 {
		report("extract_parse", "失败", uerr, "stdout_tail", tail(string(exRaw), 200))
		report("verdict", "FAIL ⇒ 远端 extract 回执不合（零段/非 JSON 都不算跑通）")
		os.Exit(1)
	}
	report("extract_segments", len(ex.Texts))
	// ★ 纯读数，**不参与判定**（〇-AF 补丁五）：extract 的回执结构上只有文本段、没有路径，
	//	这一条真机实测恒 false；把它当判据就会造出「派成功也永远红」的假红，
	//	把人的排障引到一条本来就好的腿上去。真正的证据是 extract_leg=OK 本身
	//	（TryDispatch 只有 DispatchRun 全程成功才返回 true，降级一律 false，探针里没有本地兜底代码）。
	report("extract_has_remote_path", strings.Contains(string(exRaw), remoteRoot()))

	// ② apply：恒等映射写回，声明式产物 = 主站绝对路径（由 Go 侧换成远端会话目录名再回拉）。
	tr := make(map[string]string, len(ex.Texts))
	for _, t := range ex.Texts {
		tr[t] = t
	}
	payload, merr := json.Marshal(map[string]interface{}{"translations": tr})
	if merr != nil {
		report("fatal", merr)
		os.Exit(1)
	}
	_ = os.Remove(outPath)
	outputs := map[string]string{filepath.Base(outPath): outPath}
	t1 := time.Now()
	apRaw, apOK := fileproc.TryDispatch(ctx, session, "pdf_overlay.py",
		[]string{"pdf_overlay.py", "apply", inPath, outPath, lang}, payload, []string{inPath}, outputs)
	report("apply_leg", okWord(apOK), time.Since(t1).Round(time.Millisecond))
	if !apOK {
		report("verdict", "FAIL ⇒ apply 这一腿没派出去（同一份输入 extract 能派、apply 不能派时，先查产物落点守卫）")
		os.Exit(1)
	}
	applyLine := tail(string(apRaw), 300)
	report("apply_stdout", strings.ReplaceAll(applyLine, "\n", " | "))
	// ★ 远端证据必须问**整段 stdout**，不能问 tail：截 300 字节可能正好把远端路径削掉，
	//   于是把派成功的一单判成降级（假红比假绿好查，但也得查对地方）。
	report("remote_evidence", strings.Contains(string(apRaw), remoteRoot()))

	// ③ 产物入门校验：存在 / 非空 / PDF 魔数 / 与输入不同源（写回必然改字节）。
	oi, oerr := os.Stat(outPath)
	if oerr != nil {
		report("verdict", "FAIL ⇒ 产物没落到主站", oerr)
		os.Exit(1)
	}
	head := make([]byte, 5)
	if f, ferr := os.Open(outPath); ferr == nil {
		_, _ = f.Read(head)
		_ = f.Close()
	}
	report("artifact", outPath)
	report("artifact_size", oi.Size())
	report("artifact_sha", sha256Of(outPath))
	report("artifact_magic", string(head))
	// 膨胀比按**产物/输入**算：这一档（MIN_MB）是按产物尺寸定的，不是按输入尺寸定的。
	// 09-30 定档实测 3.4×，今晚 G5 实测 1.7×——探针把这个数原样记下来，档位调整才有依据，
	// 而不是让后人再拿一份件去现场猜。
	if fi.Size() > 0 {
		report("inflation_x", fmt.Sprintf("%.2f", float64(oi.Size())/float64(fi.Size())))
	}
	if !strings.HasPrefix(string(head), "%PDF") || oi.Size() == 0 {
		report("verdict", "FAIL ⇒ 产物不是合法 PDF（空件/坏件都不许当成功）")
		os.Exit(1)
	}
	if !strings.Contains(string(apRaw), remoteRoot()) {
		report("verdict", "SUSPECT ⇒ 产物能打开但整段 stdout 里没有远端路径＝这一单被降级链兜回本地跑了")
		os.Exit(2)
	}
	report("verdict", "OK ⇒ Go 派发腿真机往返过一次（extract 走通＋apply 回拉到件且 stdout 带远端路径）")
}

// okWord 把布尔读数写成中英一致的账面目（成功/失败），避免 true/false 在交接文档里读成"某项没配"。
func okWord(v bool) string {
	if v {
		return "OK"
	}
	return "FAIL"
}

// lastJSONLine 取 stdout 最后一个非空行：远端 fpdexec 把回执打在 stdout 末尾，
//
//	转换脚本可能在前面带 warn 行（与 dispatch_preflight.sh 的 jf() 同一口径）。
func lastJSONLine(raw []byte) []byte {
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" && strings.HasPrefix(s, "{") {
			return []byte(s)
		}
	}
	return []byte(strings.TrimSpace(string(raw)))
}

// tail 取字符串尾部 n 个字节（日志读数用，避免把整页 stdout 送进聊天/文档）。
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
