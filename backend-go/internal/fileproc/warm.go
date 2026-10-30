// ============ warm.go · 职责说明 ============
// 能力常驻服务（capsvc）的 Go 侧协议客户端 + 回落 + 计数。
//
// 背景（方案二 §4.4）：OCR/ASR/TTS 每次调用都新起 Python 进程并重新加载 ONNX 权重，
// 冷启动是这条链上最大的固定成本。capsvc 把「进程 + 权重」常驻起来，但**任何一条不满足
// 都必须回落子进程，不许硬失败**——常驻服务是加速件，不是新的单点。
//
// 三条口径（改这里之前先读）：
//
//	① **回落只有一次**：`CAPSVC=off` / socket 不存在 / 连接超时 ⇒ 直接回落子进程；
//	   协议错误或响应超时 ⇒ 回落**一次**并记 fallback 计数。同一请求不重试两次以上
//	   （重试堆叠会把"上游坏"放大成"上游被打爆"）。
//	② **Go 侧超时不杀服务进程**：只 `conn.Close()`；服务端那次推理继续跑完、结果丢弃，
//	   由服务端记 `late_result`。一次超时 `Kill` 整个守护进程会连带打掉其它并发请求。
//	③ **warm 路径不进 procGate**：那道闸是给几百 MB 的转换子进程用的（1.6G 机器容量 1）。
//	   常驻能力走自己的 `capGate(cap)`（容量＝该能力 worker 数）；**子进程回落仍走 procGate**
//	   （保持现状，防内存尖峰）。
//
// 远端派发 `fpdexec`（fileproc_remote.go）**不接 capsvc**：那条腿在另一台机器上、
// 协议不同（AGENTS §一·12 的桩只认协议），继续走子进程。
// =============================================
package fileproc

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"translator/internal/observability"
)

// capsvc 协议与连接的默认档（socket 路径／连接超时／单帧上限）：都以 env 可覆盖，见各常量注释。
const (
	// CapsvcSocketEnv 覆盖 socket 路径（生产由 unit 注入，默认即是生产值）。
	CapsvcSocketEnv = "CAPSVC_SOCKET"
	// CapsvcDefaultSocket 默认 socket 路径（与 deploy/capsvc/translator-capsvc.service 同源）。
	CapsvcDefaultSocket = "/run/langcross/capsvc.sock"
	// CapsvcOffEnv 显式关闸：CAPSVC=off|0|false ⇒ 一次都不拨 socket，直接走子进程。
	CapsvcOffEnv = "CAPSVC"

	// capsvcFrameMax 单帧上限：8 MiB（与 capsvc.py 的 FRAME_MAX 同值，两侧改一侧必红）。
	capsvcFrameMax = 8 << 20
	// capsvcHealthTTL 健康面探测缓存：/api/health 是匿名公开面，不能每个请求都去拨一次。
	capsvcHealthTTL = 5 * time.Second
)

// ------------------------------------------------------------------ 帧结构与协议
// warmRequest / warmResponse 是协议的唯一 Go 侧定义（字段名与 capsvc.py 逐字对应）。
type warmRequest struct {
	ID        string   `json:"id"`
	Cap       string   `json:"cap"`
	Cmd       string   `json:"cmd"`
	Args      []string `json:"args"`
	StdinB64  string   `json:"stdin_b64"`
	TimeoutMS int      `json:"timeout_ms"`
}

