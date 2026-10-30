// ============================================================================
// capsvc_client_test.go —— 常驻腿客户端（warm.go / warm_caps.go）的行为锁（方案二 A2~A5）。
//
// 为什么这些腿必须真起一个 capsvc 进程：客户端的所有判据都是**协议与时序**的
// （帧、deadline、id 回显、回落计数），假一个 socket 只能测到自己写的桩。
// 所以这里用 `capsvc.py --selftest` 同一份服务本体，只把它 import 的能力脚本换成
// 一个假 `ocr_recognize.py`（协议级夹具：只认 `cmd_probe`/`cmd_image` 两个函数）。
//
// 夹具口径（AGENTS §一·12「桩只认协议」）：假脚本**只实现真实脚本的契约**
// （返回码 + stdout JSON + `ERR:<Variant>` 行），不模拟任何内部实现细节；
// 换一个真实实现时这些用例不该红，只有协议变了才该红。
//
// 本机没有 python3 时**优雅跳过**（t.Skip）：跳过的是"真起服务"的这层，
// 纯 Go 侧的帧编解码腿仍在 capsvc_protocol_test.go 里跑。
// ============================================================================
package fileproc

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeOcrModule 假 ocr_recognize 模块：协议级夹具（只按真实契约回应）。
//   - 正常图 ⇒ stdout 一份 JSON（text/boxes/confs）
//   - asset*/corrupt*/empty* ⇒ stderr 打 `ERR:<Variant>:` 并退 2（与真脚本同形）
//   - slow* ⇒ 睡 2s（A4 的超时夹具）
const fakeOcrModule = `import json, os, sys, time

MODULES = []
ASSETS_DIR = None

def cmd_probe():
    print("OK: fake ocr ready")
    return 0

def cmd_image(path):
    name = os.path.basename(path)
    if name.startswith("slow"):
        time.sleep(2.0)
        print(json.dumps({"text": "slow done", "boxes": [[0, 0, 10, 10]], "confs": [0.9]}))
        return 0
    if name.startswith("asset"):
        print("ERR:Asset:fake 资产缺失", file=sys.stderr)
        return 2
    if name.startswith("corrupt"):
        print("ERR:ImageCorrupt:fake 坏图", file=sys.stderr)
        return 2
    if name.startswith("empty"):
        print("ERR:Empty:fake 空图", file=sys.stderr)
        return 2
    print(json.dumps({"text": "fake " + name, "boxes": [[0, 0, 10, 10]],
                      "confs": [0.9], "confidence": 0.9}, ensure_ascii=False))
    return 0
`

// capsvcTestPython 取一个可用解释器：FILEPROC_PYTHON_BIN 优先（与 pyBin 判定链同序），
// 否则 PATH 里的 python3。两者都没有 ⇒ **跳过**（不是失败：本机缺依赖不是产品缺陷）。
func capsvcTestPython(t *testing.T) string {
	t.Helper()
	if v := strings.TrimSpace(os.Getenv("FILEPROC_PYTHON_BIN")); v != "" {
		if st, err := os.Stat(v); err == nil && !st.IsDir() {
			return v
		}
	}
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Skip("本机没有 python3 ⇒ 真起 capsvc 的协议腿跳过（纯 Go 帧腿仍在 protocol 测试里跑）")
	return ""
}

// startFakeCapsvc 在临时目录里真起一个 capsvc（服务本体＝仓库现读那份，能力脚本＝假模块），
// 并把 CAPSVC_SOCKET 指过去。返回 socket 路径。
func startFakeCapsvc(t *testing.T) string {
	t.Helper()
	py := capsvcTestPython(t)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("取工作目录失败: %v", err)
	}
	dir, err := os.MkdirTemp("", "cs")
	if err != nil {
		t.Fatalf("建临时目录失败: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	if err := copyFile(filepath.Join(wd, "capsvc.py"), filepath.Join(dir, "capsvc.py")); err != nil {
		t.Fatalf("拷 capsvc.py 失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ocr_recognize.py"), []byte(fakeOcrModule), 0o644); err != nil {
		t.Fatalf("写假能力模块失败: %v", err)
	}

	sock := filepath.Join(dir, "s.sock")
	var out bytes.Buffer
	cmd := exec.Command(py, filepath.Join(dir, "capsvc.py"), "--socket", sock)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CAPSVC_IDLE_UNLOAD_SEC=0")
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("起 capsvc 失败: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("capsvc 提前退出: %v\n%s", err, out.String())
		case <-time.After(20 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("capsvc 10s 内没起来\n%s", out.String())
		}
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
	})
	t.Setenv(CapsvcSocketEnv, sock)
	t.Setenv(CapsvcOffEnv, "")
	ResetCapsvcHealthCache()
	return sock
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

