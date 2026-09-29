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
// ★ 本实现刻意不用 sftp 协议库，而是复用本机 ssh 二进制：少一个第三方依赖、少一处需要长期维护的安全面；
//
//	代价是需要密钥文件与 BatchMode=yes。
//
// ★★ 2026-09-29 传输腿改造（交接文档第一节，动这块前必读）：
//
//	体验机的 sshd 给 fpd 账号配了 `ForceCommand=/opt/fpdispatch/bin/fpd-bridge` ＋ `PermitTTY no`
//	（这是方案本来的红线：不给交互 shell、忽略 SSH_ORIGINAL_COMMAND）。于是：
//	  · 裸 `ssh host 'mkdir -p …'` 会被顶成 bridge，bridge 只把 stdin 喂给 fpdexec，
//	    那条 mkdir 永远不会被执行 → 实测退 78「stdin 缺 header 行」，目录压根没建出来；
//	  · `scp` 要的是远端 `scp -t/-f` 进程或 sftp 子系统，两者都不存在 → 回拉挂到墙钟超时、
//	    上送静默非零退出，连错误文本都拿不到。
//	⇒ 三条腿（建目录/上送/回拉）一律改走 fpdexec 自己的 put/stat/get 协议（stdin 第一行 base64(header)）。
//	   put 内部就 makedirs，所以建目录那条多余往返直接删掉；
//	   上送与回拉两侧都按 sha256 **逐字节比对**——scp 时代没有这一比对，"传半截"会当成传成功。
//
// ================================================================
package fileproc

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
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
	// 不配的话每次派发都要先把它整份搬到远端——搬 20MB 字体去换一次 fpdf2 排版，纯亏。
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
//
// ★ 2026-09-29 补三个字段，因为它们都是**同一类空转**：判据写在 Go 侧、远端从没吐过，
//
//	json.Unmarshal 把缺失键留成零值，那道腿结构上恒通过。远端 fpdexec 已同期上报，
//	这里接上才算数；配套反向锁见 fileproc_remote_test.go 的 TestProbeRejectsFailedSelftest
//	与 TestDispatchReadinessStatusWords。
type DispatchProbe struct {
	OK        bool              `json:"ok"`
	MemGB     float64           `json:"mem_gb"`
	ScriptSHA map[string]string `json:"script_sha"`
	// Selftest 用**指针**接：0 与"远端根本没吐这个键"在值上必须能区分开。
	// 曾经是 `int`，于是"删掉远端那个字段"这种改名式破坏会解成零值 0＝自检通过，
	// 就绪闸第三条腿当场变回恒真——而这正是 09-28 那批空转判据的成因，不能留第二份土壤。
	// 远端 fpdexec 的 probe 现在无条件吐 `selftest`（真实退出码），所以 nil 只有一种解释：对端不是这一版。
	Selftest   *int   `json:"selftest"`
	Expired    bool   `json:"expired"`
	PybinExist bool   `json:"pybin_exists"`
	Pymupdf    string `json:"pymupdf"`
	// ExpireDate：远端配置的到期日（YYYY-MM-DD）。此前结构体里连这个键都没有，
	// 等于「到期即拒」这件事主站侧只能靠人记住一个日期——现在它是读数。
	ExpireDate string `json:"expire_date"`
	Caps       struct {
		// RlimitAsSet：远端是否真的读到了内存帽配置（false＝fpd.env 或 fpd-bridge 那一环断了）。
		RlimitAsSet bool `json:"rlimit_as_set"`
		// Clamped：配置值高于系统硬上限 ⇒ setrlimit 会失败 ⇒ 「帽配了但没戴上」的现网形态。
		Clamped bool `json:"clamped"`
	} `json:"caps"`
}

