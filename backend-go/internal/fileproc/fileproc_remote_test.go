package fileproc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ============ fileproc_remote_test.go · 职责说明 ============
// 守护「远程派发」这条**默认关闭**的增益路径（改造方案 §4/§9/§10）。
//
// 这个文件存在的理由只有一条：派发一旦开错，症状是**静默的**——
//   · 关着的时候它必须**逐字节不存在**（§0-③）：不能多一次 ssh、不能改一次路径、不能动一次返回值；
//   · 开着的时候它最危险的失败不是报错，而是"**派发成功但产物没回来**"（§9-A1），
//     以及"库里记了一条指向远端机器的路径"（§9-A2，到期后必然取不到）。
// 这两类失败都不会让工单变红，所以只能靠断言钉住。
//
// 手法：用**假 ssh 桩**（临时目录里一个可执行脚本，插到 PATH 最前面）真跑 DispatchRun，
// 不拨任何真实主机；桩在本地开一个目录当远端 `w/` 的替身，put/stat/get 真的落盘、真的算 sha256，
// 于是"上传/取回"这两条腿是被**逐字节**验过的，不是被一句 exit 0 蒙过去的。
//
// ★★ 2026-09-29 桩改造（交接文档第一节，这是本文件最重要的一段）：
//
//	旧桩里那句 `*mkdir*) exit 0 ;;` 对建目录**无条件放行**，scp 同理被桩成成功搬运，
//	于是"目录已存在"在单测里永远成立——这套桩证明的是"我写的流程在我编的远端上能跑通"，
//	而不是"远端真有这条腿"。真机上 sshd 给 fpd 配了 ForceCommand，裸 mkdir 实测退 78、
//	scp 要么挂到墙钟要么静默非零退出：**两条腿在真机上是结构性死路，而绿灯一直在**。
//	⇒ 新桩只认 fpdexec 的 stdin 协议：调用形状不对（不带 fpdexec 那一段）一律照真机的样子退 78；
//	  scp 桩存在但**必红**（谁把 scp 腿写回来，这里当场失败并留下证据）。
//	  两条桩各自的"必红"形态由 TestFakeStubsThemselvesRejectDeadLegs 自证，
//	  防止桩哪天退化成恒放行（那才是这次事故真正的根因）。
//
// ★ 三条纪律（改本文件前先看）：
//  1. 不用 t.Skip：缺 python3/缺桩能力一律 t.Fatal（本仓多次把红灯洗成绿的教训）；
//  2. 每个用例跑前重置 probe 缓存与降级计数（缓存不清会串味）；
//  3. 负向锁必须配正向对照（AGENTS §一·5）：判"远端路径要红"就得同时判"主站路径要绿"。
// ================================================================

// ---------------- 桩与夹具 ----------------

