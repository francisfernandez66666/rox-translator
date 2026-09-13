// ============================================================================
// ★ H12 TMX 导出查询层测试：ExportRows 语言过滤/租户隔离/Langs 填充。
// ============================================================================
package kb

import "testing"

func TestH12ExportRows(t *testing.T) {
	k, err := Open(t.TempDir() + "/kb.db")
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	if _, e := k.SaveBack("你好 <世界> & 100%", map[string]string{"en": "Hello <world> & 100%", "de": "Hallo"}, "approved", 1); e != nil {
		t.Fatal(e)
	}
	if _, e := k.SaveBack("仅英文", map[string]string{"en": "EN only"}, "approved", 1); e != nil {
		t.Fatal(e)
	}
	if _, e := k.SaveBack("他租户", map[string]string{"en": "other"}, "approved", 2); e != nil {
		t.Fatal(e)
	}
	rows, e := k.ExportRows(1, "")
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) != 2 {
		t.Fatalf("租户1应导出 2 行: %d", len(rows))
	}
	found := false
	for _, r := range rows {
		if r.Langs["en"] != "" && r.Langs["de"] != "" {
			found = true
			if r.Langs["en"] != "Hello <world> & 100%" {
				t.Fatalf("Langs 未原样填充: %q", r.Langs["en"])
			}
		}
	}
	if !found {
		t.Fatal("双语句对未命中")
	}
	// 按语言过滤：de 只有 1 行
	drows, e := k.ExportRows(1, "de")
	if e != nil || len(drows) != 1 {
		t.Fatalf("de 过滤错误: %d %v", len(drows), e)
	}
	if _, e := k.ExportRows(1, "xx"); e == nil {
		t.Fatal("非法语言代码应报错")
	}
}
