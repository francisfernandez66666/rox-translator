package fileproc

import (
	"os"
	"strings"
	"testing"
)

// TestCheckHealth 验证 CheckHealth 执行不 panic，并返回检查结果
func TestCheckHealth(t *testing.T) {
	// CheckHealth 应该在任何环境下都能执行不 panic
	// 即使某些依赖不存在，也不应该崩溃
	result := CheckHealth()
	if result == nil {
		t.Error("CheckHealth returned nil")
	}
	if result.Warnings == nil {
		t.Error("CheckHealth.Warnings is nil")
	}
}

// TestCheckPythonModuleDiscriminates ★ 2026-09-28 D5 教训的守卫：
// checkPythonModule 是**所有 Python 依赖探测的唯一通道**，它必须真的能区分"有/没有"，
// 否则健康检查就是一张绿纸（历史上 `checkPythonModule(py,"fpdf2")` 因写错导入名恒为 false，
// 打了很久的假告警；而主链依赖 pymupdf 干脆没进探测清单，机器「健康」但 PDF 主链必崩）。
// 这里配一组「必然存在 / 必然不存在」的双向对照，任何一侧失真都立刻红。
func TestCheckPythonModuleDiscriminates(t *testing.T) {
	py := findPython()
	if py == "" {
		t.Fatal("findPython() 返回空：本机无 python3。本用例不许 skip——" +
			"依赖探测若在无 Python 的机器上失去验证，就等于承认『健康检查可以是空转』")
	}
	if !checkPythonModule(py, "json") {
		t.Fatal("标准库 json 竟然探测失败 ⇒ checkPythonModule 的退出码判读写反了，所有依赖字段都不可信")
	}
	if checkPythonModule(py, "definitely_not_a_module_zzz") {
		t.Fatal("不存在的模块竟然探测成功 ⇒ checkPythonModule 恒真，健康检查会退化成全绿空转")
	}
	// fpdf2 的导入名是 fpdf（pip 包名 ≠ 导入名）；这个不等式一旦反转，说明又开始误用包名当导入名。
	if checkPythonModule(py, "fpdf2") {
		t.Fatal("存在名为 fpdf2 的可导入模块 ⇒ 与 fpdf2 的发布事实不符，healthcheck.go 的订正被回退或环境被污染")
	}
}

// TestPymupdfInHealthResult ★ D5 直接补丁：主链依赖必须进 HealthResult，
// 不许再出现「健康检查全绿但 pdf_overlay apply 一进去就崩」的形态。
// 断言两条：① 字段值必须等于本机真实探测结果（不是写死的常量）；② 不可用时必须有对应告警。
func TestPymupdfInHealthResult(t *testing.T) {
	py := findPython()
	if py == "" {
		t.Fatal("findPython() 返回空：本机无 python3。本用例不许 skip——" +
			"依赖探测若在无解释器的机器上失去验证，就等于承认『健康检查可以空转』")
	}
	real := checkPythonModule(py, "pymupdf")
	res := CheckHealth()
	if res.PymupdfAvailable != real {
		t.Fatalf("HealthResult.PymupdfAvailable=%v 与本机实探测=%v 不一致 ⇒ 字段不是照实填的，健康检查失去意义",
			res.PymupdfAvailable, real)
	}
	if !real {
		hasWarn := false
		for _, w := range res.Warnings {
			if strings.Contains(w, "pymupdf") {
				hasWarn = true
			}
		}
		if !hasWarn {
			t.Fatal("pymupdf 不可用却没有对应告警 ⇒ 这条会变成谁也看不见的沉默字段（D5 的形态）")
		}
	}
}

// TestFindPython 验证 Python 查找逻辑
func TestFindPython(t *testing.T) {
	// 在大多数开发环境中 python3 应该存在
	pythonPath := findPython()
	if pythonPath == "" {
		t.Log("Python not found (expected in some environments)")
	} else {
		t.Logf("Python found at: %s", pythonPath)
	}
}

// TestFindLibreOffice 验证 LibreOffice 查找逻辑
func TestFindLibreOffice(t *testing.T) {
	// LibreOffice 可能不存在于开发环境
	loPath := findLibreOffice()
	if loPath == "" {
		t.Log("LibreOffice not found (expected in some environments)")
	} else {
		t.Logf("LibreOffice found at: %s", loPath)
	}
}

// TestCheckPythonModule 验证 Python 模块检查逻辑
func TestCheckPythonModule(t *testing.T) {
	pythonPath := findPython()
	if pythonPath == "" {
		t.Skip("Python not found, skipping module check test")
	}

	// 测试存在的模块（os 是内置模块）
	if !checkPythonModule(pythonPath, "os") {
		t.Error("checkPythonModule should return true for 'os' module")
	}

	// 测试不存在的模块
	if checkPythonModule(pythonPath, "nonexistent_module_xyz_12345") {
		t.Error("checkPythonModule should return false for nonexistent module")
	}
}

// TestGetPythonPath 验证 GetPythonPath 带缓存的查找
func TestGetPythonPath(t *testing.T) {
	// 第一次调用
	path1 := GetPythonPath()
	// 第二次调用应该返回相同结果（缓存）
	path2 := GetPythonPath()

	if path1 != path2 {
		t.Errorf("GetPythonPath: inconsistent results: %s vs %s", path1, path2)
	}
}

// TestGetLibreOfficePath 验证 GetLibreOfficePath 带缓存的查找
func TestGetLibreOfficePath(t *testing.T) {
	path1 := GetLibreOfficePath()
	path2 := GetLibreOfficePath()

	if path1 != path2 {
		t.Errorf("GetLibreOfficePath: inconsistent results: %s vs %s", path1, path2)
	}
}

// TestIsHealthy 验证 IsHealthy 返回正确的健康状态
func TestIsHealthy(t *testing.T) {
	// IsHealthy 不应该 panic
	healthy := IsHealthy()
	t.Logf("IsHealthy: %v", healthy)
}

// TestHealthResultStructure 验证 HealthResult 结构体字段
func TestHealthResultStructure(t *testing.T) {
	result := &HealthResult{
		Warnings: make([]string, 0),
	}

	// 验证初始状态
	if result.PythonAvailable {
		t.Error("New HealthResult should have PythonAvailable=false")
	}
	if result.LibreOfficeAvail {
		t.Error("New HealthResult should have LibreOfficeAvail=false")
	}
	if len(result.Warnings) != 0 {
		t.Error("New HealthResult should have empty Warnings")
	}
}

// TestCheckHealthWithEnvVar 验证环境变量覆盖 Python 路径
func TestCheckHealthWithEnvVar(t *testing.T) {
	// 设置环境变量为不存在的路径
	original := os.Getenv("FILEPROC_PYTHON_BIN")
	defer os.Setenv("FILEPROC_PYTHON_BIN", original)

	os.Setenv("FILEPROC_PYTHON_BIN", "/nonexistent/python")
	// 由于 sync.Once，这个测试不会重新执行健康检查
	// 但可以验证环境变量读取逻辑
	pythonPath := findPython()
	t.Logf("findPython with env var: %s", pythonPath)
}