// fakeBinDir 造一个只含 ssh/scp 两个假命令的目录，返回该目录（调用方插到 PATH 最前）。
//
// artifactMode 控制桩的"远端产物"行为：
//
//	"pdf"   → run 真的在会话目录里写出产物（正向对照）
//	"none"  → 什么都不写（远端没做出产物，A1 要判红的形态；回拉前 stat 就该报错）
//	"trunc" → 产物写全，但 get 只回前 4 个字节（★ 半截文件必须被 sha 比对抓住）
//
// 额外的桩行为都走 env（见脚本内注释）：FPD_FAKE_PROBE／FPD_FAKE_MAX_BYTES／FPD_FAKE_EXPIRED。
func fakeBinDir(t *testing.T, artifactMode string) string {
	t.Helper()
	dir := t.TempDir()

	ssh := `#!/bin/sh
# 假 ssh：只认 fpdexec 的 stdin 协议（base64(header) 一行 + payload 本体）。
# ★ 形状不对就照真机的样子退 78：真机 sshd 的 ForceCommand 会把任何命令顶成 fpd-bridge，
#   bridge 只把 stdin 交给 fpdexec，而 fpdexec 拿不到命令 ⇒ "stdin 缺 header 行"、rc=78。
#   这里刻意**写死**期望的命令形状（不跟 Go 侧的常量走）：哪天有人改回裸 ssh 或换了入口路径，
#   本桩必须红，逼他回来看这段注释。
set -u
WORK="${FPD_FAKE_WORK:?}"
VICTIM="${FPD_FAKE_VICTIM:-}"
HOST="${FPD_FAKE_HOST:?}"
EXPECTED="$HOST /opt/fpdispatch/.venv/bin/python3 /opt/fpdispatch/bin/fpdexec.py"
case "$*" in
  *"$EXPECTED"*) : ;;
  *)
    printf '%s\n' '[fpd-bridge] 忽略 SSH_ORIGINAL_COMMAND（协议只走 stdin）' >&2
    if [ -n "$VICTIM" ]; then printf 'ssh-dead-leg %s\n' "$*" >>"$VICTIM"; fi
    printf '%s\n' '[fpdexec] stdin 缺 header 行' >&2
    exit 78 ;;
esac

# stdin 先整份落临时文件再切：head/tail 直接读管道会把 payload 的尾巴吞掉，
# 而那正是 fpdexec.py 文件头骂的那类"远端正常退出、数据却是空的"的形态。
TMPD=$(mktemp -d) || exit 70
IN="$TMPD/in"; OUT="$TMPD/out"
cat > "$IN"
LINE=$(head -n 1 "$IN")
HDR=$(printf '%s' "$LINE" | base64 -d 2>/dev/null || printf '%s' "$LINE" | base64 -D 2>/dev/null || echo "")
if [ -z "$HDR" ]; then printf '%s\n' '[fpdexec] header 解析失败' >&2; exit 78; fi
sha_of() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1;
  else shasum -a 256 "$1" | cut -d' ' -f1; fi
}
size_of() { wc -c < "$1" | tr -d ' '; }

# rel（相对 w/）从 header 里取；header 由 Go 侧 json.Marshal 生成，键序固定、值只含安全字符
rel_of() { printf '%s' "$HDR" | sed -n 's/.*"rel":"\([^"]*\)".*/\1/p'; }

case "$HDR" in
  *'"mode":"probe"'*)
    if [ -n "${FPD_FAKE_PROBE_LOG:-}" ]; then printf 'probe\n' >>"$FPD_FAKE_PROBE_LOG"; fi
    case "${FPD_FAKE_PROBE:-ok}" in
      selftest2) printf '%s\n' '{"ok":true,"expired":false,"selftest":2,"expire_date":"2026-10-26","mem_gb":2,"caps":{"rlimit_as_mb":1423,"rlimit_as_set":true,"clamped":false}}' ;;
      selftest3) printf '%s\n' '{"ok":true,"expired":false,"selftest":3,"expire_date":"2026-10-26","mem_gb":2,"caps":{"rlimit_as_mb":1423,"rlimit_as_set":true,"clamped":false}}' ;;
      expired)   printf '%s\n' '{"ok":true,"expired":true,"selftest":0,"expire_date":"2026-01-01","mem_gb":2,"caps":{"rlimit_as_mb":1423,"rlimit_as_set":true,"clamped":false}}' ;;
      nocap)     printf '%s\n' '{"ok":true,"expired":false,"selftest":0,"expire_date":"2026-10-26","mem_gb":2,"caps":{"rlimit_as_mb":0,"rlimit_as_set":false,"clamped":false}}' ;;
      clamped)   printf '%s\n' '{"ok":true,"expired":false,"selftest":0,"expire_date":"2026-10-26","mem_gb":2,"caps":{"rlimit_as_mb":1423,"rlimit_as_set":true,"clamped":true}}' ;;
      stale)     printf '%s\n' '{"ok":true,"expired":false,"mem_gb":2}' ;;
      garbage)   printf '%s\n' 'not json at all' ;;
      *)         printf '%s\n' '{"ok":true,"expired":false,"selftest":0,"expire_date":"2026-10-26","mem_gb":2,"cpu":2,"pybin_exists":true,"pymupdf":"1.28.2","script_sha":{},"caps":{"rlimit_as_mb":1423,"rlimit_as_set":true,"clamped":false}}' ;;
    esac
    rm -rf "$TMPD"; exit 0 ;;
  *'"mode":"put"'*)
    if [ "${FPD_FAKE_EXPIRED:-0}" = "1" ]; then printf '%s\n' '[fpdexec] 已到期，拒绝接收文件' >&2; rm -rf "$TMPD"; exit 78; fi
    REL=$(rel_of)
    [ -n "$REL" ] || { printf '%s\n' '[fpdexec] 缺少 rel' >&2; rm -rf "$TMPD"; exit 78; }
    case "$REL" in /*|..*) printf '%s\n' '[fpdexec] rel 必须是相对路径' >&2; rm -rf "$TMPD"; exit 78 ;; esac
    DEST="$WORK/$REL"
    tail -n +2 "$IN" > "$OUT"
    if [ -n "${FPD_FAKE_MAX_BYTES:-}" ]; then
      if [ "$(size_of "$OUT")" -gt "$FPD_FAKE_MAX_BYTES" ]; then
        printf '%s\n' '[fpdexec] payload 超过上限，拒绝落盘' >&2; rm -rf "$TMPD"; exit 78; fi
    fi
    mkdir -p "$(dirname "$DEST")" || { printf '%s\n' '[fpdexec] 建目录失败' >&2; rm -rf "$TMPD"; exit 1; }
    mv "$OUT" "$DEST" || { printf '%s\n' '[fpdexec] 落盘失败' >&2; rm -rf "$TMPD"; exit 1; }
    printf '%s\n' '{"ok": true, "path": "'"$DEST"'", "size": '"$(size_of "$DEST")"', "sha256": "'$(sha_of "$DEST")'"}'
    rm -rf "$TMPD"; exit 0 ;;
  *'"mode":"stat"'*)
    REL=$(rel_of); SRC="$WORK/$REL"
    if [ ! -f "$SRC" ]; then printf '%s\n' '[fpdexec] 文件不存在' >&2; rm -rf "$TMPD"; exit 2; fi
    printf '%s\n' '{"ok": true, "path": "'"$SRC"'", "size": '"$(size_of "$SRC")"', "sha256": "'$(sha_of "$SRC")'"}'
    rm -rf "$TMPD"; exit 0 ;;
  *'"mode":"get"'*)
    REL=$(rel_of); SRC="$WORK/$REL"
    if [ ! -f "$SRC" ]; then printf '%s\n' '[fpdexec] 文件不存在' >&2; rm -rf "$TMPD"; exit 2; fi
    # ★ stdout 只放文件字节（与 fpdexec 的 cmd_get 同口径）；trunc 档模拟"回拉半截"
    if [ "${FPD_FAKE_ARTIFACT:-pdf}" = "trunc" ]; then head -c 4 "$SRC"; else cat "$SRC"; fi
    rm -rf "$TMPD"; exit 0 ;;
  *'"mode":"run"'*)
    if [ "${FPD_FAKE_EXPIRED:-0}" = "1" ]; then printf '%s\n' '[fpdexec] 已到期，拒绝执行' >&2; rm -rf "$TMPD"; exit 78; fi
    OUTS=$(printf '%s' "$HDR" | sed -n 's/.*"outputs":\[\([^]]*\)\].*/\1/p' | tr ',' '\n' | sed 's/[" ]//g')
    case "${FPD_FAKE_ARTIFACT:-pdf}" in
      pdf|trunc)
        for o in $OUTS; do
          d=$(dirname "$o")
          # 会话目录必须由 put 建出来；不存在就是"死腿复活"的形态，照真机一样失败
          if [ ! -d "$d" ]; then printf '%s\n' '[fpdexec] 产物目录不存在，写不出产物' >&2; rm -rf "$TMPD"; exit 1; fi
          printf '%%PDF-1.4\nstub artifact\n%%%%EOF\n' > "$o" || { rm -rf "$TMPD"; exit 1; }
        done ;;
    esac
    printf '%s\n' 'OK: replaced=3 overflow=0 requested=3'
    rm -rf "$TMPD"; exit 0 ;;
  *)
    printf '%s\n' '[fpdexec] 未知 mode' >&2; rm -rf "$TMPD"; exit 78 ;;
esac
`
	scp := `#!/bin/sh
# 假 scp：**任何一次调用都算红**（★ 交接文档第一节要求的就是这条）。
# 真机上 scp 要么挂到墙钟超时、要么静默非零退出，因为它要的远端 scp -t/-f 进程或 sftp 子系统
# 被 ForceCommand 顶掉了。Go 侧现在不该再有 scp 调用点；一旦有人写回来，这里当场失败并留证据，
# 而不是像旧桩那样"桩成成功搬运"。
set -u
VICTIM="${FPD_FAKE_VICTIM:-}"
if [ -n "$VICTIM" ]; then printf 'scp-called %s\n' "$*" >>"$VICTIM"; fi
printf '%s\n' '[fake] scp 在 ForceCommand 下是死路，调用它即判接线退化' >&2
exit 255
`
	for name, body := range map[string]string{"ssh": ssh, "scp": scp} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatalf("写假 %s 失败: %v", name, err)
		}
	}
	t.Setenv("FPD_FAKE_ARTIFACT", artifactMode)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// resetDispatchProbe 清空进程内的 probe 缓存、远端闸与降级计数
// （缓存与闸容量都是**进程级**的，桩换了一批就得跟着换，否则用例互相串味）。
func resetDispatchProbe() {
	probeMu = sync.Mutex{}
	probeRes = nil
	probeErr = nil
	probeAt = time.Time{}
	probeErrAt = time.Time{}
	remoteOnce = sync.Once{}
	remoteGate = nil
	degradedUntil = time.Time{}
	failStreak = 0
}

