// ============================================================================
// H4 QA 预筛打分单测：干净句高分、数字/占位符错误低分、同文轻扣分仍过线。
// ============================================================================
package qa

import "testing"

func TestH4ScreenPairScoring(t *testing.T) {
	cases := []struct {
		name        string
		src, tgt    string
		min, max    int // 分数区间（含）
		wantReasons bool
	}{
		{"干净句", "设备温度不得超过 45 ℃", "Die Gerätetemperatur darf 45 ℃ nicht überschreiten.", 92, 100, false},
		{"数字丢失", "扭矩为 12 Nm", "Le couple est de Nm", 0, 60, true},
		{"占位符丢失", "你好 {name}", "Hallo", 0, 60, true},
		{"译文为空", "任意原文", "", 0, 79, true},
		{"未翻译同文", "Status OK", "Status OK", 84, 100, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := ScreenPair(c.src, c.tgt)
			if r.Score < c.min || r.Score > c.max {
				t.Fatalf("分数 %d 不在 [%d,%d]: %+v", r.Score, c.min, c.max, r)
			}
			if c.wantReasons && len(r.Reasons) == 0 {
				t.Fatal("应带扣分原因")
			}
		})
	}
}
