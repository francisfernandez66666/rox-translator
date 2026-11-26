package fileproc

import (
	"os"
	"testing"
)

// TestDispatchCapacityTier 钉住"派发能力上限"这一档的默认与覆盖口径（§3.7）。
//
// 三把尺子不许互相靠拢：上传闸 200 MiB / 分流下限 20 MiB / 派发上限 100 MiB。
// 反证：把上限抬到 200 ⇒ 本测试红（防止有人顺手把容量闸跟上传闸并成一档）。
func TestDispatchCapacityTier(t *testing.T) {
	t.Setenv(envDispatchMaxMB, "")
	got := dispatchMaxPayloadBytes()
	want := int64(100) << 20
	if got != want {
		t.Fatalf("默认派发上限 = %d，期望 %d", got, want)
	}
	if got >= MaxUploadBytes {
		t.Errorf("派发上限 %d 不该 ≥ 上传闸 %d（两档必须分开）", got, MaxUploadBytes)
	}
	if got <= dispatchMinBytes() {
		t.Errorf("派发上限 %d 不该 ≤ 分流下限 %d", got, dispatchMinBytes())
	}

	// 覆盖口：env 可抬/可降，但降到分流下限以下时被钳住
	t.Setenv(envDispatchMaxMB, "60")
	if v := dispatchMaxPayloadBytes(); v != int64(60)<<20 {
		t.Errorf("env 覆盖 60 MiB 未生效：%d", v)
	}
	t.Setenv(envDispatchMaxMB, "1")
	if v := dispatchMaxPayloadBytes(); v != dispatchMinBytes() {
		t.Errorf("env 误配 1 MiB 应钳到分流下限 %d，实际 %d", dispatchMinBytes(), v)
	}
	t.Setenv(envDispatchMaxMB, "abc")
	if v := dispatchMaxPayloadBytes(); v != want {
		t.Errorf("非法 env 应回落默认 %d，实际 %d", want, v)
	}
	os.Unsetenv(envDispatchMaxMB)
}

// TestDispatchCapacityBoundary 钉住容量判据边界：60/100 可派、101 over_capacity。
// 这是规则 2b 的纯函数形态（决策器 B 阶段直接复用本函数，不重写数字）。
func TestDispatchCapacityBoundary(t *testing.T) {
	t.Setenv(envDispatchMaxMB, "")
	cap := dispatchMaxPayloadBytes()
	cases := []struct {
		name string
		mb   int64
		want bool
	}{
		{"60MiB", 60, true},
		{"100MiB", 100, true},
		{"101MiB", 101, false},
		{"200MiB", 200, false},
	}
	for _, c := range cases {
		got := c.mb<<20 <= cap
		if got != c.want {
			t.Errorf("%s：over_capacity 判定 got=%v want=%v", c.name, got, c.want)
		}
	}
	// 上传闸放行 200 MiB 与派发上限拒绝 200 MiB 必须同时成立（两档独立）
	if int64(200)<<20 > MaxUploadBytes {
		t.Error("上传闸应放行 200 MiB")
	}
	if int64(200)<<20 <= cap {
		t.Error("派发上限应拒绝 200 MiB")
	}
}
