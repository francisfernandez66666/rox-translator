// ============ healthcheck.go · 职责说明 ============
// fileproc 包依赖健康检查。
// 启动时检查 Python 解释器、fpdf2/pdf2docx 库、LibreOffice 是否可用。
// 不满足时记录告警日志，不影响主进程启动（运行时 fallback 到 Go 纯实现）。
package fileproc

import (
	"log"
	"os"
	"os/exec"
	"sync"
)

var (
	healthCheckOnce sync.Once
	healthResult    *HealthResult
)

// HealthResult 依赖健康检查结果
type HealthResult struct {
	PythonAvailable   bool   // Python 解释器是否可用
	Fpdf2Available    bool   // fpdf2 库是否可导入
	Pdf2docxAvaliable bool   // pdf2docx 库是否可导入
	LibreOfficeAvail  bool   // LibreOffice 是否可用
	PythonPath        string // Python 解释器路径
	LibreOfficePath   string // LibreOffice 路径
	Warnings          []string // 告警信息
}

// CheckHealth 检查依赖健康状态（仅执行一次）
func CheckHealth() *HealthResult {
	healthCheckOnce.Do(func() {
		healthResult = doHealthCheck()
	})
	return healthResult
}

// doHealthCheck 执行实际的健康检查
func doHealthCheck() *HealthResult {
	result := &HealthResult{
		Warnings: make([]string, 0),
	}

	// 检查 Python
	pythonPath := findPython()
	if pythonPath != "" {
		result.PythonAvailable = true
		result.PythonPath = pythonPath

		// 检查 fpdf2
		if checkPythonModule(pythonPath, "fpdf2") {
			result.Fpdf2Available = true
		} else {
			result.Warnings = append(result.Warnings, "Python fpdf2 库未安装，PDF 写回将使用 Go 兜底")
		}

		// 检查 pdf2docx
		if checkPythonModule(pythonPath, "pdf2docx") {
			result.Pdf2docxAvaliable = true
		} else {
			result.Warnings = append(result.Warnings, "Python pdf2docx 库未安装，PDF 转 DOCX 功能不可用")
		}
	} else {
		result.Warnings = append(result.Warnings, "Python 解释器未找到，文档转换子进程不可用")
	}

	// 检查 LibreOffice
	loPath := findLibreOffice()
	if loPath != "" {
		result.LibreOfficeAvail = true
		result.LibreOfficePath = loPath
	} else {
		result.Warnings = append(result.Warnings, "LibreOffice 未找到，DOCX/PPTX 转换功能不可用")
	}

	// 输出检查结果
	if len(result.Warnings) > 0 {
		log.Printf("[fileproc-health] 依赖检查完成，发现 %d 个问题:", len(result.Warnings))
		for _, w := range result.Warnings {
			log.Printf("[fileproc-health]   - %s", w)
		}
	} else {
		log.Printf("[fileproc-health] 依赖检查通过：Python=%s, LibreOffice=%s", result.PythonPath, result.LibreOfficePath)
	}

	return result
}

// findPython 查找 Python 解释器
func findPython() string {
	// 优先检查环境变量
	if pyBin := os.Getenv("FILEPROC_PYTHON_BIN"); pyBin != "" {
		if _, err := exec.LookPath(pyBin); err == nil {
			return pyBin
		}
	}

	// 按优先级查找
	candidates := []string{"python3", "python", "python3.11", "python3.10", "python3.9"}
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return ""
}

// findLibreOffice 查找 LibreOffice
func findLibreOffice() string {
	// 优先检查环境变量
	if loBin := os.Getenv("FILEPROC_LIBREOFFICE_BIN"); loBin != "" {
		if _, err := exec.LookPath(loBin); err == nil {
			return loBin
		}
	}

	// 按优先级查找
	candidates := []string{"libreoffice", "soffice", "/usr/bin/libreoffice", "/Applications/LibreOffice.app/Contents/MacOS/soffice"}
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return ""
}

// checkPythonModule 检查 Python 模块是否可导入
func checkPythonModule(pythonPath, module string) bool {
	cmd := exec.Command(pythonPath, "-c", "import "+module)
	return cmd.Run() == nil
}

// GetPythonPath 获取 Python 解释器路径（带缓存）
func GetPythonPath() string {
	health := CheckHealth()
	if health.PythonAvailable {
		return health.PythonPath
	}
	return ""
}

// GetLibreOfficePath 获取 LibreOffice 路径（带缓存）
func GetLibreOfficePath() string {
	health := CheckHealth()
	if health.LibreOfficeAvail {
		return health.LibreOfficePath
	}
	return ""
}

// IsHealthy 检查核心依赖是否健康（Python + LibreOffice）
func IsHealthy() bool {
	health := CheckHealth()
	return health.PythonAvailable && health.LibreOfficeAvail
}
