// ============ dispatch_remote_key_gate_test.go · 广州侧零 Key 落盘门禁 ============
// 2026-11-26 工单动态派发批：广州执行机是零密钥执行体——LLM Key 随 payload 临时传入进程，
// DB_DSN / JWT_SECRET 等主站凭据更不许出现在远端任何脚本与环境文件里。
//
// 本文件扫 deploy/dispatch 下的 *.py / *.sh / fpd-bridge / *.env，只认**行首赋值形态**
// `^\s*(KEY)\s*=`；说明文档（README.md 等）、注释里提到 Key 名字都不算（否则注释会把锁自伤）。
// 另钉两处：bootstrap 写 fpd.env 的那段 heredoc 无 Key 赋值、expiry 脚本无 Key 行。
//
// 反证腿：临时假文件加一行 `SILICONFLOW_API_KEY=sk-x`，扫描器必须抓到。
// ============================================
package fileproc

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// remoteKeyAssignRe 只认行首赋值形态。注释（# 开头）、if grep、echo 里的 Key 名字都不命中。
var remoteKeyAssignRe = regexp.MustCompile(`^\s*(SILICONFLOW_API_KEY|DB_DSN|JWT_SECRET|ADMIN_TOKEN|METRICS_TOKEN|FPD_LLM_KEY)\s*=`)

// remoteKeyReadFile 读相对本包目录的源码；读不到判红。
func remoteKeyReadFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("读 %s 失败（判据按相对本包目录的字面路径走）：%v", rel, err)
	}
	return string(b)
}

// remoteKeyHits 返回源码里所有"行首 Key 赋值"的行（去空白）。
func remoteKeyHits(src string) []string {
	var hits []string
	for _, ln := range strings.Split(src, "\n") {
		if remoteKeyAssignRe.MatchString(ln) {
			hits = append(hits, strings.TrimSpace(ln))
		}
	}
	return hits
}

// remoteKeyIsScanned 判一个文件是否属于扫描面：脚本、Python、环境文件、无扩展名的 bridge。
// README.md 等说明文档不在面内（只提 Key 名字、不落任何赋值）。
func remoteKeyIsScanned(name string) bool {
	ext := filepath.Ext(name)
	return ext == ".py" || ext == ".sh" || ext == ".env" || name == "fpd-bridge"
}

// bootstrapEnvBlock 从 bootstrap-dispatch.sh 里切出"写 fpd.env"那段 heredoc。
// 切不到返回空串，调用方据此判红，不能当"干净"用。
func bootstrapEnvBlock(src string) string {
	end := strings.Index(src, `} > "$FPD_ROOT/etc/fpd.env"`)
	if end < 0 {
		return ""
	}
	start := strings.LastIndex(src[:end], "\n{\n")
	if start < 0 {
		return ""
	}
	return src[start+3 : end]
}

// TestDispatchRemoteKeysNeverOnDisk 钉住广州侧零 Key 落盘：全扫描面 + bootstrap + expiry。
func TestDispatchRemoteKeysNeverOnDisk(t *testing.T) {
	// ① 扫描面零命中：deploy/dispatch 下脚本/环境文件/bridge。
	dir := "../../../deploy/dispatch"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读扫描目录 %s 失败（射程失效）：%v", dir, err)
	}
	scanned := 0
	for _, e := range entries {
		if e.IsDir() || !remoteKeyIsScanned(e.Name()) {
			continue
		}
		scanned++
		src := remoteKeyReadFile(t, filepath.Join(dir, e.Name()))
		if hits := remoteKeyHits(src); len(hits) != 0 {
			t.Errorf("%s 里出现 Key 赋值（%s）⇒ 广州侧零密钥被破坏", e.Name(), strings.Join(hits, "; "))
		}
	}
	if scanned < 5 {
		t.Fatalf("扫描面只有 %d 个文件 ⇒ 射程可疑（应至少含 chunk_translate.py/fpdexec.py/*.sh/fpd-bridge 等）", scanned)
	}

	// ② fpd.env 由 bootstrap 写入的那段 heredoc 无 Key 赋值。
	bootstrap := remoteKeyReadFile(t, "../../../deploy/dispatch/bootstrap-dispatch.sh")
	envBlock := bootstrapEnvBlock(bootstrap)
	if envBlock == "" {
		t.Fatal("切不到 bootstrap 写 fpd.env 的 heredoc ⇒ ② 判据失明")
	}
	if hits := remoteKeyHits(envBlock); len(hits) != 0 {
		t.Errorf("bootstrap 写 fpd.env 时带了 Key 赋值（%s）⇒ 远端一装机就落盘密钥", strings.Join(hits, "; "))
	}

	// ③ expiry 脚本无 Key 行。
	expiry := remoteKeyReadFile(t, "../../../deploy/dispatch/fpd-expiry.sh")
	if hits := remoteKeyHits(expiry); len(hits) != 0 {
		t.Errorf("fpd-expiry.sh 里出现 Key 赋值（%s）", strings.Join(hits, "; "))
	}

	// ④ 反证：临时假文件加一行 Key 赋值，扫描器必须抓到。
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "fake.env")
	if err := os.WriteFile(fake, []byte("# 注释里提到 SILICONFLOW_API_KEY 不算\nSILICONFLOW_API_KEY=sk-x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	hits := remoteKeyHits(remoteKeyReadFile(t, fake))
	if len(hits) == 0 || !strings.Contains(hits[0], "sk-x") {
		t.Error("反证落空：`SILICONFLOW_API_KEY=sk-x` 没被抓到 ⇒ ① 的零命中是假绿")
	}
	// 对照：只有注释提到 Key 名字不许判红（否则锁会被 README 类说明自伤）。
	if hits := remoteKeyHits("# 禁止 DB_DSN / JWT_SECRET / SILICONFLOW_API_KEY\n"); len(hits) != 0 {
		t.Errorf("注释里的 Key 名字被误判成赋值（%s）⇒ 下一位会摘掉这条锁", strings.Join(hits, "; "))
	}
}