// A2：服务在时**不 spawn 子进程**（把子进程解释器指成 /bin/false 仍然成功）。
// 判据只能落在 warm 层：ExtractImageOCR 前面还有一道 OcrAvailable()（本机没有
// onnxruntime/权重，那道闸必然为 false），所以这里直接验"常驻腿拿到的结果能解析、
// 且 fallback 计数一动不动"——真 spawn 过子进程就一定会记 fallback。
func TestCapsvcClientServesWithoutSubprocess(t *testing.T) {
	startFakeCapsvc(t)
	t.Setenv("FILEPROC_PYTHON_BIN", "/bin/false") // 子进程腿此生必死
	before := capsvcFallback1h()
	res, ok := warmOcrImage(context.Background(), "ok.png", 5*time.Second)
	if !ok {
		t.Fatalf("服务在时 warmOcrImage 应成功（fallback=%d）", capsvcFallback1h()-before)
	}
	if res.Variant != "" {
		t.Fatalf("期望无失败档位，实得 %q（stderr=%s）", res.Variant, res.Stderr)
	}
	if got := capsvcFallback1h() - before; got != 0 {
		t.Fatalf("常驻腿成功时 fallback 不许增长，实增 %d", got)
	}
	e := &Extractor{}
	if err := parseOcrJSON(string(res.Stdout), e, "ok.png"); err != nil {
		t.Fatalf("常驻腿产物解析失败: %v", err)
	}
	if len(e.Texts) != 1 || !strings.Contains(e.Texts[0], "ok.png") {
		t.Fatalf("常驻腿文本不对: %#v", e.Texts)
	}
}

// A3：服务不在时**回落子进程**且工单仍完成；fallback 计数 +1。
func TestCapsvcClientFallsBackToSubprocess(t *testing.T) {
	py := capsvcTestPython(t)
	t.Setenv(CapsvcSocketEnv, filepath.Join(t.TempDir(), "absent.sock"))
	ResetCapsvcHealthCache()

	before := capsvcFallback1h()
	if _, ok := warmOcrImage(context.Background(), "ok.png", 3*time.Second); ok {
		t.Fatalf("socket 不在时 warmOcrImage 必须返回 ok=false（回落信号）")
	}
	if got := capsvcFallback1h() - before; got < 1 {
		t.Fatalf("回落必须记 fallback，实增 %d", got)
	}

	// 子进程回落腿：用假脚本（协议级桩）证明"回落之后这一单仍然完成"。
	dir := t.TempDir()
	script := filepath.Join(dir, "fake_ocr.py")
	if err := os.WriteFile(script, []byte(`import json,sys
print(json.dumps({"text":"fallback line","boxes":[[0,0,10,10]],"confs":[0.9]}))
`), 0o644); err != nil {
		t.Fatalf("写假脚本失败: %v", err)
	}
	t.Setenv(OcrScriptEnv, script)
	t.Setenv("FILEPROC_PYTHON_BIN", py)

	e := &Extractor{}
	if err := extractImageOCRSubprocess(context.Background(), "x.png", e); err != nil {
		t.Fatalf("回落子进程腿失败: %v", err)
	}
	if len(e.Texts) != 1 || e.Texts[0] != "fallback line" {
		t.Fatalf("回落腿文本不对: %#v", e.Texts)
	}
}