// enableDispatchForTest 打开派发总闸，并把**远端根**指到本地一个临时目录当替身。
//
// ★ 为什么替身是真的文件系统而不是"桩里 exit 0"：put/stat/get 三条腿的价值全在字节等值比对上，
// 只有让桩真把 payload 落到某个目录、真算 sha256，"传半截＝判失败"这类断言才有牙齿。
// 返回的字符串是给 DispatchArtifactGuard 与"远端路径"断言用的替身根。
func enableDispatchForTest(t *testing.T) string {
	t.Helper()
	remoteRoot := filepath.Join(t.TempDir(), "opt", "fpdispatch")
	work := filepath.Join(remoteRoot, "w")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("造远端 w/ 替身失败: %v", err)
	}
	victim := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(victim, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envDispatch, "1")
	t.Setenv(envDispatchHost, "fpd@127.0.0.1")
	t.Setenv(envDispatchRoot, remoteRoot)
	t.Setenv("FPD_FAKE_WORK", work)
	t.Setenv("FPD_FAKE_HOST", "fpd@127.0.0.1")
	t.Setenv("FPD_FAKE_VICTIM", victim)
	resetDispatchProbe()
	return victim
}

// fakeRemoteFiles 列出替身 w/ 下的条目数（断言"目录确实被 put 建出来了 / 会话目录没留垃圾"）。
func fakeRemoteFiles(t *testing.T, work string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(work, func(p string, fi os.FileInfo, err error) error {
		if err == nil && fi != nil && !fi.IsDir() {
			rel, rerr := filepath.Rel(work, p)
			if rerr == nil {
				out = append(out, rel)
			}
		}
		return nil
	})
	return out
}

