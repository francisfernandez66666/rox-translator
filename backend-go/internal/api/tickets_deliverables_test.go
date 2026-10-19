// ============ tickets_deliverables_test.go · 职责说明 ============
// G13（2026-10-19）：工单详情面的交付件清单 `ticketDeliverables`。
//
// 缺陷形态（D17）：多产物单（音频四件）在 service 层被打成一个 zip 存进 result_path，
// 而详情面只回 files（输入件），客户在页面上看不见自己已拿到手的产物。
//
// 判据：按单号回读 zip 条目即得四件；单产物单列一行；工单级与文件级同值时去重；
// 纯文案旁路另计为 text 档。真值只有磁盘上那个 zip（不新造第二份清单）。
//
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestTicketDeliverables
// =============================================
package api

import (
	"archive/zip"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"translator/internal/store"
)

// makeZip 造一个含指定条目的临时 zip，返回路径。
func makeZip(t *testing.T, dir string, name string, entries map[string]string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for en, body := range entries {
		w, werr := zw.Create(en)
		if werr != nil {
			t.Fatal(werr)
		}
		if _, werr = w.Write([]byte(body)); werr != nil {
			t.Fatal(werr)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// namesOf 取交付件名列表并排序（顺序无关比较）。
func namesOf(ds []ticketDeliverableItem) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.Name)
	}
	sort.Strings(out)
	return out
}

// hasKind 判断某名字是否以指定 kind 出现。
func hasKind(ds []ticketDeliverableItem, name, kind string) bool {
	for _, d := range ds {
		if d.Name == name && d.Kind == kind {
			return true
		}
	}
	return false
}

// TestTicketDeliverables G13 主判据＋去重＋纯文案旁路。
func TestTicketDeliverables(t *testing.T) {
	dir := t.TempDir()
	zipPath := makeZip(t, dir, "audio.zip", map[string]string{
		"original.mp3": "AAAA",
		"Voice_en.m4a": "BBBBBB",
		"text_en.srt":  "CC",
		"text_en.md":   "DD",
	})
	textZip := makeZip(t, dir, "texts.zip", map[string]string{"text_en.md": "DD"})
	single := filepath.Join(dir, "out.docx")
	if err := os.WriteFile(single, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	// ① 多产物 zip：四条全出，且工单级与文件级同值不重复
	ds := ticketDeliverables(
		&store.Ticket{ResultPath: zipPath},
		[]*store.TicketFile{{ResultPath: zipPath}},
	)
	if len(ds) != 4 {
		t.Fatalf("音频四件应回 4 条，实际 %d：%v", len(ds), namesOf(ds))
	}
	for _, n := range []string{"original.mp3", "Voice_en.m4a", "text_en.srt", "text_en.md"} {
		if !hasKind(ds, n, "restore") {
			t.Errorf("缺交付件 %q: %v", n, namesOf(ds))
		}
	}
	// 大小取自 zip 头
	for _, d := range ds {
		if d.Name == "Voice_en.m4a" && d.Size != 6 {
			t.Errorf("Voice_en.m4a 大小应为 6，实际 %d", d.Size)
		}
	}

	// ② 单产物：列一行（文件名取 basename）
	ds = ticketDeliverables(&store.Ticket{ResultPath: single}, nil)
	if len(ds) != 1 || ds[0].Name != "out.docx" || ds[0].Size != 5 || ds[0].Kind != "restore" {
		t.Fatalf("单产物应回 1 行 out.docx(5)，实际 %+v", ds)
	}

	// ③ 纯文案旁路：zip 条目按 text 档
	ds = ticketDeliverables(&store.Ticket{ResultPath: zipPath, TextResultPath: textZip}, nil)
	if !hasKind(ds, "text_en.md", "text") {
		t.Fatalf("纯文案旁路未被列为 text 档：%+v", ds)
	}

	// ④ 无产物：空清单（不是 nil 崩溃）
	if ds := ticketDeliverables(&store.Ticket{}, nil); len(ds) != 0 {
		t.Fatalf("无产物应回空清单，实际 %+v", ds)
	}
	// ⑤ 路径不存在：跳过而不报错
	if ds := ticketDeliverables(&store.Ticket{ResultPath: filepath.Join(dir, "nope.zip")}, nil); len(ds) != 0 {
		t.Fatalf("缺件路径应跳过，实际 %+v", ds)
	}
}