// warmResponse capsvc 的一帧回执（字段与 capsvc.py 的响应逐字同源；ok=false 时看 variant/stderr）。
type warmResponse struct {
	ID        string `json:"id"`
	OK        bool   `json:"ok"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Variant   string `json:"variant"`
	ElapsedMS int    `json:"elapsed_ms"`
	Cold      int    `json:"cold"`
}

// warmResult 是「服务端确实跑完了」的结果（ok=false 且带 variant 也属于这一类：
// 那是能力自身的失败档位，与子进程路径同一个 ERR:<Variant>）。
type warmResult struct {
	Stdout  []byte
	Stderr  []byte
	Variant string // 失败档位（成功为空串）
	Cold    bool   // 本次是否触发了模型加载（A6 的唯一冷启动读数）
	Elapsed time.Duration
}

// capsvcDisabled CAPSVC=off|0|false 时显式关闸。未配置＝尝试使用（部署了就用）。
func capsvcDisabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(CapsvcOffEnv)))
	return v == "off" || v == "0" || v == "false"
}

// capsvcSocketPath 现读 socket 路径（每次现读，测试与多实例场景才能各指各的）。
func capsvcSocketPath() string {
	if v := strings.TrimSpace(os.Getenv(CapsvcSocketEnv)); v != "" {
		return v
	}
	return CapsvcDefaultSocket
}

// capsvcDialTimeout 连接预算：默认 300ms、钳 50~5000ms。
// 为什么单独一档：socket 是本地文件，连不上就是"没起来/权限不对"，
// 在这个面上等 5 秒等于把一次不可用放大成一次超时；而 50ms 的下界是防手滑配成 1ms。
func capsvcDialTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv("CAPSVC_DIAL_TIMEOUT_MS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n < 50 {
				n = 50
			}
			if n > 5000 {
				n = 5000
			}
			return time.Duration(n) * time.Millisecond
		}
	}
	return 300 * time.Millisecond
}

// newWarmID 生成请求 id（8 字节随机 hex）。A5 的"id 不串号"靠它 + 下方等值校验。
func newWarmID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 极罕见的熵源失败：退回纳秒时间戳，仍然保证同一次调用内唯一性校验成立。
		return fmt.Sprintf("t%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// writeWarmFrame 写一帧（uint32 大端长度 + JSON body）。长度字段的**字节序**是协议本体：
// capsvc_protocol_test.go 里有一条把长度改成小端就必须红的反证腿。
func writeWarmFrame(w io.Writer, body []byte) error {
	if len(body) > capsvcFrameMax {
		return fmt.Errorf("capsvc 请求帧超限: %d > %d", len(body), capsvcFrameMax)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}

// readWarmFrame 读一帧；长度超上限直接判协议错（服务端也会断开并记 oversize）。
func readWarmFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 {
		return nil, errors.New("capsvc 响应帧长度为 0")
	}
	if int(n) > capsvcFrameMax {
		return nil, fmt.Errorf("capsvc 响应帧超限: %d > %d", n, capsvcFrameMax)
	}
	body := make([]byte, int(n))
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

// ------------------------------------------------------------------ 回落计数（1 小时滑窗）
// fallback_1h 是健康面上唯一能回答「常驻腿到底有没有在干活」的读数。
// 用分钟桶而不是单个原子计数：单个计数在"部署当天掉了一次"之后会永久非零，
// 下一个人就再也分不清"今天还在回落"和"历史上回落过一次"。
type capsvcFallbackBucket struct {
	minute int64 // unix 分钟
	n      int64
}

// capsvcFallback 回落计数（进程内累计 + 最近一次原因）：/api/health.capsvc 的读数源。
var capsvcFallback = struct {
	mu      sync.Mutex
	buckets []capsvcFallbackBucket
	lastLog map[string]time.Time
}{lastLog: map[string]time.Time{}}

// recordCapsvcFallback 记一次回落：同一请求只回落到子进程一次，计数只增不清。
func recordCapsvcFallback(reason string) {
	now := time.Now()
	minute := now.Unix() / 60
	capsvcFallback.mu.Lock()
	// 只留最近 61 个分钟桶（60 分钟窗口 + 当前这一分钟）。
	keep := capsvcFallback.buckets[:0]
	for _, b := range capsvcFallback.buckets {
		if minute-b.minute <= 60 {
			keep = append(keep, b)
		}
	}
	capsvcFallback.buckets = keep
	if n := len(capsvcFallback.buckets); n > 0 && capsvcFallback.buckets[n-1].minute == minute {
		capsvcFallback.buckets[n-1].n++
	} else {
		capsvcFallback.buckets = append(capsvcFallback.buckets, capsvcFallbackBucket{minute: minute, n: 1})
	}
	// 日志节流：服务没部署时每个请求都会回落，逐条打日志等于把日志写满。
	last, ok := capsvcFallback.lastLog[reason]
	shouldLog := !ok || now.Sub(last) >= time.Minute
	if shouldLog {
		capsvcFallback.lastLog[reason] = now
	}
	capsvcFallback.mu.Unlock()
	if shouldLog {
		// 用观测包而不是标准 log：fileproc 的 log.Printf 受棘轮闸约束只减不增（AGENTS §一·2）。
		observability.Warn(context.Background(), "[capsvc] 回落子进程（常驻腿不可用）",
			"reason", reason, "fallback_1h", capsvcFallback1h())
	}
}

// capsvcFallback1h 最近 60 分钟的回落次数。
func capsvcFallback1h() int64 {
	minute := time.Now().Unix() / 60
	capsvcFallback.mu.Lock()
	defer capsvcFallback.mu.Unlock()
	var total int64
	for _, b := range capsvcFallback.buckets {
		if minute-b.minute <= 60 {
			total += b.n
		}
	}
	return total
}

// ------------------------------------------------------------------ 能力闸（capGate）
// 与 procGate 分闸：常驻能力不跟几百 MB 的转换子进程抢那一个名额。
// 容量＝该能力 worker 数（ocr 默认 2，asr/tts 固定 1）——与 capsvc.py 的
// `CAPSVC_OCR_WORKERS` 同源；两侧不一致时以服务端为准（这里只是客户端侧的礼貌排队）。
var capGateState = struct {
	once sync.Once
	mu   sync.Mutex
	m    map[string]chan struct{}
}{m: map[string]chan struct{}{}}

// capGateChannel 取某能力的并发闸通道（容量＝该能力 worker 数）：warm 路径不进 procGate。
func capGateChannel(cap string) chan struct{} {
	capGateState.mu.Lock()
	defer capGateState.mu.Unlock()
	if ch, ok := capGateState.m[cap]; ok {
		return ch
	}
	ch := make(chan struct{}, capsvcWorkerCount(cap))
	capGateState.m[cap] = ch
	return ch
}

// acquireCapGateCtx 可取消地取一个能力名额；返回释放函数（ctx 取消时返回 no-op）。
func acquireCapGateCtx(ctx context.Context, cap string) func() {
	g := capGateChannel(cap)
	select {
	case g <- struct{}{}:
		return func() { <-g }
	case <-ctx.Done():
		return func() {}
	}
}

// ------------------------------------------------------------------ 协议调用
// warmCall 向常驻服务发一个请求。
//
// 返回值约定：
//   - (res, true)：**服务端确实回复了**。res.OK 为 false 时 res.Variant 是能力自身的
//     失败档位（与子进程路径的 ERR:<Variant> 逐字同源），调用方按同一话术渲染，
//     **不回落**——那是一次合法的失败结果，重跑一遍子进程只是白烧时间。
//   - (nil, false)：常驻腿不可用（关闸/没部署/连不上/协议错/超时）⇒ 调用方回落子进程一次。
//     fallback 计数已在函数内记好。
func warmCall(ctx context.Context, capName, cmd string, args []string, timeout time.Duration) (*warmResult, bool) {
	return warmCallEx(ctx, capName, cmd, args, timeout, true)
}

// warmCallEx recordFallback=false 只用于**健康探测**：探测失败不是业务回落，
// 记进 fallback_1h 会让"服务没部署"这个正常态把计数越推越高，读数自己污染自己。
func warmCallEx(ctx context.Context, capName, cmd string, args []string, timeout time.Duration, recordFallback bool) (*warmResult, bool) {
	fail := func(reason string) (*warmResult, bool) {
		if recordFallback {
			recordCapsvcFallback(reason)
		}
		return nil, false
	}
	if capsvcDisabled() {
		return fail("off")
	}
	if err := ctx.Err(); err != nil {
		// 调用方已取消：不是服务的锅，不记 fallback（记了会把"用户取消"读成"服务坏了"）。
		return nil, false
	}
	sock := capsvcSocketPath()
	if _, err := os.Stat(sock); err != nil {
		return fail("no_socket")
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	dialCtx, cancel := context.WithTimeout(ctx, capsvcDialTimeout())
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(dialCtx, "unix", sock)
	if err != nil {
		return fail("dial")
	}
	defer conn.Close()

	deadline := time.Now().Add(timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)

	id := newWarmID()
	body, err := json.Marshal(warmRequest{
		ID: id, Cap: capName, Cmd: cmd,
		Args: append([]string{}, args...), StdinB64: "",
		TimeoutMS: int(timeout / time.Millisecond),
	})
	if err != nil {
		return fail("marshal")
	}
	if err := writeWarmFrame(conn, body); err != nil {
		return fail("write")
	}
	raw, err := readWarmFrame(conn)
	if err != nil {
		// 超时/半截体/超长帧都在这一档：Go 侧只关连接（defer），**不杀服务进程**。
		return fail("read")
	}
	var resp warmResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fail("bad_json")
	}
	if resp.ID != id {
		// 串号（A5 的唯一防线）：宁可按协议错回落，也不把别人的结果当自己的。
		return fail("id_mismatch")
	}
	return &warmResult{
		Stdout:  []byte(resp.Stdout),
		Stderr:  []byte(resp.Stderr),
		Variant: resp.Variant,
		Cold:    resp.Cold == 1,
		Elapsed: time.Duration(resp.ElapsedMS) * time.Millisecond,
	}, true
}

// ------------------------------------------------------------------ 健康面
// CapsvcCapHealth 单能力读数：loaded/busy/idle_s（**不含路径、权重名、主机名**）。
type CapsvcCapHealth struct {
	Loaded bool `json:"loaded"`
	Busy   int  `json:"busy"`
	IdleS  int  `json:"idle_s"`
}

// CapsvcHealth /api/health 的 capsvc 段。
// 口径同 dispatch 那一族（AGENTS §一·12）：**只出状态词与计数，绝不出路径/主机名**。
//
//	state=off  没部署（socket 不存在，正常态）
//	state=ok   探测成功
//	state=down 部署了但连不上/坏（要去看）
type CapsvcHealth struct {
	State      string                     `json:"state"`
	ModelLoads int64                      `json:"model_loads"`
	Fallback1h int64                      `json:"fallback_1h"`
	Caps       map[string]CapsvcCapHealth `json:"caps"`
}

// capsvcHealthCache 健康探测缓存：避免每次 /api/health 都拨一次 socket（抖动时只留状态词）。
var capsvcHealthCache = struct {
	mu   sync.Mutex
	at   time.Time
	snap CapsvcHealth
}{}

// ResetCapsvcHealthCache 测试用：清掉 5s 探测缓存（换 socket 的夹具必须调它，
// 否则"换了个不存在的 socket"永远读到上一份快照）。
func ResetCapsvcHealthCache() {
	capsvcHealthCache.mu.Lock()
	capsvcHealthCache.at = time.Time{}
	capsvcHealthCache.snap = CapsvcHealth{}
	capsvcHealthCache.mu.Unlock()
}

// CapsvcHealthSnapshot 返回健康面读数（5 秒 TTL 缓存；**永不返回错误**——
// 健康检查自己坏掉不该把 /api/health 打成 5xx）。
func CapsvcHealthSnapshot() CapsvcHealth {
	capsvcHealthCache.mu.Lock()
	if time.Since(capsvcHealthCache.at) < capsvcHealthTTL && !capsvcHealthCache.at.IsZero() {
		snap := capsvcHealthCache.snap
		capsvcHealthCache.mu.Unlock()
		snap.Fallback1h = capsvcFallback1h()
		return snap
	}
	capsvcHealthCache.mu.Unlock()

	snap := probeCapsvcHealth()
	snap.Fallback1h = capsvcFallback1h()

	capsvcHealthCache.mu.Lock()
	capsvcHealthCache.at = time.Now()
	capsvcHealthCache.snap = snap
	capsvcHealthCache.mu.Unlock()
	return snap
}

// probeCapsvcHealth 真探一次：socket 不存在 ⇒ off；连不上/没回 ⇒ down；正常 ⇒ ok。
// 这条腿**不记 fallback**（健康探测不是业务请求，记进去会让 fallback_1h 自己污染自己）。
func probeCapsvcHealth() CapsvcHealth {
	snap := CapsvcHealth{State: "off", Caps: map[string]CapsvcCapHealth{}}
	if capsvcDisabled() {
		return snap
	}
	sock := capsvcSocketPath()
	if _, err := os.Stat(sock); err != nil {
		return snap
	}
	res, ok := warmCallEx(context.Background(), "svc", "status", nil, 2*time.Second, false)
	if !ok || res == nil || res.Variant != "" {
		snap.State = "down"
		return snap
	}
	var status struct {
		ModelLoads int64 `json:"model_loads"`
		Caps       map[string]struct {
			Loaded bool `json:"loaded"`
			Busy   int  `json:"busy"`
			IdleS  int  `json:"idle_s"`
		} `json:"caps"`
	}
	if err := json.Unmarshal(res.Stdout, &status); err != nil {
		snap.State = "down"
		return snap
	}
	snap.State = "ok"
	snap.ModelLoads = status.ModelLoads
	for name, c := range status.Caps {
		snap.Caps[name] = CapsvcCapHealth{Loaded: c.Loaded, Busy: c.Busy, IdleS: c.IdleS}
	}
	return snap
}
