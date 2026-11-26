package fileproc

import (
	"encoding/json"
	"testing"
)

// TestDispatchProbeDynamicFields 钉住 2026-11-26 三条动态读数 + w/ 配额字段：
// ① 四字段都能从 probe JSON 解出；② 缺键⇒nil（不许解成零值）；③ -1/0⇒具体值。
//
// 反证：把指针字段改成 int ⇒ 缺键解成 0，本测试的 nil 断言必红（那就是空转判据的成因）。
func TestDispatchProbeDynamicFields(t *testing.T) {
	var full DispatchProbe
	js := `{"ok":true,"selftest":0,"mem_gb":3.6,
		"mem_available_mb":2707,"disk_free_mb":27000,
		"cpu_load1":1.03,"workdir_used_mb":128}`
	if err := json.Unmarshal([]byte(js), &full); err != nil {
		t.Fatal(err)
	}
	if full.MemAvailableMB == nil || *full.MemAvailableMB != 2707 {
		t.Errorf("mem_available_mb 解错：%v", full.MemAvailableMB)
	}
	if full.DiskFreeMB == nil || *full.DiskFreeMB != 27000 {
		t.Errorf("disk_free_mb 解错：%v", full.DiskFreeMB)
	}
	if full.CPULoad1 == nil || *full.CPULoad1 != 1.03 {
		t.Errorf("cpu_load1 解错：%v", full.CPULoad1)
	}
	if full.WorkDirUsedMB == nil || *full.WorkDirUsedMB != 128 {
		t.Errorf("workdir_used_mb 解错：%v", full.WorkDirUsedMB)
	}

	// 缺键 ⇒ nil（不是 0）
	var missing DispatchProbe
	if err := json.Unmarshal([]byte(`{"ok":true,"selftest":0,"mem_gb":2}`), &missing); err != nil {
		t.Fatal(err)
	}
	if missing.MemAvailableMB != nil || missing.DiskFreeMB != nil ||
		missing.CPULoad1 != nil || missing.WorkDirUsedMB != nil {
		t.Errorf("缺键应解成 nil，实际：%v %v %v %v",
			missing.MemAvailableMB, missing.DiskFreeMB, missing.CPULoad1, missing.WorkDirUsedMB)
	}

	// -1/0 与缺键不同形（远端"读不到"上报 -1，主站按不就绪处理）
	var neg DispatchProbe
	if err := json.Unmarshal([]byte(`{"ok":true,"mem_available_mb":-1,"disk_free_mb":0}`), &neg); err != nil {
		t.Fatal(err)
	}
	if neg.MemAvailableMB == nil || *neg.MemAvailableMB != -1 {
		t.Errorf("-1 应原样传入：%v", neg.MemAvailableMB)
	}
	if neg.DiskFreeMB == nil || *neg.DiskFreeMB != 0 {
		t.Errorf("0 应原样传入（与 nil 分家）：%v", neg.DiskFreeMB)
	}
}