// makeFakePDF 造一个"够大且页数够"的假 PDF（只用于分流判定，不用于任何转换）。
// pages=0 时故意不写 /Count ⇒ 用来验证"页数腿"确实会挡住派发。
func makeFakePDF(t *testing.T, dir, name string, mb int, pages int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatalf("造假 PDF 失败: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString("%PDF-1.4\n"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1024)
	for i := range buf {
		buf[i] = 'A'
	}
	for i := 0; i < mb*1024; i++ {
		if _, err := f.Write(buf); err != nil {
			t.Fatal(err)
		}
	}
	if pages > 0 {
		if _, err := f.WriteString("\n/Count " + strconv.Itoa(pages) + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// ---------------- ① 关着时必须逐字节不存在 ----------------

// TestDispatchOffByDefault §0-③：不配 FILEPROC_DISPATCH=1 时，本包对外行为与改造前等价。
// 这条是整批改造的**安全边际**：它一旦红了，说明某个调用点没过总闸就动了路径。
func TestDispatchOffByDefault(t *testing.T) {
	t.Setenv(envDispatch, "")
	t.Setenv(envDispatchHost, "")
	resetDispatchProbe()

	if DispatchEnabled() {
		t.Fatal("派发总闸未配却判为启用")
	}
	if got := DispatchStatus(); got != "off" {
		t.Fatalf("DispatchStatus 期望 off 实际 %q", got)
	}
	// 分流：任何形态都不得放行（连"够大够厚"的 PDF 也不行）
	dir := t.TempDir()
	big := makeFakePDF(t, dir, "big.pdf", 1, 30)
	if DispatchEligible([]string{big}, 30, 0) {
		t.Fatal("派发关闭时 DispatchEligible 竟然放行")
	}
	if out, ok := TryDispatch(context.Background(), "s1", "pdf_overlay.py", nil, nil, nil, nil); ok || out != nil {
		t.Fatalf("派发关闭时 TryDispatch 竟然返回成功: ok=%v", ok)
	}
	// 接线点：overlay 的两条子命令都必须直接走本地
	if _, ok := tryDispatchOverlay(context.Background(), []string{"apply", big, filepath.Join(dir, "o.pdf"), "zh"}, nil); ok {
		t.Fatal("派发关闭时 tryDispatchOverlay 竟然尝试派发")
	}
	if ok := tryDispatchPdfwrite(context.Background(), filepath.Join(dir, "o.pdf"), "/x/y.ttf", nil); ok {
		t.Fatal("派发关闭时 tryDispatchPdfwrite 竟然尝试派发")
	}
}

// ---------------- ② 分流阈值（宁窄勿宽） ----------------

// TestDispatchEligibleGating §5.3：双条件 + 格式白名单。
// ★ 每条都配了"该放行"与"该挡住"两侧，避免把判据写成恒真。
func TestDispatchEligibleGating(t *testing.T) {
	enableDispatchForTest(t)
	t.Setenv(envDispatchMinMB, "1") // 1MB 起步，便于在测试里造出"够大/不够大"
	t.Setenv(envDispatchMinPage, "10")

	dir := t.TempDir()
	bigMany := makeFakePDF(t, dir, "big_many.pdf", 2, 30) // 够大 + 页数够 ⇒ 派
	bigFew := makeFakePDF(t, dir, "big_few.pdf", 2, 3)    // 够大 + 页数不够 ⇒ 不派
	small := makeFakePDF(t, dir, "small.pdf", 0, 30)      // 不够大 ⇒ 不派
	notPDF := filepath.Join(dir, "a.docx")                // 非白名单 ⇒ 不派
	if err := os.WriteFile(notPDF, []byte("PK\x03\x04"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		inputs  []string
		pages   int
		payload int64
		want    bool
	}{
		{"够大且页数够→派", []string{bigMany}, 30, 0, true},
		{"够大但页数不够→不派", []string{bigFew}, 3, 0, false},
		{"体积不够→不派", []string{small}, 30, 0, false},
		{"非PDF格式→不派", []string{notPDF}, 30, 0, false},
		{"无输入且payload够大→派", nil, 0, 2 << 20, true},
		{"无输入且payload小→不派", nil, 0, 1024, false},
	}
	for _, c := range cases {
		if got := DispatchEligible(c.inputs, c.pages, c.payload); got != c.want {
			t.Errorf("%s：DispatchEligible 期望 %v 实际 %v", c.name, c.want, got)
		}
	}
}

// ---------------- ③ 会话号与页数估计 ----------------

// TestSessionIDForSanitizes 会话号会被拼进**远端路径**，字符集必须锁死（../ 与 shell 元字符一律清洗）。
func TestSessionIDForSanitizes(t *testing.T) {
	got := SessionIDFor("../../etc/passwd", "a b;rm -rf /")
	for _, r := range got {
		okc := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
		if !okc {
			t.Fatalf("会话号含非法字符 %q（会被拼进远端路径）: %s", r, got)
		}
	}
	if len(got) != 16 {
		t.Fatalf("会话号长度期望 16 实际 %d（%s）", len(got), got)
	}
	// 正向对照：同一组输入必须稳定同号（同工单多语种共用一个远端目录 ⇒ 输入件只搬一次）
	if a, b := SessionIDFor("x.pdf"), SessionIDFor("x.pdf"); a != b {
		t.Fatalf("同一输入的会话号不稳定：%s != %s", a, b)
	}
}

// TestPdfPageCountFast 页数估计只用于判阈值：尾部 /Count 优先，退化成流式数 /Type /Page。
func TestPdfPageCountFast(t *testing.T) {
	dir := t.TempDir()
	withCount := filepath.Join(dir, "c.pdf")
	if err := os.WriteFile(withCount, []byte("%PDF-1.4\n"+strings.Repeat("x", 512)+"\n/Count 42\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := pdfPageCountFast(withCount); got != 42 {
		t.Fatalf("尾部 /Count 解析期望 42 实际 %d", got)
	}
	noCount := filepath.Join(dir, "n.pdf")
	if err := os.WriteFile(noCount, []byte("%PDF-1.4\n/Type /Page\n/Type /Page\n/Type /Page\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := pdfPageCountFast(noCount); got != 3 {
		t.Fatalf("退化计数期望 3 实际 %d", got)
	}
	// 负向对照：不存在的文件必须回 0（而不是让调用方拿到垃圾页数去判阈值）
	if got := pdfPageCountFast(filepath.Join(dir, "missing.pdf")); got != 0 {
		t.Fatalf("缺失文件期望 0 实际 %d", got)
	}
}

// ---------------- ④ A2：产物落点守卫（到期后没法用） ----------------

// TestDispatchArtifactGuard §9-A2：远端路径必须判红、主站路径必须放行（★ 两侧同测，缺一侧就是恒真锁）。
func TestDispatchArtifactGuard(t *testing.T) {
	t.Setenv(envDispatchRoot, "/opt/fpdispatch")

	// 负向：落在远端根之下 ⇒ 红（这就是"库里记着别人机器上的路径"的形态）
	bad := []string{
		"/opt/fpdispatch/w/abc/out.pdf",
		"/opt/fpdispatch/w/abc",
	}
	for _, p := range bad {
		if err := DispatchArtifactGuard(p); err == nil {
			t.Fatalf("远端路径竟然被放行（到期后必然取不到）: %s", p)
		}
	}
	// 前缀相近但不同根 ⇒ 不得误杀（"/opt/fpdispatch2/..." 不是远端目录）
	if err := DispatchArtifactGuard("/opt/fpdispatch2/w/out.pdf"); err != nil {
		t.Fatalf("邻近路径被误判: %v", err)
	}
	// 正向：主站根之内 ⇒ 绿
	if err := DispatchArtifactGuard("/opt/translator/data/_output/a.pdf", "/opt/translator/data"); err != nil {
		t.Fatalf("主站产物路径被误判: %v", err)
	}
	if err := DispatchArtifactGuard("/opt/translator/data/_uploads/a.pdf", "/opt/translator/data/_output", "/opt/translator/data/_uploads"); err != nil {
		t.Fatalf("主站上传路径被误判: %v", err)
	}
	// 正向对照的反面：给了主站根却落在别处 ⇒ 红（否则"给根"这个参数形同虚设）
	if err := DispatchArtifactGuard("/tmp/leak.pdf", "/opt/translator/data"); err == nil {
		t.Fatal("给了主站根却放行了根外路径")
	}
	// 空路径 ⇒ 红
	if err := DispatchArtifactGuard(""); err == nil {
		t.Fatal("空路径竟然被放行")
	}
}

// ---------------- ⑤ A1：产物没回来必须判失败 ----------------

// TestVerifyDispatchArtifact 产物入门校验：空文件/0 字节不得放行。
func TestVerifyDispatchArtifact(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.pdf")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyDispatchArtifact(empty); err == nil {
		t.Fatal("空产物竟然通过校验（A1 要拦的正是它）")
	}
	pdf := filepath.Join(dir, "ok.pdf")
	if err := os.WriteFile(pdf, []byte("%PDF-1.4\ntrailer\n%%EOF\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyDispatchArtifact(pdf); err != nil {
		t.Fatalf("合法 PDF 被误判: %v", err)
	}
	docx := filepath.Join(dir, "ok.docx")
	if err := os.WriteFile(docx, []byte("PK\x03\x04rest"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyDispatchArtifact(docx); err != nil {
		t.Fatalf("合法 docx(zip) 被误判: %v", err)
	}
}

// TestDispatchRunRequiresArtifactBack ★ A1 的核心：
// "远端说成功了但没回文件"必须判**失败**，绝不能返回 nil + 一个空产物。
// 否则上游会把一个 0 字节的文件当成交付件，症状比失败难查得多。
func TestDispatchRunRequiresArtifactBack(t *testing.T) {
	enableDispatchForTest(t)
	dir := t.TempDir()
	in := makeFakePDF(t, dir, "in.pdf", 1, 20)
	out := filepath.Join(dir, "out.pdf")
	outputs := map[string]string{filepath.Base(out): out}

	// ① 负向：桩的 run 不写产物 ⇒ DispatchRun 必须返回 error，且不留半成品
	//    （★ 现在这条腿先走 stat，所以"远端没做出来"在回拉之前就被抓住，报错也更准）
	fakeBinDir(t, "none")
	if _, err := DispatchRun(context.Background(), "sess1", "pdf_overlay.py",
		[]string{"pdf_overlay.py", "apply", in, out, "zh"}, []byte(`{"translations":{}}`), outputs); err == nil {
		t.Fatal("远端没回产物却返回成功（A1 失守：空文件会被当成产物交付）")
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("失败路径竟然留下了产物文件（半拉文件被上层引用是最难查的一类缺陷）")
	}

	// ② 正向对照：桩真的写出一份带 %PDF 头的产物 ⇒ 必须成功，且主站路径上真有内容
	resetDispatchProbe()
	fakeBinDir(t, "pdf")
	stdout, err := DispatchRun(context.Background(), "sess2", "pdf_overlay.py",
		[]string{"pdf_overlay.py", "apply", in, out, "zh"}, []byte(`{"translations":{}}`), outputs)
	if err != nil {
		t.Fatalf("产物正常回拉却判失败（正向对照红 ⇒ 上面那条负向锁是恒真的）: %v", err)
	}
	if !strings.Contains(string(stdout), "OK:") {
		t.Fatalf("stdout 未透传远端输出: %q", stdout)
	}
	fi, err := os.Stat(out)
	if err != nil {
		t.Fatalf("产物没落到主站路径（§0-① 硬口径失守）: %v", err)
	}
	if fi.Size() == 0 {
		t.Fatal("产物落位但为 0 字节")
	}
}

// TestDispatchRunRejectsRemoteOutputPath 接线层的自伤防护：
// 产物落点只要落在**配置里的远端根**之下，必须在任何一次搬运之前判红，
// 而不是欢快地把它往一个主站不存在的目录里回拉（库里记一条别人机器上的路径＝到期即失效，§9-A2）。
//
// ★ 2026-09-29 两处修正：
//  1. **红因要能锁住**。原先这里写死 "/opt/fpdispatch/..."，而夹具把远端根指到了临时目录当替身，
//     于是那条红是靠"os.Create 失败"这种偶然理由成立的——文件系统形态一变就恒绿。
//     现在路径从 envDispatchRoot 现值推出来，判红只能来自守卫那句话；
//  2. 守卫拦在搬运之前 ⇒ 断言证据文件里一行传输记录都没有。
//
// 正向对照照旧：同一单只把落点换成主站路径就必须跑通，否则上面那条红属于恒真空转。
func TestDispatchRunRejectsRemoteOutputPath(t *testing.T) {
	victim := enableDispatchForTest(t)
	fakeBinDir(t, "pdf")
	remoteRoot := os.Getenv(envDispatchRoot) // 替身根：夹具里它就是"远端根"，判据按现值走而不是写死
	dir := t.TempDir()
	in := makeFakePDF(t, dir, "in.pdf", 1, 20)

	bad := filepath.Join(remoteRoot, "w", "x", "out.pdf")
	_, err := DispatchRun(context.Background(), "sess3", "pdf_overlay.py",
		[]string{"pdf_overlay.py", "apply", in, bad, "zh"},
		nil, map[string]string{"out.pdf": bad})
	if err == nil {
		t.Fatal("产物落点被声明成远端路径却没被判红（A2 在接线层失守）")
	}
	if !strings.Contains(err.Error(), "产物落点不合法") {
		t.Fatalf("判红是因为守卫还是因为别的偶然失败（红因错了这条锁就是假绿）: %v", err)
	}
	if v := readVictim(t, victim); strings.TrimSpace(v) != "" {
		t.Fatalf("产物落点判红前就已经拨过远端传输腿（应当在搬运之前拦住）: %s", v)
	}
	// 判红发生在搬运之前 ⇒ 替身目录里不该多出任何会话目录
	if got := fakeRemoteFiles(t, os.Getenv("FPD_FAKE_WORK")); len(got) != 0 {
		t.Fatalf("守卫拦下了却还是搬了东西过去: %v", got)
	}

	// 正向对照：同一单只把产物落点换成主站路径 ⇒ 必须成功（否则上面那条红是恒真的）
	out := filepath.Join(dir, "out.pdf")
	if _, err := DispatchRun(context.Background(), "sess3b", "pdf_overlay.py",
		[]string{"pdf_overlay.py", "apply", in, out, "zh"}, nil,
		map[string]string{"out.pdf": out}); err != nil {
		t.Fatalf("主站产物落点被判失败（正向对照红 ⇒ 负向锁失效）: %v", err)
	}
}

// ---------------- ⑥ 接线层：关着时不许改路径 ----------------

// TestTryDispatchOverlayKeepsLocalPaths 派发**开启**但这一单不够格（小件）时，
// tryDispatchOverlay 必须返回 false 且不产生任何副作用——小件派发只多付一次公网往返。
func TestTryDispatchOverlayKeepsLocalPaths(t *testing.T) {
	enableDispatchForTest(t)
	t.Setenv(envDispatchMinMB, "1")
	t.Setenv(envDispatchMinPage, "10")
	fakeBinDir(t, "pdf")
	dir := t.TempDir()
	small := makeFakePDF(t, dir, "small.pdf", 0, 2) // 体积不够
	out := filepath.Join(dir, "out.pdf")

	if _, ok := tryDispatchOverlay(context.Background(), []string{"apply", small, out, "zh"}, nil); ok {
		t.Fatal("小件竟然被派出去了（§5.3 宁窄勿宽失守）")
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("未派发却产出了文件")
	}
	// 参数不齐（缺输入路径）也必须直接 false，不得 panic 或误判
	if _, ok := tryDispatchOverlay(context.Background(), []string{"apply"}, nil); ok {
		t.Fatal("参数不齐竟然尝试派发")
	}
}

// ---------------- ⑦ 桩自证：两条死腿必须在桩上就死 ----------------

// readVictim 读桩留下的证据（每一次"死腿复活"都会往这里追加一行）。
func readVictim(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读证据文件失败: %v", err)
	}
	return string(b)
}

// TestFakeStubsThemselvesRejectDeadLegs ★ 交接文档第二节的本体：桩自己必须证明"这两条腿是死的"。
//
//	为什么这条比任何 Go 侧断言都重要：旧桩对 `mkdir` 无条件 exit 0、对 scp 桩成成功搬运，
//	于是"远端目录已存在""文件已搬过去"在单测里**永远成立**，而真机上 ForceCommand 把这两条
//	顶成 fpd-bridge ⇒ 裸 mkdir 实测退 78、scp 挂到墙钟或静默非零。**绿灯证明的是我编的远端**。
//	现在桩只认 stdin 协议，这里直接把改造前的两种调用形状喂给它，要求它们照真机一样红；
//	第三条是正向对照（合规 header 必须退 0 并吐 JSON），否则上面两条红属于"桩恒失败"的空转。
func TestFakeStubsThemselvesRejectDeadLegs(t *testing.T) {
	victim := enableDispatchForTest(t)
	fakeBinDir(t, "pdf")
	host := os.Getenv(envDispatchHost)

	// ① 改造前那条"建远端目录"腿的原样调用 ⇒ 78（真机同码）
	cmd := exec.Command("ssh", host, "mkdir", "-p", dispatchWorkRoot()+"/deadleg")
	if out, err := cmd.CombinedOutput(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 78 {
			t.Fatalf("裸 mkdir 桩的退出码期望 78（真机形态）实际 %v，输出: %s", err, out)
		}
	} else {
		t.Fatal("裸 ssh mkdir 在桩上竟然放行（这就是 09-29 事故里那条恒绿的来源，桩退化即本条必红）")
	}

	// ② scp 腿 ⇒ 255 且留证据（真机上它要么挂到墙钟要么非零退出，从不"安静地成功"）
	scp := exec.Command("scp", inExistingFile(t), host+":/tmp/x")
	if out, err := scp.CombinedOutput(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 255 {
			t.Fatalf("假 scp 期望退出 255 实际 %v，输出: %s", err, out)
		}
	} else {
		t.Fatal("scp 桩放行了（谁把 scp 腿写回来，这里必须当场红）")
	}

	v := readVictim(t, victim)
	if !strings.Contains(v, "ssh-dead-leg") {
		t.Fatalf("裸 mkdir 没在证据里留下死腿记录（桩的判据形同虚设）: %s", v)
	}
	if !strings.Contains(v, "scp-called") {
		t.Fatalf("scp 没在证据里留下记录: %s", v)
	}

	// ③ 正向对照：协议形状的 probe 调用必须退 0 且吐可解析 JSON
	hdr, err := encodeDispatchHeader(map[string]interface{}{"mode": "probe"})
	if err != nil {
		t.Fatal(err)
	}
	p := exec.Command("ssh", host, dispatchPyBin(), dispatchFpdexec)
	p.Stdin = bytes.NewReader(hdr)
	out, err := p.Output()
	if err != nil {
		t.Fatalf("协议形状的调用被桩拒绝（正向对照红 ⇒ 上面两条负向是恒真的空转）: %v", err)
	}
	var got struct {
		OK       bool `json:"ok"`
		Selftest int  `json:"selftest"`
	}
	if e := json.Unmarshal(bytes.TrimSpace(lastBodyLine(out)), &got); e != nil {
		t.Fatalf("桩的 probe 输出不是 JSON: %q", out)
	}
	if !got.OK || got.Selftest != 0 {
		t.Fatalf("默认档 probe 期望 ok/selftest=0，实际 %q", out)
	}
}

// lastBodyLine 取 stdout 的最后一行非空内容（桩与 fpdexec 同口径：JSON 在末行）。
func lastBodyLine(b []byte) []byte {
	lines := strings.Split(string(b), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return []byte(s)
		}
	}
	return nil
}

// inExistingFile 造一个临时存在文件（scp 负向腿的入参，内容无所谓，桩不看）。
func inExistingFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// ---------------- ⑧ 就绪门禁：自检不通过/没上报自检都必须不就绪 ----------------

// TestProbeRejectsFailedSelftest ★ 深判据落地：远端 selftest 非 0 ⇒ 必须判不就绪。
//
//	这条锁射程是"整台机器干不出这活"的形态（缺资产＝2、断言失败＝3）：
//	派发过去只会白付一次公网往返，再把主站那一单回落本地重跑，而工单不会变红——
//	没有这道门禁的话，症状就是"派发看起来是开的，成功率却永远是 0"。
func TestProbeRejectsFailedSelftest(t *testing.T) {
	enableDispatchForTest(t)
	fakeBinDir(t, "pdf")
	dir := t.TempDir()
	in := makeFakePDF(t, dir, "in.pdf", 1, 20)
	out := filepath.Join(dir, "out.pdf")

	for _, mode := range []string{"selftest2", "selftest3"} {
		t.Setenv("FPD_FAKE_PROBE", mode)
		resetDispatchProbe()
		if _, err := dispatchProbe(context.Background()); err == nil {
			t.Fatalf("远端自检 %s 却判就绪（深判据空转）", mode)
		} else if !strings.Contains(err.Error(), "selftest") {
			t.Fatalf("判不就绪的理由里没有 selftest（红因错位）: %v", err)
		}
		if got := DispatchStatus(); got != "degraded" {
			t.Fatalf("自检 %s 时健康面期望 degraded 实际 %q", mode, got)
		}
		// 接线层：这一单必须直接不派（DispatchRun 在探测这一步就返回错误）
		if _, err := DispatchRun(context.Background(), "st_"+mode, "pdf_overlay.py",
			[]string{"pdf_overlay.py", "apply", in, out, "zh"}, nil,
			map[string]string{"out.pdf": out}); err == nil {
			t.Fatalf("自检 %s 竟然还派发了", mode)
		}
		if _, err := os.Stat(out); err == nil {
			t.Fatalf("自检 %s 失败却留下了产物", mode)
		}
	}

	// 正向对照：selftest=0 必须判就绪并真派发成功
	t.Setenv("FPD_FAKE_PROBE", "ok")
	resetDispatchProbe()
	if _, err := dispatchProbe(context.Background()); err != nil {
		t.Fatalf("自检通过的探针被判不就绪（正向对照红 ⇒ 上面那条负向锁恒真）: %v", err)
	}
	if got := DispatchStatus(); got != "online" {
		t.Fatalf("健康面期望 online 实际 %q", got)
	}
	if _, err := DispatchRun(context.Background(), "st_ok", "pdf_overlay.py",
		[]string{"pdf_overlay.py", "apply", in, out, "zh"}, nil,
		map[string]string{"out.pdf": out}); err != nil {
		t.Fatalf("自检通过却派发失败: %v", err)
	}
}

// TestProbeRejectsMissingSelftestField ★ 字段消失式破坏的反向锁：probe 不吐 selftest ⇒ 不就绪。
//
//	这一条专门顶住 09-28 那类空转：当时 Go 侧写的是 `Selftest != 0`，而远端从没吐这个键，
//	json 解出来是零值 ⇒ 判据恒通过，"深判据一次都没执行过"却没人知道。
//	现在字段是 *int，nil 就是 nil。哪天远端把键名改了或删了，这里当场红，而不是静默放行。
func TestProbeRejectsMissingSelftestField(t *testing.T) {
	enableDispatchForTest(t)
	fakeBinDir(t, "pdf")
	t.Setenv("FPD_FAKE_PROBE", "stale") // {"ok":true,"expired":false,"mem_gb":2} —— 没有 selftest
	resetDispatchProbe()

	if _, err := dispatchProbe(context.Background()); err == nil {
		t.Fatal("远端没上报 selftest 却判就绪（零值陷阱复活）")
	} else if !strings.Contains(err.Error(), "selftest") {
		t.Fatalf("报错里没有 selftest 字样，排障时对不上号: %v", err)
	}
	if got := DispatchStatus(); got != "degraded" {
		t.Fatalf("缺字段时健康面期望 degraded 实际 %q", got)
	}
	// 正向对照：同一条腿补上 selftest 键就必须就绪（否则本条是"桩永远失败"的空转）
	t.Setenv("FPD_FAKE_PROBE", "ok")
	resetDispatchProbe()
	if _, err := dispatchProbe(context.Background()); err != nil {
		t.Fatalf("补上 selftest 后仍判不就绪: %v", err)
	}
}

// TestProbeRejectsExpiredAndUnparseable 到期档与垃圾输出都必须不就绪（"到期即拒"是远端读数的第二腿）。
func TestProbeRejectsExpiredAndUnparseable(t *testing.T) {
	enableDispatchForTest(t)
	fakeBinDir(t, "pdf")
	dir := t.TempDir()
	in := makeFakePDF(t, dir, "in.pdf", 1, 20)
	out := filepath.Join(dir, "out.pdf")

	for _, c := range []struct{ mode, wantErrPart string }{
		{"expired", "expired=true"}, // 远端自己报到期
		{"garbage", "不可解析"},         // 输出被别的东西污染（升级提示、shell 欢迎语都会长这样）
		{"nocap", ""},               // 反例：帽没戴上但活能干 ⇒ 仍算就绪，只是健康面要显示 off
	} {
		t.Setenv("FPD_FAKE_PROBE", c.mode)
		resetDispatchProbe()
		_, err := dispatchProbe(context.Background())
		if c.wantErrPart == "" {
			if err != nil {
				t.Fatalf("%s 档不该判不就绪（那是健康面的读数，不是门禁）: %v", c.mode, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.wantErrPart) {
			t.Fatalf("%s 档必须判不就绪且报错含 %q，实际: %v", c.mode, c.wantErrPart, err)
		}
		if _, err := DispatchRun(context.Background(), "exp_"+c.mode, "pdf_overlay.py",
			[]string{"pdf_overlay.py", "apply", in, out, "zh"}, nil,
			map[string]string{"out.pdf": out}); err == nil {
			t.Fatalf("%s 档竟然还派发了", c.mode)
		}
	}
}

// ---------------- ⑨ 健康面：到期日与内存帽看得见，且只出状态词 ----------------

// TestDispatchReadinessStatusWords 四档内存帽＋到期日出栈＋"不泄露远端坐标"的负向锁。
//
// ★ 四档必须分开：clamped（配置高于系统硬上限＝帽没戴上）与 off（env 那一环根本没读到）
// 在现场是两件不同的事，合成一档就查不出该看 fpd.env 还是看 ulimit。
func TestDispatchReadinessStatusWords(t *testing.T) {
	enableDispatchForTest(t)
	fakeBinDir(t, "pdf")

	cases := []struct {
		probe      string
		wantStatus string
		wantExpire string
		wantMem    string
		wantSelf   string
	}{
		{"ok", "online", "2026-10-26", "on", "pass"},
		{"clamped", "online", "2026-10-26", "clamped", "pass"},
		{"nocap", "online", "2026-10-26", "off", "pass"},
		{"selftest2", "degraded", "", "unknown", "unknown"},
		{"stale", "degraded", "", "unknown", "unknown"},
		{"garbage", "degraded", "", "unknown", "unknown"},
	}
	for _, c := range cases {
		t.Setenv("FPD_FAKE_PROBE", c.probe)
		resetDispatchProbe()
		r := DispatchReadinessSnapshot()
		if r.Status != c.wantStatus || r.ExpireDate != c.wantExpire || r.MemCap != c.wantMem || r.Selftest != c.wantSelf {
			t.Fatalf("%s 档健康面读数期望 {status:%s expire:%s mem:%s self:%s} 实际 %+v",
				c.probe, c.wantStatus, c.wantExpire, c.wantMem, c.wantSelf, r)
		}
	}

	// 关着时：状态 off，其余留空（"没配"不等于"配了但坏了"）
	t.Setenv(envDispatch, "")
	resetDispatchProbe()
	if r := DispatchReadinessSnapshot(); r.Status != "off" || r.MemCap != "" || r.ExpireDate != "" || r.Selftest != "" {
		t.Fatalf("派发关闭时读数应当只有 off，实际 %+v", r)
	}
	t.Setenv(envDispatch, "1")

	// ★ 负向锁：出栈的 JSON 里不得出现远端主机名/IP/绝对路径（健康面是公开可达面）
	t.Setenv("FPD_FAKE_PROBE", "ok")
	resetDispatchProbe()
	b, err := json.Marshal(DispatchReadinessSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	for _, forbidden := range []string{os.Getenv(envDispatchHost), os.Getenv(envDispatchRoot), dispatchFpdexec, "w/", ".venv"} {
		if forbidden != "" && strings.Contains(body, forbidden) {
			t.Fatalf("健康面泄露了远端坐标 %q（只许出状态词）: %s", forbidden, body)
		}
	}
	// 正向对照：同一份 JSON 里必须真带着到期日与状态词，否则上面那条负向是"空对象"造成的恒真
	for _, want := range []string{"online", "2026-10-26", "\"dispatch_mem_cap\":\"on\"", "\"dispatch_selftest\":\"pass\""} {
		if !strings.Contains(body, want) {
			t.Fatalf("健康面少了该出现的 %q（负向锁的对照）: %s", want, body)
		}
	}
}

// TestProbeCacheHonorsTTL 就绪缓存按 TTL 重探：静默期内只拨一次，TTL=0 时每次都拨，
// 而**到期当天那个一直在跑的主站进程必须翻旧成 degraded**（旧 sync.Once 语义下它会永远报 online）。
func TestProbeCacheHonorsTTL(t *testing.T) {
	enableDispatchForTest(t)
	fakeBinDir(t, "pdf")
	callLog := filepath.Join(t.TempDir(), "probe_calls.txt")
	t.Setenv("FPD_FAKE_PROBE_LOG", callLog)
	ctx := context.Background()

	// ① 默认 TTL（600s）：连探三次 ⇒ 桩只被拨一次
	if _, err := dispatchProbe(ctx); err != nil {
		t.Fatalf("首次探测失败: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := dispatchProbe(ctx); err != nil {
			t.Fatalf("缓存态探测失败: %v", err)
		}
	}
	if n := countLines(t, callLog); n != 1 {
		t.Fatalf("TTL 内探测次数期望 1 实际 %d（缓存没生效＝每次健康检查都拨一次远端）", n)
	}

	// ② TTL=0：每次都重拨（★ 正向对照，证明桩确实在计数，而不是"探测从没发生"）
	t.Setenv("FILEPROC_DISPATCH_PROBE_TTL_SEC", "0")
	if _, err := dispatchProbe(ctx); err != nil {
		t.Fatalf("TTL=0 探测失败: %v", err)
	}
	if n := countLines(t, callLog); n != 2 {
		t.Fatalf("TTL=0 时探测次数期望 2 实际 %d（桩的计数腿是死的 ⇒ 上面那条 1 次属于恒真）", n)
	}

	// ③ 缓存过期 + 远端改成到期档 ⇒ 必须翻旧成 degraded（不靠人重启进程）
	t.Setenv("FILEPROC_DISPATCH_PROBE_TTL_SEC", "600")
	t.Setenv("FPD_FAKE_PROBE", "expired")
	probeMu.Lock()
	probeAt, probeErrAt = time.Now().Add(-2*time.Hour), time.Now().Add(-2*time.Hour) // 手工把两份缓存都推到过期（真跑要等 10 分钟，测试不等）
	probeMu.Unlock()
	if _, err := dispatchProbe(ctx); err == nil {
		t.Fatal("缓存过期后仍拿旧读数判就绪（到期当天主站会一直报 online，这就是改造前 sync.Once 的形态）")
	}
	if n := countLines(t, callLog); n != 3 {
		t.Fatalf("过期后没有重探远端（探测次数 %d）", n)
	}
	if got := DispatchStatus(); got != "degraded" {
		t.Fatalf("到期重探后健康面期望 degraded 实际 %q", got)
	}
	// ④ 失败短缓存：健康面紧接着再问一次，**不得**又拨一次远端
	//   （没有这一档，远端宕掉时每次 /api/health 都付一次最坏 15s 的 ssh——把故障放大成自家慢接口）
	if n := countLines(t, callLog); n != 3 {
		t.Fatalf("失败后 30s 内仍重复拨远端（探测次数 %d，健康面会被故障放大成慢接口）", n)
	}
}

// countLines 数非空行数（文件不存在＝0 次）。
func countLines(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("读探测计数文件失败: %v", err)
	}
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

// ---------------- ⑩ 传输腿：逐字节等值才是"传成功" ----------------

// TestDispatchRunByteEqualRoundTrip ★ 交接文档第一节要的等值验证：
// 上传与回传两侧都比**字节**，不再拿退出码当判据（09-29 实测 scp 两轮错误码都不一致）。
// 这里同时锁住四件事：
//  1. 输入件真的落在远端 w/ 替身里（会话目录由 put 顺带建出来，不再有裸 mkdir）；
//  2. 主站产物与远端产物**逐字节相等**，不是"非空"；
//  3. 回拉被截成半截 ⇒ 必须判失败，且主站路径上不留任何半成品或 .tmp；
//  4. 全程没有一次死腿调用（证据文件里既无 ssh-dead-leg 也无 scp-called）。
func TestDispatchRunByteEqualRoundTrip(t *testing.T) {
	victim := enableDispatchForTest(t)
	work := os.Getenv("FPD_FAKE_WORK")
	fakeBinDir(t, "pdf")
	dir := t.TempDir()
	in := makeFakePDF(t, dir, "in.pdf", 1, 20)
	out := filepath.Join(dir, "out.pdf")
	outputs := map[string]string{"out.pdf": out}
	ctx := context.Background()

	if _, err := DispatchRun(ctx, "rt1", "pdf_overlay.py",
		[]string{"pdf_overlay.py", "apply", in, out, "zh"}, []byte(`{"translations":{}}`), outputs); err != nil {
		t.Fatalf("正常往返被判失败: %v", err)
	}

	// ①' 远端目录清单**等值**：只许有输入件与产物两件。
	//     ★ 这条锁的是 2026-09-29 抓到的真缺陷：argv[0] 是裸脚本名 pdf_overlay.py，而这份 .py
	//     就在包目录（= 测试进程的 CWD）里，旧的"存在即上传"会把它当输入件搬走，
	//     于是远端 argv[0] 不再是脚本名 ⇒ 派发必败、还被降级链兜成静默死分支。
	//     判据挂在 CWD 上就是这类红/绿的来源，现在只有绝对路径才上传。
	if got, want := strings.Join(fakeRemoteFiles(t, work), ","), "rt1/in_in.pdf,rt1/out_out.pdf"; got != want {
		t.Fatalf("远端会话目录条目期望 [%s] 实际 [%s]（多出 in_pdf_overlay.py＝argv[0] 被当输入件搬走了）", want, got)
	}

	// ① 输入件在远端替身里（文件名 = in_ + 原名，落在会话目录 rt1 之下）
	sessDir := filepath.Join(work, sanitizeSession("rt1"))
	inOnRemote := filepath.Join(sessDir, "in_in.pdf")
	remoteIn, err := os.ReadFile(inOnRemote)
	if err != nil {
		t.Fatalf("输入件没落到远端替身会话目录（put 腿失守或目录没建出来）: %v", err)
	}
	localIn, err := os.ReadFile(in)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(remoteIn, localIn) {
		t.Fatal("上传字节不等值：远端收到的输入件与主站原文不同（哈希比对失守）")
	}

	// ② 产物逐字节相等
	remoteArt, err := os.ReadFile(filepath.Join(sessDir, "out_out.pdf"))
	if err != nil {
		t.Fatalf("远端产物不在会话目录: %v（替身 w/ 下现有条目 %v）", err, fakeRemoteFiles(t, work))
	}
	localArt, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(localArt, remoteArt) {
		t.Fatal("回拉字节不等值：主站产物与远端产物不一致")
	}

	// ③ 没有裸 mkdir / scp 的任何痕迹
	if v := readVictim(t, victim); strings.Contains(v, "dead-leg") || strings.Contains(v, "scp-called") {
		t.Fatalf("派发过程又走了死腿（协议外的通路复活）: %s", v)
	}

	// ④ 负向：远端回传只给前 4 个字节 ⇒ 必须判失败，且不留半截文件与 .tmp
	t.Setenv("FPD_FAKE_ARTIFACT", "trunc")
	if _, err := os.Stat(out); err == nil {
		if err := os.Remove(out); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := DispatchRun(ctx, "rt2", "pdf_overlay.py",
		[]string{"pdf_overlay.py", "apply", in, out, "zh"}, nil, outputs); err == nil {
		t.Fatal("回拉半截产物却判成功（sha 比对没咬住：这类缺陷交付出去就是一个打不开的 PDF）")
	} else if !strings.Contains(err.Error(), "不等值") {
		t.Fatalf("失败原因不是字节不等值，红因错位: %v", err)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("失败路径留下了产物文件")
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, "*.dispatch.tmp"))
	if len(leftovers) != 0 {
		t.Fatalf("失败路径留下了临时文件（半拉文件被上层引用是最难查的一类缺陷）: %v", leftovers)
	}

	// ⑤ 负向：payload 超远端上限 ⇒ 上传当场判失败（不猜远端错误码，只认"这次没成功"）
	t.Setenv("FPD_FAKE_ARTIFACT", "pdf")
	t.Setenv("FPD_FAKE_MAX_BYTES", "4096") // 输入件 1MB ⇒ 必然超限
	if _, err := DispatchRun(ctx, "rt3", "pdf_overlay.py",
		[]string{"pdf_overlay.py", "apply", in, out, "zh"}, nil, outputs); err == nil {
		t.Fatal("超上限的输入件竟然派发成功（远端拒收没被当成失败）")
	} else if !strings.Contains(err.Error(), "输入搬运失败") {
		t.Fatalf("失败点不在上传腿，红因错位: %v", err)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("上传失败却留下了产物")
	}
	// 正向对照：把上限放宽回同样的调用 ⇒ 必须成功（否则 ④⑤ 是恒真锁）
	t.Setenv("FPD_FAKE_MAX_BYTES", "")
	if _, err := DispatchRun(ctx, "rt4", "pdf_overlay.py",
		[]string{"pdf_overlay.py", "apply", in, out, "zh"}, nil, outputs); err != nil {
		t.Fatalf("上限放宽后仍失败（正向对照红 ⇒ 上面的负向锁恒真）: %v", err)
	}
}

// TestDispatchRunWithoutInputUsesMarkerPut ★ 删掉建目录腿之后，"没有输入件"的会话（pdfwrite 降级重建链）
// 仍然必须有目录——它走 ensureDispatchDir 那一次 1 字节 marker put，**仍在同一条协议里**。
//
// 桩的 run 分支在产物目录不存在时**照真机一样失败**，所以这条用例同时证明了两件事：
// marker put 真把目录建出来了，而且这条腿一旦被人删掉/写回裸 mkdir，这里当场红。
func TestDispatchRunWithoutInputUsesMarkerPut(t *testing.T) {
	victim := enableDispatchForTest(t)
	work := os.Getenv("FPD_FAKE_WORK")
	fakeBinDir(t, "pdf")
	dir := t.TempDir()
	out := filepath.Join(dir, "out.pdf")
	ctx := context.Background()

	if _, err := DispatchRun(ctx, "nowrap", "pdfwrite.py",
		[]string{"pdfwrite.py", "convert", "-", out, "zh"},
		[]byte(`{"text":"hello"}`), map[string]string{"out.pdf": out}); err != nil {
		t.Fatalf("无输入件的会话派发失败（marker put 没把目录建出来）: %v", err)
	}
	sess := filepath.Join(work, sanitizeSession("nowrap"))
	// ★ 等值锁：这一单的远端目录只许有 marker 与产物两件——
	//   多出 in_pdfwrite.py 就说明 argv[0] 又被 CWD 命中当输入件搬走了（见上一用例的同名锁）。
	if got, want := strings.Join(fakeRemoteFiles(t, work), ","), "nowrap/.keep,nowrap/out_out.pdf"; got != want {
		t.Fatalf("无输入件会话的远端条目期望 [%s] 实际 [%s]", want, got)
	}
	if _, err := os.Stat(filepath.Join(sess, ".keep")); err != nil {
		t.Fatalf("会话目录里没有 marker 文件，说明目录不是按协议建出来的: %v", err)
	}
	if v := readVictim(t, victim); strings.Contains(v, "dead-leg") || strings.Contains(v, "scp-called") {
		t.Fatalf("无输入件的会话走了死腿: %s", v)
	}
	// 逐字节对照：主站产物 == 远端产物（marker put 这条腿不能把等值性弄丢）
	remoteArt, err := os.ReadFile(filepath.Join(sess, "out_out.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	localArt, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(localArt, remoteArt) {
		t.Fatal("marker put 会话的产物回拉字节不等值")
	}
}
