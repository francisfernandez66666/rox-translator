package store

import (
	"testing"
)

// TestRejectRemoteArtifactPath §9-A2 的 store 侧落库守卫：库里不得出现指向远端派发机的路径。
// 负向锁配正向对照（与 fileproc.TestDispatchArtifactGuard 同口径）：远端路径必须判红，主站/临时路径必须放行。
func TestRejectRemoteArtifactPath(t *testing.T) {
	t.Setenv("FILEPROC_DISPATCH_ROOT", "/opt/fpdispatch")

	cases := []struct {
		name string
		path string
		want bool // true=应判红
	}{
		{"远端根下产物→红", "/opt/fpdispatch/w/abc/out.pdf", true},
		{"远端会话目录→红", "/opt/fpdispatch/w/abc", true},
		{"主站输出目录→绿", "/opt/translator/data/_output/x.pdf", false},
		{"主站上传目录→绿", "/opt/translator/data/_uploads/x.pdf", false},
		{"临时目录→绿", "/tmp/out/title_en.docx", false},
		{"空路径→绿", "", false},
		{"邻近但非远端根→绿", "/opt/fpdispatch2/w/out.pdf", false},
	}
	for _, c := range cases {
		err := rejectRemoteArtifactPath(c.path)
		gotRed := err != nil
		if gotRed != c.want {
			t.Errorf("%s：期望 red=%v 实际 red=%v（err=%v）", c.name, c.want, gotRed, err)
		}
	}
}