// 进程内派发状态（就绪缓存 + 连续失败降级）。
var (
	// probeMu＋probeAt：★ 已不再是 sync.Once（2026-09-29 改造第三节）。
	//
	//	Once 的语义是"首次成功后永不重探"，于是**远端到期当天，一直在跑的主站进程仍报 online**，
	//	每次派发靠真实失败＋降级链兜住＝"能用是靠运气，不是靠判据"。现在按 TTL 重探（见 dispatchProbe）。
	probeMu  sync.Mutex
	probeRes *DispatchProbe
	probeErr error
	probeAt  time.Time
	// probeErrAt：★ 失败也要缓存一小段（probeFailBackoff），但只缓存 30s，不是一整个 TTL。
	//
	//	两头的坑都真踩过：sync.Once 时代"失败一次就永远不再探"⇒ 换件后不重启就永远 degraded；
	//	而完全不留缓存 ⇒ 远端一旦宕掉，**每一次 /api/health 都会现拨一次 ssh**（最坏 15s 一次），
	//	健康面自己先被放大成慢接口。30s 比健康检查的节奏慢得多、又比 10 分钟快得多，恢复后不用等太久。
	probeErrAt time.Time
	// probeFailBackoff 失败缓存时长（独立成变量是为了单测能把它压到 0 验"确实会重拨"）。
	probeFailBackoff = 30 * time.Second

	dispatchMu sync.Mutex
	// degradedUntil：连续失败后的静默期（§5.2），期间不再浪费一次 RTT 去拨一台坏机器。
	degradedUntil time.Time
	failStreak    int
)

