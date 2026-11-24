package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestWriteRecDryRunPrintsVerdict(t *testing.T) {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("创建输出管道失败: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = old })

	if err := writeRec(nil, changeRec{ID: 7, Src: "粉丝", Before: "旧译文", After: "新译文", Action: "repaired", Reason: "复核通过"}); err != nil {
		t.Fatalf("dry-run 回执输出失败: %v", err)
	}
	_ = w.Close()
	os.Stdout = old
	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("读取 dry-run 回执失败: %v", err)
	}
	got := string(body)
	for _, must := range []string{"DRY|", `"id":7`, `"action":"repaired"`, `"src":"粉丝"`, `"after":"新译文"`} {
		if !strings.Contains(got, must) {
			t.Fatalf("dry-run 回执缺 %q: %s", must, got)
		}
	}
}
