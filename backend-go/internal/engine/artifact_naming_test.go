// ============ 本文件职责中文说明 ============
// O-03（2026-10-24 用户令）：译文产物文件名统一加 `-<lang>` 后缀。
// 本锁只测命名工具与下载 basename 选择，不跑真实文件写回（那部分由 UAT 下载用例覆盖）。
// ============================================
package engine

import "testing"

func TestArtifactLangSuffixDefault(t *testing.T) {
	e := &Engine{}
	if got := e.artifactLangSuffix("en"); got != "-en" {
		t.Fatalf("默认应 -en，实得 %q", got)
	}
}

func TestDeliverableBaseName(t *testing.T) {
	cases := []struct {
		path, fallback, want string
	}{
		{"/data/translated/x/uat_sample-en.txt", "uat_sample.txt", "uat_sample-en.txt"},
		{"/data/translated/x/报价单-ja.docx", "报价单.docx", "报价单-ja.docx"},
		{"/data/translated/1792786023938279726_old.txt", "uat_sample.txt", "uat_sample.txt"},
		{"", "fallback.txt", "fallback.txt"},
	}
	for _, c := range cases {
		if got := DeliverableBaseName(c.path, c.fallback); got != c.want {
			t.Errorf("DeliverableBaseName(%q,%q)=%q want %q", c.path, c.fallback, got, c.want)
		}
	}
}
