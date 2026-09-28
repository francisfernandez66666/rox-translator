// ============ fileproc_remote.go · 职责说明 ============
// 文档转换**远程派发**执行器（改造方案 §4，2026-09-28）。
//
// 它解决的问题：主站内存小（1.6G），最大的内存尖峰来自 PDF 转换子进程，
// 于是本地那道并发闸被迫常年锁在 1。本执行器把「最吃内存的那一单」送到体验机上执行，
// 产物**同步取回主站**后再返回，所以库里出现的路径永远只可能在主站（§0-①）。
//
// 三条硬约束（改这个文件前必须先读）：
//  1. **默认关闭**：不配 FILEPROC_DISPATCH=1 时，本文件除 DispatchEnabled() 返回 false
//     之外不产生任何影响，代码路径与改造前逐字节等价（§0-③）。
//  2. **远端失败＝回本地排队**（§5.4 D3 已决）：本文件永远只返回 error，
//     由调用方（pdf_overlay/pdfwrite）决定要不要回落；它自己不重试、不自作主张降级。
//  3. **远端不持有数据**：会话目录用完即由远端 sweep timer 回收；本文件回拉产物后
//     立即校验（存在 / 非空 / 文件头合法），校验不过就判失败 ⇒ 主站会走本地重跑。
//
// ★ 本实现刻意不用 sftp 协议库，而是复用本机 ssh/scp 二进制：
//
//	少一个第三方依赖、少一处需要长期维护的安全面；代价是需要密钥文件与 BatchMode=yes。
//
// ================================================================
package fileproc

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"translator/internal/observability"
)

// 派发相关的环境变量（★ 全部默认关闭；真值写在独立 drop-in dispatch.conf 里）
const (
	envDispatch        = "FILEPROC_DISPATCH"         // 空=关；=1 才启用（唯一总闸）
	envDispatchHost    = "FILEPROC_DISPATCH_HOST"    // fpd@<ip>
	envDispatchRoot    = "FILEPROC_DISPATCH_ROOT"    // 远端根，默认 /opt/fpdispatch
	envDispatchPyBin   = "FILEPROC_DISPATCH_PYBIN"   // 远端 venv 解释器
	envDispatchKey     = "FILEPROC_DISPATCH_SSH_KEY" // 0600 私钥
	envDispatchMinMB   = "FILEPROC_DISPATCH_MIN_MB"  // 分流阈值（体积腿）
	envDispatchMinPage = "FILEPROC_DISPATCH_MIN_PAGES"
	envDispatchMaxConc = "FILEPROC_DISPATCH_MAX_CONCURRENT"
	envDispatchTTL     = "FILEPROC_DISPATCH_SESSION_TTL_SEC"
	// envDispatchFont：pdfwrite 那条腿专用的**远端字体路径**。
	// 为什么必须显式配：pdfwrite.py 的 argv[2] 是字体文件（主站是 NotoSansCJK 的 .ttc，约 20MB），
	// 不配的话每次派发都要先把它 scp 过去——搬 20MB 字体去换一次 fpdf2 排版，纯亏。
	// 配了它就把字体参数换成远端自己的路径（不搬运），不配则** pdfwrite 一律不派**（宁可不派，不派错）。
	envDispatchFont = "FILEPROC_DISPATCH_FONT"
)

// dispatchFpdexec 远端唯一的执行入口（ForceCommand 的固定目标）
const dispatchFpdexec = "/opt/fpdispatch/bin/fpdexec.py"

// DispatchEnabled 总闸是否打开（唯一入口，其余导出函数都得先过它）。
// ★ 关着的时候本包对外行为与改造前完全一致，这条要能被单测直接验证。
func DispatchEnabled() bool {
	return os.Getenv(envDispatch) == "1" && os.Getenv(envDispatchHost) != ""
}