// probeTTL 就绪缓存的重探周期（FILEPROC_DISPATCH_PROBE_TTL_SEC，默认 600s）。
// 为什么默认 10 分钟：到期判定最坏晚 10 分钟翻旧，而派发本身还有 `expired`＋远端 78 拒绝两层；
// 再密就是拿公网 RTT 换排场，这一条腿本来就是"增益路径"。
func probeTTL() time.Duration {
	if v := os.Getenv("FILEPROC_DISPATCH_PROBE_TTL_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 600 * time.Second
}

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

// DispatchReadiness 派发面的健康读数（★ 2026-09-29 第二节：到期日与内存帽要能在健康面看见）。
//
// 口径：**只出状态词，绝不出远端主机名/IP/绝对路径**，也不把 probe 的 JSON 原样透出
// （那是把一个外部机器的字段表挂到自家公开健康面上，字段一变就跟着变，没法写断言）。
// MemCap 四档是刻意分开的——"配置齐全但帽没戴上"（clamped）与"配置根本没读到"（off）
// 在现场是两件事，合成一档就看不出该去查 env 文件还是查系统硬上限。
type DispatchReadiness struct {
	Status     string `json:"dispatch"`          // off / online / degraded
	ExpireDate string `json:"dispatch_expire"`   // 远端到期日（取不到=空串，不是"没有到期"）
	MemCap     string `json:"dispatch_mem_cap"`  // on / clamped / off / unknown
	Selftest   string `json:"dispatch_selftest"` // pass / missing_asset / failed / unknown
}

// DispatchStatusWords 把一次 probe 的实值翻成状态词（DispatchReadiness 与单测共用同一份翻译，
// 免得"健康面显示 on、门禁判的是另一套"这种分叉长出来）。
func DispatchStatusWords(p *DispatchProbe) (expire, memCap, selftest string) {
	if p == nil {
		return "", "unknown", "unknown"
	}
	expire = p.ExpireDate
	switch {
	case p.Caps.Clamped:
		memCap = "clamped"
	case p.Caps.RlimitAsSet:
		memCap = "on"
	default:
		memCap = "off"
	}
	switch {
	case p.Selftest == nil:
		selftest = "unknown" // 对端没吐这个键：不是"自检通过"，是"不知道"
	case *p.Selftest == 0:
		selftest = "pass"
	case *p.Selftest == 2:
		selftest = "missing_asset"
	default:
		selftest = "failed"
	}
	return expire, memCap, selftest
}

// DispatchReadinessSnapshot 组一份健康面读数（三态＋到期日＋内存帽＋自检状态词）。
// ★ 它取的是**缓存的 probe**，不额外拨远端；派发关着时其余字段留空（"没配"不等于"配了但坏了"）。
func DispatchReadinessSnapshot() DispatchReadiness {
	st := DispatchStatus()
	r := DispatchReadiness{Status: st}
	if st == "off" {
		return r
	}
	// 只在缓存上取：dispatchProbe 命中 TTL 内缓存时零网络成本；过期会重探一次（同一把锁、同一判据）
	p, err := dispatchProbe(context.Background())
	if err != nil {
		// 探不到就把"取不到"如实写成 unknown，绝不填一个看起来正常的默认值
		r.ExpireDate, r.MemCap, r.Selftest = "", "unknown", "unknown"
		return r
	}
	r.ExpireDate, r.MemCap, r.Selftest = DispatchStatusWords(p)
	return r
}

// dispatchProbe 远端就绪探测（★ 按 TTL 缓存：首次成功后复用 probeTTL()，过期或从没成功过才重探）。
//
// 三条纪律，都是踩过的位置：
//  1. **静默期内不拨**（degradedUntil）：重探本身也要计入降级，否则等于拿公网 RTT 去反复拨一台已知坏的机器；
//  2. **临界区里不许调用**：本函数只在 DispatchRun 取远端闸**之前**、以及健康面用；
//     闸容量由 probe 的 mem_gb 反推（remoteGateMax），若在持闸后重探就形成同 goroutine 重入（2026-09-28 死锁本体）；
//  3. **锁内只跑 ssh**，不再回调任何会取闸/取锁的函数（Go 的 Mutex 不可重入，重入即永久挂死）。
func dispatchProbe(ctx context.Context) (*DispatchProbe, error) {
	probeMu.Lock()
	defer probeMu.Unlock()
	// 双检：拿到锁后先看别人是否已经把这一轮探完了
	if probeRes != nil && time.Since(probeAt) < probeTTL() {
		return probeRes, nil
	}
	// 失败短缓存：30s 内不重复拨（否则远端宕掉时每次健康检查都付一次最坏 15s 的 ssh）。
	// TTL=0 是显式"每次都真探"的档（单测与排障用），这时失败缓存也必须让位，否则"重探"这条腿名存实亡。
	if probeErr != nil && probeTTL() > 0 && time.Since(probeErrAt) < probeFailBackoff {
		return nil, probeErr
	}
	if probeRes == nil && time.Now().Before(degradedUntil) {
		return nil, probeErr
	}
	res, err := doDispatchProbe(ctx)
	if err != nil {
		// 失败只缓存 probeFailBackoff 这么久就允许重探（旧形态是 sync.Once：失败一次就永不翻身，
		// 换件不重启也修不好——2026-09-28 实测过一次）。成功读数 probeRes 保持到 TTL 到期。
		probeErr, probeErrAt = err, time.Now()
		return nil, err
	}
	probeRes, probeErr, probeAt = res, nil, time.Now()
	return probeRes, nil
}

// doDispatchProbe 真跑一次 `fpdexec.py probe` 并解 JSON。
//
// ⚠️ stdin 第一行必须是 **base64(header)**，与 §4 的协议一致（DispatchRun 走的是同一条编码）。
//
//	2026-09-28 单测实测：这里曾直接发裸 JSON，远端 fpdexec 解不出来 ⇒ 探测恒失败，
//	而失败被降级链兜住后表现为"派发从没生效过"，是最难发现的一类静默死分支。
func doDispatchProbe(ctx context.Context) (*DispatchProbe, error) {
	header, err := encodeDispatchHeader(map[string]interface{}{"mode": "probe"})
	if err != nil {
		return nil, err
	}
	stdout, stderr, err := dispatchSSH(ctx, header, nil, subTimeout())
	if err != nil {
		return nil, fmt.Errorf("远端探针失败: %w\n%s", err, truncateTail(stderr))
	}
	line := strings.TrimSpace(lastNonEmptyLine(string(stdout)))
	var p DispatchProbe
	if err := json.Unmarshal([]byte(line), &p); err != nil {
		return nil, fmt.Errorf("远端探针返回不可解析（%s）: %w", line, err)
	}
	// ★ 就绪四腿（`selftest` 缺失也算一腿）。`Selftest != 0` 这一条在 09-28 是**空转**的：远端 probe 从没吐过该键，
	//
	//	反序列化永远是零值 0 ⇒ 判据恒通过（"深判据一次都没执行过"）。远端 09-29 已改成真跑真上报，
	//	主站侧接上真值并把字段改成指针（nil＝没吐键＝不就绪），配套反向锁见
	//	TestProbeRejectsFailedSelftest 与 TestProbeRejectsMissingSelftestField——
	//	零值陷阱不配反向锁，下一次谁再删掉远端那个字段，主站照样静默放行。
	if p.Selftest == nil {
		return nil, errors.New("远端未就绪：probe 没上报 selftest 字段（对端不是 fpdexec 09-29 版，深判据无从执行）")
	}
	if !p.OK || p.Expired || *p.Selftest != 0 {
		return nil, fmt.Errorf("远端未就绪 ok=%v expired=%v selftest=%d", p.OK, p.Expired, *p.Selftest)
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

// dispatchWorkRoot 远端工作目录根（fpdexec 里叫 WORK=root/w）。
// put/stat/get 的 `rel` 就是相对这里；run 的 argv 用它的绝对形态。两处同源，别各拼一遍。
func dispatchWorkRoot() string { return dispatchRoot() + "/w" }

// dispatchSessionDir 本次会话在远端的工作目录（会话粒度：同一工单多语种共用一个目录）。
func dispatchSessionDir(sessionID string) string {
	return dispatchWorkRoot() + "/" + sanitizeSession(sessionID)
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

// dispatchRemoteName 把声明的产物名换成远端会话目录里的安全文件名（前缀 out_ 便于与输入件区分）。
//
// ★ 保留扩展名（2026-09-29）：早先直接 `"out_" + sanitizeSession(rel)`，于是 `out.pdf` 变成 `out_pdf`。
//
//	远端脚本现在没按后缀分派，但"产物名丢掉后缀"这类形态变更迟早让某个下游按后缀认格式时踩坑，
//	而远端目录里的文件名也是要给人排障看的。故只清洗主干，扩展名过白名单后原样带上。
//	白名单：以 "." 开头、1～8 个字母/数字；不合就把整个名字清洗掉（宁可丢后缀，也不放宽字符集）。
func dispatchRemoteName(rel string) string {
	stem := rel
	ext := path.Ext(rel)
	if ext != "" && validRemoteExt(ext) {
		stem = strings.TrimSuffix(rel, ext)
		return "out_" + sanitizeSession(stem) + strings.ToLower(ext)
	}
	return "out_" + sanitizeSession(rel)
}

// validRemoteExt 扩展名白名单：`.pdf` `.docx` `.txt` 这类；其余（含点号、超长、空主干）一律不合。
func validRemoteExt(ext string) bool {
	if len(ext) < 2 || len(ext) > 9 || ext[0] != '.' {
		return false
	}
	for i := 1; i < len(ext); i++ {
		c := ext[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if !ok {
			return false
		}
	}
	return true
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
	// probe 现在按 TTL 重探（见 dispatchProbe），到期当天跑着的进程也会翻旧成 degraded 而不是永远 online。
	if _, err := dispatchProbe(ctx); err != nil {
		noteDispatchResult(ctx, false, err)
		return nil, err
	}

	release := acquireRemoteGateCtx(ctx)
	defer release()

	dir := dispatchSessionDir(sessionID)
	// ① ★ 2026-09-29：这里**不再有"建远端目录"这一腿**。裸 `ssh host 'mkdir -p …'` 在这台机器上是死路
	//
	//	（sshd 的 ForceCommand 把它顶成 fpd-bridge，bridge 只认 stdin 协议 → 实测退 78、目录压根没建出来），
	//	而 fpdexec 的 cmd_put 内部就是 makedirs(dirname)，所以上传输入件时目录顺手就建好了。
	//	只剩一种会话没有输入件可上传：pdfwrite 降级重建链（原文/译文都在 payload 里）——
	//	它走 ensureDispatchDir 那一次 marker put，仍然在同一条协议里，不多造第二条通路。
	uploaded := false

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
		safeRel := dispatchRemoteName(rel)
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
		// ★ 只有**绝对路径**才有资格当输入件（2026-09-29 由单测抓到的真缺陷）：
		//
		//	argv[0] 在主站形态下是裸脚本名 "pdf_overlay.py"，而这份 .py 与二进制同目录，
		//	只要进程的工作目录恰好落在脚本目录（本地 `go test` 就是这个形态），旧的 `os.Stat(a)`
		//	就会命中 ⇒ argv[0] 被换成远端路径 ⇒ 远端第一个参数不再是脚本名，派发必败、又被降级链
		//	兜成"看起来从没生效过"的静默死分支。**判据不许挂在进程 CWD 上**；
		//	且远端 _check_paths 本来就只认 WORK 之下的绝对路径，相对路径即使搬过去也跑不通。
		if !filepath.IsAbs(a) {
			remoteArgs = append(remoteArgs, a) // 子命令/语种码/脚本名：原样透传
			continue
		}
		if fi, err := os.Stat(a); err == nil && !fi.IsDir() {
			rel := "in_" + filepath.Base(a)
			if err := dispatchPut(ctx, a, dir+"/"+rel); err != nil {
				noteDispatchResult(ctx, false, err)
				return nil, fmt.Errorf("输入搬运失败 %s: %w", a, err)
			}
			uploaded = true // ★ 这一次 put 同时把会话目录建出来了（见上方 ①）
			remoteArgs = append(remoteArgs, dir+"/"+rel)
			continue
		}
		remoteArgs = append(remoteArgs, a) // 绝对路径但本地不存在：原样透传，由远端守卫判死
	}

	// ③b 没有输入件的会话（pdfwrite 降级重建链）补一次 marker put 建目录——
	//     走的是同一条 fpdexec 协议，**不是**把裸 ssh mkdir 换个写法捡回来。
	if !uploaded {
		if err := ensureDispatchDir(ctx, dir); err != nil {
			noteDispatchResult(ctx, false, err)
			return nil, fmt.Errorf("远端会话目录准备失败: %w", err)
		}
	}

	header, err := encodeDispatchHeader(map[string]interface{}{
		"mode":    "run",
		"script":  script,
		"argv":    remoteArgs,
		"outputs": outRels,
	})
	if err != nil {
		return nil, err
	}
	stdout, stderr, rerr := dispatchSSH(ctx, header, stdin, fileprocTimeout())
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

// ---------------- fpdexec 协议原语（唯一外部二进制：ssh，统一走 BatchMode） ----------------

// sshBaseArgs ssh 通用参数（BatchMode=yes：密钥不可读时必须失败而不是静默等口令，见 §6.3）
func sshBaseArgs() []string {
	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=15", "-o", "StrictHostKeyChecking=accept-new"}
	if k := dispatchKey(); k != "" {
		args = append(args, "-i", k)
	}
	return args
}

// dispatchCmd 走 ssh 调 fpdexec.py 的**唯一出口**（probe/selftest/run/put/stat/get 全在这一条上）。
//
//	协议：stdin 第一行是 base64(header)，其后紧跟 payload（put＝文件本体，run＝译文映射 JSON）。
//	远端 main() 用 `sys.stdin.buffer.readline()` 取 header、再 `read()` 取 payload，
//	所以 header 行**必须自带换行符**，且 payload 只在 run/put 两种模式读——
//	给 stat/get 也留着管道不关，远端就会一路等到墙钟超时（症状是"远端没报错但派发死活不返回"）。
//
// ★ 为什么不走 here-string：stderr 与退出码都要能拿回来给排障用，必须 exec.Command。
//
// ★★ 这里**刻意不取远端闸**（gate=nil），理由是两个实测踩到的永久死锁（2026-09-28）：
//
//	① 重入死锁：闸的容量由探测回的 mem_gb 自适应算出 ⇒ "取闸"会触发"闸初始化"，
//	   初始化又要跑一次探测 ssh；若探测也取闸，就形成同 goroutine 重入 ⇒ 进程永久挂死。
//	② 自持死锁：DispatchRun 已经为**整个会话**持有了那一个名额（2G 机容量=1），
//	   会话内部的传输原语再去取一次 ⇒ 自己等自己。
//	⇒ 结论：远端闸只在 DispatchRun 的会话层取一次，传输原语一律不取闸。
//	  并发上限的真正含义是"同时在远端的会话数"，不是"同时在跑的 ssh 进程数"。
//
// to 非 nil 时 stdout **直接写进 to**（回拉产物走这里：几 MB 到几十 MB 的字节必须边收边落盘，
// 内存缓冲版会把超过 4MB 的部分静默丢掉，于是"截半截的 PDF"被当成品交付）。
func dispatchCmd(ctx context.Context, headerLine []byte, payload io.Reader, to io.Writer,
	timeout time.Duration) ([]byte, []byte, error) {
	var stdin io.Reader = bytes.NewReader(headerLine)
	if payload != nil {
		stdin = io.MultiReader(bytes.NewReader(headerLine), payload)
	}
	args := append(sshBaseArgs(), dispatchHost(), dispatchPyBin(), dispatchFpdexec)
	if to != nil {
		// 流式落盘：stdout 直接进调用方的文件句柄，这里只回收 stderr 给排障
		stderr, err := runSubprocessGatedStream(ctx, timeout, "ssh", args, stdin, to, nil)
		return nil, stderr, err
	}
	var buf limitedBuffer
	buf.limit = 4 << 20
	stderr, err := runSubprocessGatedStream(ctx, timeout, "ssh", args, stdin, &buf, nil)
	return buf.b.Bytes(), stderr, err
}

// dispatchSSH 内存缓冲版调用（probe/run 用：它们的 stdout 就是几十 KB 的 JSON/脚本文本）。
func dispatchSSH(ctx context.Context, headerLine, payload []byte, timeout time.Duration) ([]byte, []byte, error) {
	var r io.Reader
	if payload != nil {
		r = bytes.NewReader(payload)
	}
	return dispatchCmd(ctx, headerLine, r, nil, timeout)
}

// encodeDispatchHeader 把 header 编成协议要求的"base64(一行 JSON) + 换行"。
// 收成一个函数是因为这条编码在四条腿上都要用；抄两遍就迟早出现「probe 编码对了、put 忘了换行」。
func encodeDispatchHeader(h map[string]interface{}) ([]byte, error) {
	hb, err := json.Marshal(h)
	if err != nil {
		return nil, err
	}
	return append([]byte(base64.StdEncoding.EncodeToString(hb)), '\n'), nil
}

// dispatchRelOf 把远端**绝对路径**换成 fpdexec 的 put/stat/get 认的 `rel`（相对 w/ 的那一段）。
//
// ★ 两套路径口径不能混（这是我 09-29 真机自证时踩的第一坑）：
//
//	put/stat/get 的 rel 相对 w/；而 run 的 argv 必须是**绝对路径且落在 w/ 下**，
//	由远端 _check_paths 机械拒绝越界。所以这里只做"剥掉 w/ 前缀"，绝不反过来把 run 的 argv 相对化。
func dispatchRelOf(remoteAbs string) (string, error) {
	// ★ 分隔符写死 "/"：这些是**远端（Linux）路径**，不是本地路径。
	// 拿 os.PathSeparator 会在 Windows 交叉编译/本机排障时把比较变成恒假（另一类静默死分支）。
	work := path.Clean(dispatchWorkRoot())
	clean := path.Clean(remoteAbs)
	if clean != work && !strings.HasPrefix(clean, work+"/") {
		return "", fmt.Errorf("远端路径不在工作目录之下: %s", clean)
	}
	rel := strings.TrimPrefix(clean, work+"/")
	if rel == "" || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("远端相对路径不合法: %q", rel)
	}
	return rel, nil
}

// sha256File 流式算本地文件的 sha256 与字节数（不整读进内存：派发省的就是内存）。
func sha256File(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// fpdPut 把 payload 落到远端绝对路径，并把远端回执的 size/sha256 与本地算出的**逐字节比一次**。
//
// ★ 为什么必须比：改造前用 scp，"传成功"的判据只有退出码，而 09-29 实测它在 ForceCommand 下
//
//	一会儿 exit=1、一会儿 exit=255 且**没有任何输出**——两轮取回码都不一致，说明错误码不能当判据。
//	现在两侧都有哈希：传半截/传错文件当场暴露，且远端 cmd_put 自己还校验落盘字节数（双保险）。
//	注意别按远端错误码分支判因，只按"字节对不上"判——超限（FPD_MAX_PUT_MB=80）就是非零退出＋一句文案。
func fpdPut(ctx context.Context, remoteAbs string, payload io.Reader, wantSHA string, wantSize int64) error {
	rel, err := dispatchRelOf(remoteAbs)
	if err != nil {
		return err
	}
	header, err := encodeDispatchHeader(map[string]interface{}{"mode": "put", "rel": rel})
	if err != nil {
		return err
	}
	stdout, stderr, err := dispatchCmd(ctx, header, payload, nil, fileprocTimeout())
	if err != nil {
		return fmt.Errorf("远端接收失败: %w\n%s", err, truncateTail(stderr))
	}
	var ack struct {
		OK     bool   `json:"ok"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	}
	line := lastNonEmptyLine(string(stdout))
	if e := json.Unmarshal([]byte(line), &ack); e != nil {
		return fmt.Errorf("远端 put 回执不可解析: %s", line)
	}
	if !ack.OK || ack.SHA256 == "" {
		return fmt.Errorf("远端 put 回执不合法: %s", line)
	}
	if ack.Size != wantSize || !strings.EqualFold(ack.SHA256, wantSHA) {
		return fmt.Errorf("上传字节不等值 本地 size=%d sha=%s / 远端 size=%d sha=%s",
			wantSize, wantSHA, ack.Size, ack.SHA256)
	}
	return nil
}

// dispatchPut 上传单个本地文件到远端会话目录（顺带把会话目录建出来——fpdexec 的 cmd_put
// 内部就是 os.makedirs(dirname)，所以**不再有裸 ssh mkdir 这条腿**）。
func dispatchPut(ctx context.Context, local, remote string) error {
	wantSHA, size, err := sha256File(local)
	if err != nil {
		return fmt.Errorf("本地输入件读不了 %s: %w", filepath.Base(local), err)
	}
	f, err := os.Open(local)
	if err != nil {
		return err
	}
	defer f.Close()
	return fpdPut(ctx, remote, f, wantSHA, size)
}

// ensureDispatchDir 保证远端会话目录存在（★ 只在"这一单没有输入件"时才走）。
//
// pdfwrite 那条腿的原文与译文都在 payload 里、没有输入文件，于是没有任何一次 put 顺带建目录，
// 而 run 的产物路径需要一个已存在的目录（cmd_run 自己不建）。
// 这里**不造第二条 ssh 协议**：用一次 1 字节的 marker put 达到同样效果（cmd_put 会 makedirs），
// 代价是多一次往返，但只在降级重建链上发生，且仍在同一条 ForceCommand 允许的协议里。
func ensureDispatchDir(ctx context.Context, dir string) error {
	const marker = "k"
	sum := sha256.Sum256([]byte(marker))
	return fpdPut(ctx, dir+"/.keep", strings.NewReader(marker),
		hex.EncodeToString(sum[:]), int64(len(marker)))
}

// dispatchStat 查远端文件的 size 与 sha256（排障腿：主站报"产物取不到"时，
// 先分清是"远端没做出来"还是"做出来了没搬回"——09-29 实测远端 3s 就能做出 6MB 产物、
// 而回传要 11.8s，没有这一腿这两件事在现场完全长一个样）。
func dispatchStat(ctx context.Context, remote string) (int64, string, error) {
	rel, err := dispatchRelOf(remote)
	if err != nil {
		return 0, "", err
	}
	header, err := encodeDispatchHeader(map[string]interface{}{"mode": "stat", "rel": rel})
	if err != nil {
		return 0, "", err
	}
	stdout, stderr, err := dispatchCmd(ctx, header, nil, nil, subTimeout())
	if err != nil {
		return 0, "", fmt.Errorf("远端查尺寸失败: %w\n%s", err, truncateTail(stderr))
	}
	var ack struct {
		OK     bool   `json:"ok"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	}
	line := lastNonEmptyLine(string(stdout))
	if e := json.Unmarshal([]byte(line), &ack); e != nil {
		return 0, "", fmt.Errorf("远端 stat 回执不可解析: %s", line)
	}
	if !ack.OK || ack.SHA256 == "" {
		return 0, "", fmt.Errorf("远端 stat 回执不合法: %s", line)
	}
	return ack.Size, ack.SHA256, nil
}

// dispatchGet 回拉远端文件到本地临时文件：**先 stat 拿期望值，再流式收字节，落盘后逐字节比**。
//
// ★ fpdexec 的 get 刻意让 stdout 只放原始文件字节（不掺 JSON），就是为了这条比对能成立；
//
//	任何一句日志混进 stdout 都会让产物变成坏文件，所以这里也不能用内存缓冲版收。
func dispatchGet(ctx context.Context, remote, local string) error {
	wantSize, wantSHA, err := dispatchStat(ctx, remote)
	if err != nil {
		return err
	}
	rel, err := dispatchRelOf(remote)
	if err != nil {
		return err
	}
	header, err := encodeDispatchHeader(map[string]interface{}{"mode": "get", "rel": rel})
	if err != nil {
		return err
	}
	f, err := os.Create(local)
	if err != nil {
		return err
	}
	_, stderr, err := dispatchCmd(ctx, header, nil, f, fileprocTimeout())
	syncErr, closeErr := f.Sync(), f.Close()
	if err != nil {
		return fmt.Errorf("产物回拉中断: %w\n%s", err, truncateTail(stderr))
	}
	if syncErr != nil || closeErr != nil {
		return fmt.Errorf("产物落盘失败: sync=%v close=%v", syncErr, closeErr)
	}
	gotSHA, gotSize, err := sha256File(local)
	if err != nil {
		return fmt.Errorf("取回的产物读不了: %w", err)
	}
	if gotSize != wantSize || !strings.EqualFold(gotSHA, wantSHA) {
		return fmt.Errorf("回拉字节不等值 远端 size=%d sha=%s / 主站 size=%d sha=%s",
			wantSize, wantSHA, gotSize, gotSHA)
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
