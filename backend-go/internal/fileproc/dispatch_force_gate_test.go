// ============ dispatch_force_gate_test.go · 派发强开 env 的射程门禁 ============
// 2026-11-26 工单动态派发批：FILEPROC_DISPATCH_FORCE 是 fpdprobe 的**探针专用**开关，
// 只许出现在 cmd/fpdprobe/main.go 的探测报告里；生产决策链（engine/*.go、fileproc/*.go）
// 零命中——否则线上会多出一个绕过正常开闸流程的隐藏入口。
//
// 本文件用 filepath.Walk + strings.Contains 做源码扫描（与 Grep 工具同口径，不引新依赖）。
// 扫描器必须排除 _test.go：本文件自己就含那个 env 字面量，扫进去会把自己判红（自伤）。
//
// 反证腿：往临时假文件写该 env，扫描器必须抓到——证明"零命中"不是扫不到东西的假绿。
// ============================================
package fileproc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forceEnvLiteral 探针专用强开开关的字面量（探针侧唯一允许出现的位置）。
const forceEnvLiteral = "FILEPROC_DISPATCH_FORCE"

// forceReadFile 读**相对本包目录**（backend-go/internal/fileproc）的源码。
// 本包在 internal/fileproc，仓库根是 ../../../；写错路径报 ENOENT 而不是假绿。
func forceReadFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("读 %s 失败（判据按相对本包目录的字面路径走）：%v", rel, err)
	}
	return string(b)
}

// forceEnvHitsInDir 扫目录下所有生产 .go（排除 _test.go），返回含该 env 字面量的文件列表。
// 排除测试文件是必须的：门禁测试自己要写出这个字面量才能断言它缺席，扫进去就是自伤。
func forceEnvHitsInDir(dir string) ([]string, error) {
	var hits []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), forceEnvLiteral) {
			hits = append(hits, path)
		}
		return nil
	})
	return hits, err
}

// TestDispatchForceEnvScopedToProbe 钉住强开 env 只在探针、生产决策链零命中。
func TestDispatchForceEnvScopedToProbe(t *testing.T) {
	// ① 正向对照：探针源码必须含该 env（否则下面两条"零命中"可能只是扫错了地方）。
	probe := forceReadFile(t, "../../cmd/fpdprobe/main.go")
	if !strings.Contains(probe, forceEnvLiteral) {
		t.Fatal("cmd/fpdprobe/main.go 里没有 FILEPROC_DISPATCH_FORCE ⇒ 探测器射程失效（正对照认不出）")
	}

	// ② 负向：生产决策链两处目录零命中。
	for _, dir := range []string{"../engine", "."} {
		hits, err := forceEnvHitsInDir(dir)
		if err != nil {
			t.Fatalf("扫描 %s 失败（射程失效，不算绿）：%v", dir, err)
		}
		if len(hits) != 0 {
			t.Errorf("%s 的生产源码出现 %s（%s）⇒ 线上多出一个绕过开闸流程的隐藏入口",
				dir, forceEnvLiteral, strings.Join(hits, ", "))
		}
	}

	// ③ 反证：临时假文件写该 env，扫描器必须抓到（证明 ② 的零命中不是空转）。
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "fake_prod.go")
	if err := os.WriteFile(fake, []byte("package x\nfunc f(){ os.Getenv(\""+forceEnvLiteral+"\") }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	hits, err := forceEnvHitsInDir(tmp)
	if err != nil {
		t.Fatalf("反证扫描失败：%v", err)
	}
	if len(hits) == 0 {
		t.Error("反证落空：假生产源码里写了强开 env 却没被抓到 ⇒ ② 的零命中是假绿")
	}

	// ④ 对照：_test.go 必须被排除（否则本文件自己就会把 ② 判红，下一位会误删门禁）。
	// 用临时目录模拟：同目录放一个生产文件（干净）与一个测试文件（含 env），应只命中测试文件之外的零。
	tmp2 := t.TempDir()
	os.WriteFile(filepath.Join(tmp2, "prod.go"), []byte("package x\n// clean\n"), 0644)
	os.WriteFile(filepath.Join(tmp2, "probe_test.go"), []byte("package x\nconst s=\""+forceEnvLiteral+"\"\n"), 0644)
	hits2, err := forceEnvHitsInDir(tmp2)
	if err != nil {
		t.Fatalf("对照组扫描失败：%v", err)
	}
	if len(hits2) != 0 {
		t.Errorf("_test.go 没被排除，命中了 %s ⇒ 门禁会在自己的测试文件上自伤", strings.Join(hits2, ", "))
	}
}