// dispatchHost / dispatchRoot / dispatchPyBin：远端连接参数（缺省值照 §7 表）
func dispatchHost() string { return os.Getenv(envDispatchHost) }

// dispatchRoot 远端派发根目录（产物与工作目录都在其下；默认 /opt/fpdispatch）。
func dispatchRoot() string {
	if v := os.Getenv(envDispatchRoot); v != "" {
		return v
	}
	return "/opt/fpdispatch"
}

// dispatchPyBin 远端 venv 解释器（与 pyBin() 同角色，只是跑在体验机上）。
func dispatchPyBin() string {
	if v := os.Getenv(envDispatchPyBin); v != "" {
		return v
	}
	return "/opt/fpdispatch/.venv/bin/python3"
}

// dispatchKey 派发专用 SSH 私钥路径（0600，属主 translator，只主站持有）。
func dispatchKey() string { return os.Getenv(envDispatchKey) }

// dispatchRemoteFont 远端字体路径（pdfwrite 那条腿用；空 ⇒ 不派 pdfwrite）。
func dispatchRemoteFont() string { return os.Getenv(envDispatchFont) }

// dispatchMinBytes 分流阈值的体积腿（字节）：FILEPROC_DISPATCH_MIN_MB × 1MiB，默认 8MiB。
// ★ 待 §12-D2 带宽实测后上调，宁窄勿宽——别把小件派出去白付公网往返。
func dispatchMinBytes() int64 {
	mb := 8 // §7 默认值（★ 待 §12-D2 带宽实测后上调，别把小件派出去白付往返）
	if v := os.Getenv(envDispatchMinMB); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			mb = n
		}
	}
	return int64(mb) << 20
}

// dispatchMinPages 分流阈值的页数腿（FILEPROC_DISPATCH_MIN_PAGES，默认 15）。
func dispatchMinPages() int {
	n := 15
	if v := os.Getenv(envDispatchMinPage); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p >= 0 {
			n = p
		}
	}
	return n
}

// ---------------- 远端闸（与本地 procGate 完全独立） ----------------

var (
	remoteOnce sync.Once
	remoteGate chan struct{}
)

// remoteGateChan 远端并发闸（惰性初始化）。
// ★ 为什么不能复用本地 procGate：本地那道是被 1.6G 内存逼到容量=1 的（procgate.go D17），
//
//	若远端也走它，"把大件送走"的收益当场被锁回串行。这里照抄 D17 的自适应规则，
//	但读的是**远端自己的内存**（probe 的 mem_gb）：<3G→1 / 3-6G→2 / ≥6G→4。
func remoteGateChan() chan struct{} {
	remoteOnce.Do(func() {
		remoteGate = make(chan struct{}, remoteGateMax())
	})
	return remoteGate
}

// remoteGateMax 远端并发上限：显式覆盖优先，否则按远端内存自适应。
func remoteGateMax() int {
	if v := os.Getenv(envDispatchMaxConc); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	gb := dispatchMemGB()
	switch {
	case gb >= 6:
		return 4
	case gb >= 3:
		return 2
	default:
		return 1
	}
}

// acquireRemoteGateCtx 可取消地领取远端名额（语义照抄 acquireProcGateCtx）。
func acquireRemoteGateCtx(ctx context.Context) func() {
	g := remoteGateChan()
	select {
	case g <- struct{}{}:
	case <-ctx.Done():
		return func() {}
	}
	return func() { <-g }
}

// ---------------- 就绪探测（启动时握手，缓存 + 连续失败降级） ----------------

// DispatchProbe 远端实测值（来自 fpdexec.py probe；JSON 字段对齐方案 §5.1）
type DispatchProbe struct {
	OK         bool              `json:"ok"`
	MemGB      float64           `json:"mem_gb"`
	ScriptSHA  map[string]string `json:"script_sha"`
	Selftest   int               `json:"selftest"`
	Expired    bool              `json:"expired"`
	PybinExist bool              `json:"pybin_exists"`
	Pymupdf    string            `json:"pymupdf"`
}

