package knowledge

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// KN13 禁词（生产源码里不许出现：调 LLM、定义翻译腿接口、依赖 engine 包）。
// 注意：本测试文件自身含这些模式串作为"判据"，故扫描排除 _test.go（不让判据扫自己）。
var knForbidden = []string{"llm.", "FactTranslator", "engine."}

func knSourceHasForbidden(src string) bool {
	for _, p := range knForbidden {
		if strings.Contains(src, p) {
			return true
		}
	}
	return false
}

func knRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(wd, "contract")); err == nil {
			return wd
		}
		wd = filepath.Dir(wd)
	}
	t.Fatalf("KN13 找不到 internal/knowledge 根目录（闸门失明）")
	return ""
}

// KN13 知识服务零模型调用／零跨域 import。
//   - 源码扫描：现算 internal/knowledge/** 的生产 .go 全集，扫描面为空报「闸门失明」；
//   - import 图：用 `go list -deps` 现算（不按文件名字符串扫，别名/引号写法也漏不掉）；
//   - 反证判别力：注入一个带 llm.Client 的变异源码串，扫描器必须命中。
func TestKN13Isolation(t *testing.T) {
	root := knRoot(t)
	scanned := 0
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		scanned++
		if knSourceHasForbidden(string(b)) {
			t.Errorf("KN13 生产源码 %s 出现禁词（加一个 llm.Client 字段 ⇒ M0317 在此红）", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned == 0 {
		t.Fatalf("KN13 闸门失明：扫到 0 个生产 .go 文件")
	}
	if !knSourceHasForbidden("type S struct{ c llm.Client }") {
		t.Fatalf("KN13 判据失明：注入 llm.Client 却没抓到")
	}

	// import 图现算：精确匹配主库 store / 主服务 api 两个跨域包路径。
	out, err := exec.Command("go", "list", "-deps", "translator/internal/knowledge/...").Output()
	if err != nil {
		t.Fatalf("KN13 go list -deps 失败: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		dep := strings.TrimSpace(line)
		if dep == "translator/internal/store" || dep == "translator/internal/api" {
			t.Fatalf("KN13 import 图出现跨域包 %s（知识侧不许依赖主库 store / 主服务 api）", dep)
		}
	}
}