// A4：单请求超时只影响该请求——第二个并发请求仍被服务，且**进程未被杀**。
// 关键设计：ctx 的 deadline 比 timeout 参数短，让"客户端超时"成为确定行为
// （服务端 timeout_ms 仍是长的，所以它那边会正常跑完、记 late_result）。
func TestCapsvcClientTimeoutDoesNotKillService(t *testing.T) {
	startFakeCapsvc(t)

	type outcome struct {
		ok  bool
		err error
	}
	fast := make(chan outcome, 1)
	go func() {
		res, ok := warmOcrImage(context.Background(), "ok.png", 3*time.Second)
		var err error
		if ok && res.Variant != "" {
			err = ocrWarmError(res)
		}
		fast <- outcome{ok: ok, err: err}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	t0 := time.Now()
	if _, ok := warmOcrImage(ctx, "slow.png", 5*time.Second); ok {
		t.Fatalf("客户端 deadline 已到，warmOcrImage 必须返回 ok=false（不是等 5 秒）")
	}
	if spent := time.Since(t0); spent > 2*time.Second {
		t.Fatalf("客户端超时没有生效，花了 %s", spent)
	}

	select {
	case o := <-fast:
		if !o.ok || o.err != nil {
			t.Fatalf("并发请求不应受另一个请求超时影响: ok=%v err=%v", o.ok, o.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("并发请求被另一个请求的超时拖住了（说明服务/闸被串行化或进程被打掉）")
	}

	// 再确认进程还活着：健康探测仍 ok（若被 Kill，socket 消失 ⇒ off/down）。
	ResetCapsvcHealthCache()
	deadline := time.Now().Add(3 * time.Second)
	for {
		snap := CapsvcHealthSnapshot()
		if snap.State == "ok" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("超时之后服务不可用了（state=%s）⇒ 客户端超时把服务打掉了", snap.State)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A5：并发 8 请求 id 不串号、结果与请求一一对应。
func TestCapsvcClientConcurrentIDsDoNotMix(t *testing.T) {
	startFakeCapsvc(t)
	var wg sync.WaitGroup
	errs := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "ok" + string(rune('a'+i)) + ".png"
			res, ok := warmOcrImage(context.Background(), name, 5*time.Second)
			if !ok {
				errs <- name + ": 调用失败"
				return
			}
			if res.Variant != "" {
				errs <- name + ": variant=" + res.Variant
				return
			}
			if !strings.Contains(string(res.Stdout), name) {
				errs <- name + ": 结果串号了 → " + string(res.Stdout)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Error(msg)
	}
}

// 健康面三态（A8 的产品侧判定；api 层的接线由 server.go 那一行承担）：
// off（没部署）／down（部署了但连不上）／ok，且载荷**不含路径**。
func TestCapsvcHealthStates(t *testing.T) {
	// off：socket 不存在
	absent := filepath.Join(t.TempDir(), "absent.sock")
	t.Setenv(CapsvcSocketEnv, absent)
	t.Setenv(CapsvcOffEnv, "")
	ResetCapsvcHealthCache()
	snap := CapsvcHealthSnapshot()
	if snap.State != "off" {
		t.Fatalf("socket 不存在时期望 state=off，实得 %s", snap.State)
	}
	if snap.Caps == nil {
		t.Fatalf("caps 必须是空对象而不是 nil（JSON 出 null 会让前端判空崩溃）")
	}

	// down：路径在，但不是 socket
	notSock := filepath.Join(t.TempDir(), "not.sock")
	if err := os.WriteFile(notSock, []byte("x"), 0o644); err != nil {
		t.Fatalf("写占位文件失败: %v", err)
	}
	t.Setenv(CapsvcSocketEnv, notSock)
	ResetCapsvcHealthCache()
	if snap := CapsvcHealthSnapshot(); snap.State != "down" {
		t.Fatalf("socket 连不上时期望 state=down，实得 %s", snap.State)
	}

	// ok：真服务
	sock := startFakeCapsvc(t)
	ResetCapsvcHealthCache()
	snap = CapsvcHealthSnapshot()
	if snap.State != "ok" {
		t.Fatalf("服务在时期望 state=ok，实得 %s", snap.State)
	}
	if _, ok := snap.Caps["ocr"]; !ok {
		t.Fatalf("caps 里必须有 ocr（注册表声明即出现，不必等第一次调用）: %#v", snap.Caps)
	}
	blob, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	for _, leak := range []string{sock, "/tmp", "/run/", "/opt/"} {
		if strings.Contains(string(blob), leak) {
			t.Fatalf("健康段泄漏了内部路径 %q: %s", leak, blob)
		}
	}
}