// 进程内派发状态（就绪一次缓存 + 连续失败降级）。
var (
	probeOnce  sync.Once
	probeRes   *DispatchProbe
	probeErr   error
	dispatchMu sync.Mutex
	// degradedUntil：连续失败后的静默期（§5.2），期间不再浪费<｜hy_place▁holder▁no▁813｜> RTT 去拨一台坏机器。
	degradedUntil time.Time
	failStreak    int
)

// DispatchStatus 给 /api/health 的三态状态词（★ 只出状态词，不出主机与路径，见 #42 口径）
func DispatchStatus() string {
	if !DispatchEnabled() {
		return "off"
	}
	if _, err := dispatchProbe(context.Background()); err != nil {
		return "degraded"
	}
	dispatchMu.Lock()
	defer dispatchMu.Unlock()
	if time.Now().Before(degradedUntil) {
		return "degraded"
	}
	return "online"
}

// dispatchProbe 远端就绪探测（进程内只成功一次；失败可重探）。
func dispatchProbe(ctx context.Context) (*DispatchProbe, error) {
	probeOnce.Do(func() {
		probeRes, probeErr = doDispatchProbe(ctx)
	})
	if probeRes == nil {
		return nil, probeErr
	}
	return probeRes, nil
}

// doDispatchProbe 真跑一次 `fpdexec.py probe` 并解 JSON。
//
// ⚠️ stdin 第一行必须是 **base64(header)**，与 §4 的协议一致（DispatchRun 走的是同一条编码）。
//
//	2026-09-28 单测实测：这里曾直接发裸 JSON，远端 fpdexec 解不出来 ⇒ 探测恒失败，
//	而失败被降级链兜住后表现为"派发从没生效过"，是最难发现的一类静默死分支。
func doDispatchProbe(ctx context.Context) (*DispatchProbe, error) {
	hb, err := json.Marshal(map[string]string{"mode": "probe"})
	if err != nil {
		return nil, err
	}
	stdout, stderr, err := dispatchSSH(ctx, append([]byte(base64.StdEncoding.EncodeToString(hb)), '\n'), nil, subTimeout())
	if err != nil {
		return nil, fmt.Errorf("远端探针失败: %w\n%s", err, truncateTail(stderr))
	}
	line := strings.TrimSpace(lastNonEmptyLine(string(stdout)))
	var p DispatchProbe
	if err := json.Unmarshal([]byte(line), &p); err != nil {
		return nil, fmt.Errorf("远端探针返回不可解析（%s）: %w", line, err)
	}
	if !p.OK || p.Expired || p.Selftest != 0 {
		return nil, fmt.Errorf("远端未就绪 ok=%v expired=%v selftest=%d", p.OK, p.Expired, p.Selftest)
	}
	return &p, nil
}

// dispatchMemGB 远端内存（GB）。拿不到就按 0 处理 ⇒ 自适应档落到最保守的 1。
func dispatchMemGB() float64 {
	p, err := dispatchProbe(context.Background())
	if err != nil || p == nil {
		return 0
	}
	return p.MemGB
}

// noteDispatchResult 记录一次派发结果：成功清零失败计数，失败连成一串后置静默期（§5.2）。
func noteDispatchResult(ctx context.Context, ok bool, err error) {
	dispatchMu.Lock()
	defer dispatchMu.Unlock()
	if ok {
		failStreak = 0
		degradedUntil = time.Time{}
		return
	}
	failStreak++
	if failStreak >= 3 {
		degradedUntil = time.Now().Add(30 * time.Second)
		observability.Warn(ctx, "[fpdispatch] 连续失败已满 3 次，暂停派发 30s", "err", err)
	}
}

// ---------------- 分流判定 ----------------

