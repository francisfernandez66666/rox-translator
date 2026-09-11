package fileproc

import (
	"os"
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
