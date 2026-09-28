package fileproc

import (
	"context"
	"os"
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
// 手法：用**假 ssh/scp 桩**（临时目录里两个可执行脚本，插到 PATH 最前面）真跑 DispatchRun，
// 不拨任何真实主机。桩按 §4 的 base64 header 协议分流 probe/run，与线上走同一条代码路径。
//
// ★ 三条纪律（改本文件前先看）：
//  1. 不用 t.Skip：缺 python3/缺桩能力一律 t.Fatal（本仓多次把红灯洗成绿的教训）；
//  2. 每个用例跑前重置 probeOnce/probeRes（进程内 probe 只成功一次，不重置会串味）；
//  3. 负向锁必须配正向对照（AGENTS §一·5）：判"远端路径要红"就得同时判"主站路径要绿"。
// ================================================================

// ---------------- 桩与夹具 ----------------

// fakeBinDir 造一个只含 ssh/scp 两个假命令的目录，返回该目录（调用方插到 PATH 最前）。
//
// getMode 控制假 scp 的"回拉"行为：
//
//	"pdf"  → 真的写一份带 %PDF 头的产物（正向对照）
//	"none" → 什么都不写（远端没回产物，A1 要判红的形态）
func fakeBinDir(t *testing.T, getMode string) string {
	t.Helper()
	dir := t.TempDir()

	ssh := `#!/bin/sh
# 假 ssh：只认 §4 的协议——stdin 第一行是 base64(header)，其余是 payload。
# 形如 "... fpdexec.py" 的调用（dispatchSSH）走这里；"mkdir -p ..."（dispatchMkdir）直接放行。
set -u
LINE=$(head -n 1)
HDR=""
if command -v base64 >/dev/null 2>&1; then
  HDR=$(printf '%s' "$LINE" | base64 -d 2>/dev/null || printf '%s' "$LINE" | base64 -D 2>/dev/null || echo "")
fi
case "$*" in
  *mkdir*) exit 0 ;;
esac
case "$HDR" in
  *'"mode":"probe"'*)
    printf '%s\n' '{"ok":true,"expired":false,"selftest":0,"mem_gb":2,"cpu":2,"script_sha":{},"libs":{},"fonts":{"zh_families":[],"fallback_ttf":true}}'
    exit 0 ;;
  *)
    printf '%s\n' 'OK: replaced=3 overflow=0 requested=3'
    exit 0 ;;
esac
`
	scp := `#!/bin/sh
# 假 scp：区分方向靠"含冒号的那一半"。
#   上传（put）：本地→远端，什么都不做（远端文件系统本就不在这个桩的能力范围）
#   回拉（get）：远端→本地，按 FPD_FAKE_GET 决定是否写出产物
set -u
LAST=""
for a in "$@"; do case "$a" in -*) ;; *) LAST="$a" ;; esac; done
case "$LAST" in
  *:*) exit 0 ;;   # put：目标是 host:path
esac
if [ "${FPD_FAKE_GET:-none}" = "pdf" ]; then
  printf '%%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer\n%%%%EOF\n' >"$LAST"
fi
exit 0
`
	for name, body := range map[string]string{"ssh": ssh, "scp": scp} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatalf("写假 %s 失败: %v", name, err)
		}
	}
	t.Setenv("FPD_FAKE_GET", getMode)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// resetDispatchProbe 清空进程内的 probe 缓存与降级计数（桩换了一批就得跟着换，否则用例互相串味）。
func resetDispatchProbe() {
	probeOnce = sync.Once{}
	probeRes = nil
	probeErr = nil
	degradedUntil = time.Time{}
	failStreak = 0
}

// enableDispatchForTest 打开派发总闸（并把远端根指向一个测试专用路径）。
func enableDispatchForTest(t *testing.T) {
	t.Helper()
	t.Setenv(envDispatch, "1")
	t.Setenv(envDispatchHost, "fpd@127.0.0.1")
	t.Setenv(envDispatchRoot, "/opt/fpdispatch")
	resetDispatchProbe()
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

	// ① 负向：假 scp 不写产物 ⇒ DispatchRun 必须返回 error，且不留半成品
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
// 若调用方误把**远端路径**声明成主站产物落点，必须在搬运之前就判红，
// 而不是欢快地把它 scp 到一个不存在的本地目录。
func TestDispatchRunRejectsRemoteOutputPath(t *testing.T) {
	enableDispatchForTest(t)
	fakeBinDir(t, "pdf")
	dir := t.TempDir()
	in := makeFakePDF(t, dir, "in.pdf", 1, 20)
	_, err := DispatchRun(context.Background(), "sess3", "pdf_overlay.py",
		[]string{"pdf_overlay.py", "apply", in, "/opt/fpdispatch/w/x/out.pdf", "zh"},
		nil, map[string]string{"out.pdf": "/opt/fpdispatch/w/x/out.pdf"})
	if err == nil {
		t.Fatal("产物落点被声明成远端路径却没被判红（A2 在接线层失守）")
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