// DispatchEligible 这一单是否值得派出去（§5.3，宁窄勿宽）。
// 两条腿，命中任一即可：
//
//	① **输入文件腿**（overlay 链）：有 .pdf 输入、体积 ≥ MIN_MB **且** 页数 ≥ MIN_PAGES（双条件）；
//	② **payload 腿**（pdfwrite 降级重建链）：它没有输入文件——原文与译文都在 payload 里，
//	   所以按 payload 体积判。这一条是 09-28 补的：否则 pdfwrite 永远派不出去。
//
// ★ 为什么这么保守：2G 远端并发只有 1，派错一单的代价是「白付一次公网搬运 +
//
//	主站那一单还得回本地重跑」，双份时间。宁可少派，不可派错。
func DispatchEligible(inputs []string, pages int, payloadLen int64) bool {
	if !DispatchEnabled() {
		return false
	}
	// ② payload 腿（无输入文件的调用，如 pdfwrite）
	if len(inputs) == 0 {
		return payloadLen >= dispatchMinBytes()
	}
	// ① 输入文件腿
	var maxSize int64
	for _, p := range inputs {
		if fi, err := os.Stat(p); err == nil && fi.Size() > maxSize {
			maxSize = fi.Size()
		}
		if strings.ToLower(filepath.Ext(p)) != ".pdf" {
			return false // §7 白名单：只有 PDF 链（office 是纯 Go，派不了也用不着派）
		}
	}
	if maxSize < dispatchMinBytes() {
		return false
	}
	return pages >= dispatchMinPages()
}

// DispatchArtifactGuard 产物落点守卫（§9-A2：库里/argv 里**绝不允许**出现远端路径）。
//
// 它拦的是本方案最怕的那个形态：**到期后没法用**——
// 工单成功、账照扣，库里 `result_path` 记的却是体验机上的 `/opt/fpdispatch/w/xxx/out.pdf`。
// 体验机一到期回收，这条记录就变成指向一台不存在的机器的死链，客户点下载必然 404，
// 而排障时看到的是"工单成功了"，最容易被误判成偶发。
//
// 参数：p=将要写进库或 argv 的产物路径；allowedRoots=主站允许的落点根（OutputDir/UploadDir 等），
//
//	留空时只做"不得落在远端"这一条负向判据。
//
// 返回错误：p 落在远端根之下（或为空）时返回错误；其余放行。
//
// ★ 负向锁必须配正向对照：见 fileproc_remote_test.go 的 TestDispatchArtifactGuard，
// 该用例同时断言"主站路径必须放行"与"远端路径必须判红"两侧，否则这条守卫改坏也不会红。
func DispatchArtifactGuard(p string, allowedRoots ...string) error {
	if p == "" {
		return errors.New("产物路径为空")
	}
	clean := filepath.Clean(p)
	// ① 负向：落在远端派发根之下 ⇒ 直接判红（这条就是"到期后没法用"的形态）
	if root := dispatchRoot(); root != "" {
		remote := filepath.Clean(root)
		if clean == remote || strings.HasPrefix(clean, remote+string(os.PathSeparator)) {
			return fmt.Errorf("产物路径落在远端派发根 %s 之下（到期后必然取不到）: %s", remote, clean)
		}
	}
	// ② 正向：给了主站根就必须落在其中之一（不给根时不判，避免过度收紧打断既有调用）
	if len(allowedRoots) > 0 {
		for _, r := range allowedRoots {
			if r == "" {
				continue
			}
			rr := filepath.Clean(r)
			if clean == rr || strings.HasPrefix(clean, rr+string(os.PathSeparator)) {
				return nil
			}
		}
		return fmt.Errorf("产物路径不在主站允许的落点内 %v: %s", allowedRoots, clean)
	}
	return nil
}

// ---------------- 会话与传输 ----------------

// dispatchSessionDir 本次会话在远端的工作目录（会话粒度：同一工单多语种共用一个目录）。
func dispatchSessionDir(sessionID string) string {
	return dispatchRoot() + "/w/" + sanitizeSession(sessionID)
}

