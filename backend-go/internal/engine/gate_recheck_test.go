// ============ gate_recheck_test.go · 闸门多次重检上限的 env 可配性断言（§6.4 Q2） ============
// 锁定方案 Q2/D2：TRANS_GATE_RECHECK_MAX 可配，缺省/非法/非正一律回退 2；
// 默认 2 = 首检 1 次 + 最多 1 次重译，与补齐前单次重译行为等价（成本基线不变）。
package engine

import (
	"os"
	"strings"
	"testing"
)

func TestGateRecheckMaxEnv(t *testing.T) {
	cases := []struct {
		name string
		val  string
		want int
	}{
		{"缺省回退2", "", 2},
		{"合法3", "3", 3},
		{"合法1灰度档", "1", 1},
		{"非法字符串回退2", "abc", 2},
		{"零回退2", "0", 2},
		{"负数回退2", "-1", 2},
		{"带空格合法", " 4 ", 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.val != "" {
				t.Setenv("TRANS_GATE_RECHECK_MAX", c.val)
			} else {
				t.Setenv("TRANS_GATE_RECHECK_MAX", "")
			}
			if got := gateRecheckMax(); got != c.want {
				t.Errorf("TRANS_GATE_RECHECK_MAX=%q ⇒ %d，期望 %d", c.val, got, c.want)
			}
		})
	}
}

// TestGateRecheckLoopWired 结构断言：Q2 的多次重检循环必须真的接在 check() 的重译腿上。
// 若有人把 for round := 1; round < gateRecheckMax() 改回单次重译，本测试当场点名——
// 防止「env 助手存在、但循环没用它」这种装配级假绿（同 §11 装配漏一处的教训）。
func TestGateRecheckLoopWired(t *testing.T) {
	b, err := os.ReadFile("gates.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if !strings.Contains(src, "round < gateRecheckMax()") {
		t.Error("check() 重译腿未使用 gateRecheckMax() 循环（Q2 多次重检未接线）")
	}
	// 反证自锁：把一个已知坏形态喂给同一判据，必须判缺失（证明上一条不是恒真）。
	bad := "if fixed := e.TranslateWithFeedback(ctx, source, lang, msg, config.StageReview); true {"
	if strings.Contains(bad, "round < gateRecheckMax()") {
		t.Error("判据自证失败：坏形态也被当成已接线")
	}
}
