// ============ dispatch_slice_caps_test.go · fpd.slice 资源帽门禁 ============
// 2026-11-26 工单动态派发批（D-1/D-8）：广州机与量化服务共盘，派发执行体必须戴死资源帽：
//   systemd slice：CPUQuota=30% / MemoryHigh=512M / MemoryMax=768M（覆盖 bootstrap 的自适应大帽）
//   w/ 目录：FPD_WORKDIR_MAX_MB 总配额，fpdexec.cmd_put 落盘前必须过 _check_workdir_quota
//
// 本文件用源码形态钉住 gz-install.sh 的 sed 覆盖与 fpdexec.py 的落盘前检查。
// 反证腿：假源码删掉 CPUQuota 行必须判红。
// ============================================
package fileproc

import (
	"os"
	"strings"
	"testing"
)

// sliceCapsReadFile 读相对本包目录的源码；读不到判红。
func sliceCapsReadFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("读 %s 失败（判据按相对本包目录的字面路径走）：%v", rel, err)
	}
	return string(b)
}

// sliceCapsOK 判 gz-install.sh 是否同时具备四件帽参数与 fpd.slice 模板覆盖 sed。
func sliceCapsOK(src string) bool {
	for _, want := range []string{
		"CPUQuota=30%",
		"MemoryHigh=512M",
		"MemoryMax=768M",
		"FPD_WORKDIR_MAX_MB",
	} {
		if !strings.Contains(src, want) {
			return false
		}
	}
	// fpd.slice 模板必须被 gz-install 覆盖，而不是只在 bootstrap 里按总内存算大帽。
	for _, want := range []string{
		"/etc/systemd/system/fpd.slice",
		"sed -i.bak",
		`s/^MemoryHigh=.*/MemoryHigh=512M/`,
		`s/^MemoryMax=.*/MemoryMax=768M/`,
		`i CPUQuota=30%`,
	} {
		if !strings.Contains(src, want) {
			return false
		}
	}
	return true
}

// sliceCapsDropLines 删掉含指定片段的整行（构造反证夹具用）。
func sliceCapsDropLines(src, frag string) string {
	out := make([]string, 0)
	dropped := 0
	for _, ln := range strings.Split(src, "\n") {
		if strings.Contains(ln, frag) {
			dropped++
			continue
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// TestDispatchSliceCapsEnforced 钉住 slice 资源帽、模板覆盖与落盘前配额检查。
func TestDispatchSliceCapsEnforced(t *testing.T) {
	// ① gz-install.sh 四件帽参数 + fpd.slice sed 覆盖。
	sh := sliceCapsReadFile(t, "../../../deploy/dispatch/gz-install.sh")
	if !sliceCapsOK(sh) {
		t.Error("gz-install.sh 没通过资源帽门禁（CPUQuota/MemoryHigh/MemoryMax/FPD_WORKDIR_MAX_MB 或 sed 覆盖有缺项）")
	}

	// ② 反证：删掉 CPUQuota 行必须判红（log 行写的是 `CPUQuota 30%`，不含等号，不会被误当成实现）。
	fake := sliceCapsDropLines(sh, "CPUQuota=30%")
	if fake == sh {
		t.Fatal("反证构造失败：gz-install.sh 里没有 `CPUQuota=30%` 行（锚点漂了，① 已扫不到东西）")
	}
	if sliceCapsOK(fake) {
		t.Error("反证落空：删掉 CPUQuota 行仍判通过 ⇒ ① 抓不住资源帽回退")
	}

	// ③ fpdexec.py：_check_workdir_quota 存在且 cmd_put 落盘前真调用它。
	py := sliceCapsReadFile(t, "../../../deploy/dispatch/fpdexec.py")
	if !strings.Contains(py, "def _check_workdir_quota(") {
		t.Error("fpdexec.py 没有 _check_workdir_quota ⇒ w/ 总配额无人执行")
	}
	putBody := chunk4PyFuncBody(py, "def cmd_put(")
	if len(putBody) < 100 {
		t.Fatalf("切 cmd_put 函数体失败（%d 字节），判据失明", len(putBody))
	}
	if !strings.Contains(putBody, "_check_workdir_quota(") {
		t.Error("cmd_put 没有调用 _check_workdir_quota ⇒ 落盘前不查 w/ 配额，共盘可能被写满")
	}

	// ④ 反证 2：把调用从 cmd_put 体里删掉，判据必须判红。
	noCall := strings.Replace(putBody, "_check_workdir_quota(", "_no_quota_check(", 1)
	if noCall == putBody {
		t.Fatal("反证 2 构造失败：cmd_put 体里没有 `_check_workdir_quota(` 锚点")
	}
	if strings.Contains(noCall, "_check_workdir_quota(") {
		t.Error("反证 2 落空：删掉调用后仍读到它")
	}
}