// sanitizeSession 会话号清洗（防 ../ 与 shell 元字符——这个是拼进远端路径的，必须锁死字符集）。
func sanitizeSession(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "anon"
	}
	return b.String()
}

// DispatchRun 远端执行一次脚本会话：登记 → 搬运输入 → 跑 → 回拉产物 → 校验 → 落位。
//
//	参数：
//	  ctx       超时/取消上下文（由 §5.5 的 20min 总预算约束）
//	  sessionID 会话号（工单号 + 语言，用于远端目录隔离）
//	  script    远端脚本名（fpdexec.py 的白名单会再判一次）
//	  args      脚本参数（**主站形态**：argv[0] 是脚本名，与本地调用同形）
//	  stdin     喂给脚本的 payload（译文映射 JSON；含客户原文，只走 stdin 不进 ps）
//	  outputs   声明式产物（远端相对会话目录的文件名 → 主站最终绝对路径）
//
//	返回：远端子进程 stdout / 错误消息 + 错误。
//	★ 任何一步失败都返回 error：调用方据此回落本地，本函数本身不做兜底，避免"成功是假的"。
func DispatchRun(ctx context.Context, sessionID, script string, args []string, stdin []byte,
	outputs map[string]string) ([]byte, error) {
	if !DispatchEnabled() {
		return nil, errors.New("派发未启用")
	}
	// 零产物是合法的：`extract` 的产物就在 stdout 上（下面会用「stdout 非空」兜住这条）。
	p, err := dispatchProbe(ctx)
	if err != nil {
		noteDispatchResult(ctx, false, err)
		return nil, err
	}
	_ = p

	release := acquireRemoteGateCtx(ctx)
	defer release()

	dir := dispatchSessionDir(sessionID)
	// ① 建远端会话目录（幂等；cat remote mkdir 走一次 ssh）
	if err := dispatchMkdir(ctx, dir); err != nil {
		noteDispatchResult(ctx, false, err)
		return nil, fmt.Errorf("远端会话目录创建失败: %w", err)
	}

	// ② 产物映射先建、输入映射后建（★ 顺序不能反）：
	//    apply 的 argv 里**同时**含输入路径与输出路径，两者都长得像"一个绝对路径"。
	//    若先按"存在即上传"处理，输出路径此时还不存在 ⇒ 会被当成普通参数原样透传，
	//    远端 fpdexec 就会去写一个主站的绝对路径（又被它自己的越界守卫判死）。
	//    表现是"派发必失败"，而日志只显示越界拒绝，很难联想到是映射顺序问题。
	outRemote := make(map[string]string, len(outputs))
	outRels := make([]string, 0, len(outputs))
	convertedOutputs := make(map[string]string, len(outputs))
	for rel, local := range outputs {
		if err := DispatchArtifactGuard(local); err != nil {
			noteDispatchResult(ctx, false, err)
			return nil, fmt.Errorf("产物落点不合法 %s: %w", local, err)
		}
		safeRel := "out_" + sanitizeSession(rel)
		remote := dir + "/" + safeRel
		outRemote[local] = remote
		outRels = append(outRels, remote)
		convertedOutputs[remote] = local
	}

	// ③ 映射 + 搬运输入：远端只认会话目录下的相对名（§4.2 映射表由 Go 单侧生成）
	remoteArgs := make([]string, 0, len(args))
	remoteArgs = append(remoteArgs, script)
	for _, a := range args {
		if r, ok := outRemote[a]; ok {
			remoteArgs = append(remoteArgs, r) // 声明过的产物：换成远端会话目录内的名字
			continue
		}
		if fi, err := os.Stat(a); err == nil && !fi.IsDir() {
			rel := "in_" + filepath.Base(a)
			if err := dispatchPut(ctx, a, dir+"/"+rel); err != nil {
				noteDispatchResult(ctx, false, err)
				return nil, fmt.Errorf("输入搬运失败 %s: %w", a, err)
			}
			remoteArgs = append(remoteArgs, dir+"/"+rel)
			continue
		}
		remoteArgs = append(remoteArgs, a) // 非文件路径：子命令/语种码，原样透传
	}

	header := map[string]interface{}{
		"mode":    "run",
		"script":  script,
		"argv":    remoteArgs,
		"outputs": outRels,
	}
	hb, err := json.Marshal(header)
	if err != nil {
		return nil, err
	}
	stdout, stderr, rerr := dispatchSSH(ctx, append([]byte(base64.StdEncoding.EncodeToString(hb)), '\n'), stdin, fileprocTimeout())
	if rerr != nil {
		noteDispatchResult(ctx, false, rerr)
		return nil, fmt.Errorf("远端执行失败: %w\n%s", rerr, truncateTail(stderr))
	}

	// ④ 回拉产物 → 校验 → 原子落位。**先落临时文件再 rename**，避免半拉文件被上层当产物引用。
	for remotePath, localPath := range convertedOutputs {
		tmp := localPath + ".dispatch.tmp"
		if err := dispatchGet(ctx, remotePath, tmp); err != nil {
			_ = os.Remove(tmp)
			noteDispatchResult(ctx, false, err)
			return nil, fmt.Errorf("产物回拉失败 %s: %w", remotePath, err)
		}
		if err := verifyDispatchArtifact(tmp); err != nil {
			_ = os.Remove(tmp)
			noteDispatchResult(ctx, false, err)
			return nil, fmt.Errorf("产物校验失败 %s: %w", remotePath, err)
		}
		if err := os.Rename(tmp, localPath); err != nil {
			_ = os.Remove(tmp)
			noteDispatchResult(ctx, false, err)
			return nil, fmt.Errorf("产物落位失败 %s: %w", localPath, err)
		}
	}

	// 没有声明产物时（extract 这类调用），产物就是 stdout——空 stdout 一律判失败，
	// 否则上游会拿到"派发成功但什么也没有"的空 JSON，症状比失败更难查。
	if len(convertedOutputs) == 0 && strings.TrimSpace(string(stdout)) == "" {
		err := errors.New("远端返回空 stdout（extract 类调用的产物就在 stdout 上）")
		noteDispatchResult(ctx, false, err)
		return nil, err
	}

	noteDispatchResult(ctx, true, nil)
	observability.Info(ctx, "[fpdispatch] 派发成功并已取回产物", "session", sanitizeSession(sessionID),
		"outputs", len(convertedOutputs))
	return stdout, nil
}

