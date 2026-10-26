// ============ 本文件职责中文说明 ============
// 2026-10-26 裁决 5／八-1／九-4 汇到一处后的"一把尺子"锁：单行补译这件事**只许有一个实现**。
//
// 为什么要有这一条：本轮把补译梯与两条档位键从 orchestrator 上收进 engine（图片原位链也要做
// 同一件事——"这一行还没翻出来，再给它几枪"）。上收本身是修，但**上收不彻底**才是新坑：
// 如果 orchestrator 里还留着半份自己读配置的旧实现，就会出现
// 「文本工单一行打三枪、图片一行打五枪」——同一个动作两个成本、两个灵敏度，
// 而两侧各自都"看起来符合配置"（本仓反复登记的那一族「多把尺子」）。
//
// 三条腿都是**守卫级**（判接线），不是判据级（判函数对不对）：
// 委托关系断了不会让任何行为测试变红——两条链各自仍能翻出正确答案，只是钱与等待不再同源。
// 反向自证在 fileproc/ocr_image.go 那侧由 shape 闸门管，本文件只管"读点有几处"这一维。
// ============================================
package engine

import (
	"os"
	"strings"
	"testing"
)

// tlKeyNames 两条档位键的字面名（唯一事实源＝engine 这两个函数里的现读）。
var tlKeyNames = []string{"text_line_retry_max", "text_line_upstream_dead_streak"}

// readGateSource 读源文件正文；读不到即红（"没读到"与"没有该写法"必须分得开）。
func readGateSource(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s 失败: %v（判据读不到文件＝失明，不许静默放行）", path, err)
	}
	return string(b)
}

// TestRetryLadderHasSingleImplementation 文本逐行链必须**委托**引擎那一份补译梯，
// 且不再自己读那两把档位键。
func TestRetryLadderHasSingleImplementation(t *testing.T) {
	// 跨包读文件：engine 测试在 internal/engine，orchestrator 在同级目录。
	src := readGateSource(t, "../orchestrator/text_lines.go")
	for _, needle := range []string{"RetryLineUntilUsable", "LineRetryMax", "LineDeadStreakMin"} {
		if !strings.Contains(src, needle) {
			t.Errorf("orchestrator/text_lines.go 里读不到 %s ⇒ 委托被摘掉，文本链在用自己的第二份实现", needle)
		}
	}
	for _, key := range tlKeyNames {
		if strings.Contains(src, `"`+key+`"`) {
			t.Errorf("orchestrator 仍在自己读档位键 %q ⇒ 两侧会漂成两个灵敏度（补译轮数/早停连续数各一套）", key)
		}
	}
	// 自证：判据必须能认出"自己读配置"这一形态，否则上面两条负向是空转。
	if n := strings.Count(readGateSource(t, "instruction_echo.go"), `ConfigInt(`); n < 2 {
		t.Fatalf("engine 侧 ConfigInt 现读只有 %d 处（两把键各一处是下限）⇒ 锚漂了，本条锁看不见真实读点", n)
	}
	if strings.Contains("x \"text_line_retry_max\" y", "\""+tlKeyNames[0]+"\"") == false {
		t.Fatal("自证夹具异常：字符串包含判据本身不成立")
	}
}

// TestFileChainLastResortIsSingleSegment 文件链硬闸兜底那两条腿必须是**单段请求**。
// 为什么：长度爆炸那一族的病因就在批量上下文里（裁决八-3d 原话"6 字格吐 2100 字符整封邮件"），
// 只换措辞把同一批再打一遍＝把病因留着。
//
// 射程口径（写清楚，免得下一位以为它管得住整个函数）：兜底腿与批量主路都在 HandleFile 里
// （实测行距 34／72 行），整函数级"不许出现 BatchTranslate"必然假红；本条按**调用点邻域**判
// ——谁把 `translateSegmentMinimal` 换成批量重发，那一行必然落在窗口内。
// 窗口外的批量调用是主路（合法），不在这一条的射程里；
// "补漏段别再攒回一批"的行为面由 engine 侧 file_echo_pipeline／image_retry 那族行为锁管。
func TestFileChainLastResortIsSingleSegment(t *testing.T) {
	lines := strings.Split(readGateSource(t, "file.go"), "\n")
	const win = 8 // 行窗口：够包住那一段兜底循环体，又不至于吃到主路
	sites := []int{}
	for i, l := range lines {
		if strings.Contains(l, "translateSegmentMinimal(") && !strings.HasPrefix(strings.TrimSpace(l), "//") {
			sites = append(sites, i)
		}
	}
	if len(sites) < 2 {
		t.Fatalf("只找到 %d 处单段兜底调用（应 ≥2：逐段兜底腿与漏译率定向补译腿）⇒ 锚漂了或有一腿被摘，本条锁失明", len(sites))
	}
	for _, at := range sites {
		lo := at - win
		if lo < 0 {
			lo = 0
		}
		hi := at + win
		if hi > len(lines) {
			hi = len(lines)
		}
		for i := lo; i < hi; i++ {
			l := lines[i]
			if strings.HasPrefix(strings.TrimSpace(l), "//") {
				continue
			}
			if strings.Contains(l, "BatchTranslate(") || strings.Contains(l, "BatchTranslateMarked(") {
				t.Errorf("file.go:%d 单段兜底调用（第 %d 行）邻域内出现批量重发 ⇒ 补漏又回到批量上下文，长度爆炸串扰的病因没被摘掉：%s",
					i+1, at+1, strings.TrimSpace(l))
			}
		}
	}
	// 自证：窗口判据必须真能认出"把这一行换成批量"这一形态（否则上面是恒绿的形状检查）
	mutated := append([]string{}, lines...)
	at := sites[0]
	mutated[at] = strings.Replace(mutated[at], "translateSegmentMinimal(", "BatchTranslateMarked(", 1)
	found := false
	for i := at - win; i <= at+win && i < len(mutated); i++ {
		if i >= 0 && strings.Contains(mutated[i], "BatchTranslateMarked(") {
			found = true
		}
	}
	if !found {
		t.Error("自证失败：把单段调用换成批量后窗口没抓到 ⇒ 上面的判据是摆设")
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
