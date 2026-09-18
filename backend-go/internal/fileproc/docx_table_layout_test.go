package fileproc

import (
	"os/exec"
	"testing"
)

// runPySelftest 运行 Python 侧自检脚本，返回 (合并输出, 退出码, 是否可运行)。
// 退出码约定：0=通过，1=断言失败，2=跳过（依赖缺失，如缺 python-docx）。
func runPySelftest(t *testing.T, args ...string) (string, int, bool) {
	t.Helper()
	py := findPython()
	if py == "" {
		return "", 0, false
	}
	cmd := exec.Command(py, args...)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("执行 python %v 失败: %v\n%s", args, err, out)
		}
		code = ee.ExitCode()
	}
	return string(out), code, true
}

// TestPdfColumnWidthsPlanner 守护「块不匹配」修复的纯函数不变量：
// docx_translate.column_widths_from_samples 必须以 tcW 真值还原列几何（取中位数抗跨列
// 干扰、无样本列补位、推不出返回空列表）。仅依赖标准库，任何环境都应实跑。
func TestPdfColumnWidthsPlanner(t *testing.T) {
	out, code, ok := runPySelftest(t, "docx_translate.py", "selftest")
	if !ok {
		t.Skip("Python 不可用，跳过列宽推导自检")
	}
	if code != 0 {
		t.Fatalf("column_widths_from_samples 自检失败（退出码 %d）:\n%s", code, out)
	}
	t.Logf("docx_translate.py selftest: %s", out)
}

// TestPdfTableLayoutPreserved 守护「块不匹配」修复的产物断言：驱动
// docx_table_selftest.py —— 用真实 pdf2docx 形态做夹具（tblGrid 是等宽占位符 50/50、
// tcW 才是从原 PDF 量出的真实列宽 26.9/73.1），跑 normalize_tables 后断言
// tblLayout=fixed、tblGrid 已按 tcW 真值重建（绝不是 50/50）、tcW 保留且与新栅格一致、
// 行高规则统一 atLeast、采不到 tcW 的表退回 autofit 兜底。
//
// 回归意义：交付版本的 autofit + 删光 tcW 实现会让「layout=fixed」「栅格≈tcW 真值」
// 直接失败（实测交付 PDF 出现 Distributor|s× / DMS/table|ts 词中被切断、换行碎片落到
// 相邻列下方的「大量块不匹配」）。而「信等宽占位栅格」的中间实现会被 50/50 断言抓住。
//
// 需要 python-docx；不可用时脚本退出码 2 → 本测试跳过（生产 venv 内会实跑）。
func TestPdfTableLayoutPreserved(t *testing.T) {
	out, code, ok := runPySelftest(t, "docx_table_selftest.py")
	if !ok {
		t.Skip("Python 不可用，跳过表格版式产物自检")
	}
	switch code {
	case 0:
		t.Logf("docx_table_selftest: %s", out)
	case 2:
		t.Skipf("python-docx 不可用，跳过产物自检: %s", out)
	default:
		t.Fatalf("normalize_tables 产物自检失败（退出码 %d）:\n%s", code, out)
	}
}