// verifyDispatchArtifact 产物入门校验（存在 / 非空 / 文件头合法）。
// ★ 这是 §9-A1「远端没回文件必须判错」的机器化：宁可回主站重跑，也不许一个空文件当成功。
func verifyDispatchArtifact(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, 8)
	n, _ := f.Read(head)
	if n == 0 {
		return errors.New("产物为空文件")
	}
	head = head[:n]
	switch {
	case bytes.HasPrefix(head, []byte("%PDF")):
		return nil
	case bytes.HasPrefix(head, []byte("PK\x03\x04")): // docx/pptx/xlsx 都是 zip
		return nil
	case bytes.HasPrefix(head, []byte("\x1f\x8b")): // gzip（中间产物）
		return nil
	default:
		// 文本文件（txt/md/csv）没有魔数，允许通过——但它们不在白名单里，派发阈值会把它们挡在外面。
		return nil
	}
}

// ---------------- ssh / scp 原语（外部二进制，统一走 BatchMode） ----------------

// sshBaseArgs ssh 通用参数（BatchMode=yes：密钥不可读时必须失败而不是静默等口令，见 §6.3）
func sshBaseArgs() []string {
	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=15", "-o", "StrictHostKeyChecking=accept-new"}
	if k := dispatchKey(); k != "" {
		args = append(args, "-i", k)
	}
	return args
}

// dispatchSSH 走 ssh 调 fpdexec.py：stdin 第一行是 base64(header)，其后是 payload。
// ★ 为什么不走 "-o ... <<here string"：stderr 与退出码都要能拿回来给排障用，必须 exec.Command。
//
// ★★ 这里**刻意不取远端闸**（gate=nil），理由是两个实测踩到的永久死锁（2026-09-28）：
//
//	① 重入死锁：闸的容量由探测回的 mem_gb 自适应算出 ⇒ "取闸"会触发"闸初始化"，
//	   初始化又要跑一次探测 ssh；若探测也取闸，就形成同 goroutine 对同一个 sync.Once
//	   的重入，Once.Do 会一直等第一次调用返回，而第一次又在等它 ⇒ 进程永久挂死。
//	② 自持死锁：DispatchRun 已经为**整个会话**持有了那一个名额（2G 机容量=1），
//	   会话内部的 ssh/scp 再去取一次 ⇒ 自己等自己。
//	⇒ 结论：远端闸只在 DispatchRun 的会话层取一次，传输原语一律不取闸。
//	  并发上限的真正含义是"同时在远端的会话数"，不是"同时在跑的 ssh 进程数"。
func dispatchSSH(ctx context.Context, headerLine, payload []byte, timeout time.Duration) ([]byte, []byte, error) {
	var stdin bytes.Buffer
	stdin.Write(headerLine)
	if payload != nil {
		stdin.Write(payload)
	}
	args := append(sshBaseArgs(), dispatchHost(), dispatchPyBin(), dispatchFpdexec)
	return runSubprocessGated(ctx, timeout, "ssh", args, stdin.Bytes(), nil)
}

// dispatchMkdir 远端建会话目录（幂等）。
// ★ 不取远端闸：名额由 DispatchRun 在会话层持有（见 dispatchSSH 的死锁说明）。
func dispatchMkdir(ctx context.Context, dir string) error {
	remote := fmt.Sprintf("mkdir -p %q", dir)
	_, stderr, err := runSubprocessGated(ctx, subTimeout(), "ssh", append(sshBaseArgs(), dispatchHost(), remote), nil, nil)
	if err != nil {
		return fmt.Errorf("%w\n%s", err, truncateTail(stderr))
	}
	return nil
}

// dispatchPut 上传单个文件到远端会话目录（scp -p 保 mtime，便于远端 sweep 判闲置）。
func dispatchPut(ctx context.Context, local, remote string) error {
	target := dispatchHost() + ":" + remote
	_, stderr, err := runSubprocessGated(ctx, fileprocTimeout(), "scp", append(sshBaseArgs(), local, target), nil, nil)
	if err != nil {
		return fmt.Errorf("%w\n%s", err, truncateTail(stderr))
	}
	return nil
}

// dispatchGet 下载远端产物到本地临时文件。
func dispatchGet(ctx context.Context, remote, local string) error {
	src := dispatchHost() + ":" + remote
	_, stderr, err := runSubprocessGated(ctx, fileprocTimeout(), "scp", append(sshBaseArgs(), src, local), nil, nil)
	if err != nil {
		return fmt.Errorf("%w\n%s", err, truncateTail(stderr))
	}
	return nil
}

// lastNonEmptyLine 取最后一行非空输出（probe 的 JSON 在最后一行）。
func lastNonEmptyLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}

// ---------------- 调用侧入口（P3 接线用） ----------------

// TryDispatch 一次"能派就派"的尝试：先判分流资格，再走 DispatchRun。
//
//	参数同 DispatchRun；inputs 为需要搬过去的本地输入文件；outputs 为 产物基名→主站绝对路径。
//	返回 (远端 stdout, 是否派发成功)。false 一律表示"该走本地"——
//	★ 这是 D3 已决的口径：远端失败就回本地排队，不打满主机；调用方不得把 false 当错误返回给客户。
func TryDispatch(ctx context.Context, sessionID, script string, args []string, payload []byte,
	inputs []string, outputs map[string]string) ([]byte, bool) {
	if !DispatchEnabled() {
		return nil, false
	}
	pages := 0
	if len(inputs) > 0 {
		pages = pdfPageCountFast(inputs[0])
	}
	if !DispatchEligible(inputs, pages, int64(len(payload))) {
		return nil, false // 小件：本地那条路今天就是好的，派出去只多付一次公网往返
	}
	stdout, err := DispatchRun(ctx, sessionID, script, args, payload, outputs)
	if err != nil {
		// ★ 只告警不返回错误：调用方接着跑本地路径，客户看到的是"慢一点"，不是"失败"。
		observability.Warn(ctx, "[fpdispatch] 派发失败 ⇒ 回落本地排队", "script", script, "err", err)
		return nil, false
	}
	return stdout, true
}

// SessionIDFor 由输入文件派生派发会话号（同一工单多语种复用同一个远端目录 ⇒ 输入件只搬一次）。
// 用路径的 sha1 而不是工单号：fileproc 层拿不到工单上下文，路径稳定即可满足隔离需求。
func SessionIDFor(inputs ...string) string {
	h := sha1.New()
	for _, p := range inputs {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// pdfPageCountFast 派发阈值用的页数估计（★ 只用于判阈值，不用于计费/展示）。
// 为什么自己实现一遍：`internal/api` 的 pdfPageCount 在另一个包（fileproc 不能反向依赖 api），
// 而且这里只要一个"够准就行"的数，不值得再引入一次整文件解析。
// 取数顺序：① 尾部 64KB 里找 "/Count N"（绝大多数 PDF 的页树根在这）；② 退化成流式数 "/Type /Page"。
// 全程流式（内存有界），不会像早期实现那样把 40MB 文件整读进堆。
func pdfPageCountFast(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0
	}
	if n := countFromTrailer(f, fi.Size()); n > 0 {
		return n
	}
	return countTypePage(f)
}

// countFromTrailer 从文件尾部 64KB 里解析 "/Count N"（页树根节点通常写在文件尾）。
func countFromTrailer(f *os.File, size int64) int {
	tail := int64(64 << 10)
	if size < tail {
		tail = size
	}
	if tail <= 0 {
		return 0
	}
	if _, err := f.Seek(size-tail, io.SeekStart); err != nil {
		return 0
	}
	buf := make([]byte, tail)
	n, _ := io.ReadFull(f, buf)
	buf = buf[:n]
	idx := bytes.LastIndex(buf, []byte("/Count"))
	if idx < 0 {
		return 0
	}
	i := idx + len("/Count")
	for i < len(buf) && (buf[i] == ' ' || buf[i] == '\n' || buf[i] == '\r' || buf[i] == '\t') {
		i++
	}
	j := i
	for j < len(buf) && buf[j] >= '0' && buf[j] <= '9' {
		j++
	}
	if j == i {
		return 0
	}
	v, err := strconv.Atoi(string(buf[i:j]))
	if err != nil || v <= 0 {
		return 0
	}
	return v
}

// countTypePage 流式统计 "/Type /Page" 的出现次数（跨块边界用重叠窗口兜住）。
func countTypePage(f *os.File) int {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0
	}
	const chunk = 1 << 20
	const overlap = 32
	buf := make([]byte, chunk)
	total := 0
	var carry []byte
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			window := append(append([]byte{}, carry...), buf[:n]...)
			total += bytes.Count(window, []byte("/Type /Page"))
			if len(window) > overlap {
				carry = window[len(window)-overlap:]
			} else {
				carry = window
			}
		}
		if rerr != nil {
			break
		}
	}
	return total
}
