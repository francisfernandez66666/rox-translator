// ============ md_excess_test.go · 标记过发收敛（P1/G5 加固，真值验收 §13.2 暴露） ============
// 锁定：模型把已带 ** 的骨架再包一层 / 把编号逐字加粗时，按源计数削回，
// " ** 计数守恒、奇数行=0"，且**只删标记、内容逐字节保留**。
package fileproc

import (
	"strings"
	"testing"
)

func TestMdTrimExcessMarkers(t *testing.T) {
	cases := []struct {
		name string
		src  string
		tr   string
		want string
	}{
		{
			name: "标题被再包一层(真值L18形态)",
			src:  `**1\.2 价值测算：效率、成本、订单**`,
			tr:   `****1.2 הערכת העלות****`,
			want: `**1.2 הערכת העלות**`,
		},
		{
			name: "编号逐字加粗(真值L128形态)",
			src:  `### **04  为什么要做**`,
			tr:   `### ****0****4****_canonical**`,
			want: `### **04_canonical**`,
		},
		{
			name: "中间多一对强调(真值诊断形态)",
			src:  `**1\.2 价值测算**`,
			tr:   `**1.2 הערכת** **העלות**`,
			want: `**1.2 הערכת העלות**`,
		},
		{
			name: "正常单对不动",
			src:  `**指标**`,
			tr:   `**מדד**`,
			want: `**מדד**`,
		},
		{
			name: "模型删了标记不在此处补(交给restore)",
			src:  `**指标**`,
			tr:   `מדד`,
			want: `מדד`,
		},
		{
			name: "源无标记时模型加粗属内容增强不动",
			src:  `价值测算`,
			tr:   `**הערכת ערך**`,
			want: `**הערכת ערך**`,
		},
		{
			name: "反引号多塞收敛",
			src:  "`shrink_draft`",
			tr:   "`` shrink_draft ``",
			want: "` shrink_draft `",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mdTrimExcessMarkers(c.src, c.tr)
			if got != c.want {
				t.Errorf("trim 结果不符\n  src=%q\n  tr =%q\n  got =%q\n  want=%q", c.src, c.tr, got, c.want)
			}
			// 不变量：内容（去掉所有标记字符后）不得变少/改变——只删标记不动内容。
			stripMarks := func(s string) string {
				r := strings.NewReplacer("**", "", "__", "", "~~", "", "`", "")
				return strings.TrimSpace(r.Replace(s))
			}
			if stripMarks(got) != stripMarks(c.tr) {
				t.Errorf("内容被改动（违反只删标记）: got content=%q, tr content=%q", stripMarks(got), stripMarks(c.tr))
			}
		})
	}
}

// TestMdExcessThenRestore 验证双向守恒串联：过发削回 + 缺失补回，最终计数==源。
func TestMdExcessThenRestore(t *testing.T) {
	src := `**1\.2 价值测算：效率、成本、订单**`
	// 模型既多塞了成对标记、又吞了另一个片段的 <br> —— 两次操作应各自按源对齐
	tr := `****1.2 הערכת העלות****`
	out := mdRestoreMarkers(src, mdTrimExcessMarkers(src, tr))
	if n := strings.Count(out, "**"); n != 2 {
		t.Errorf("双向守恒后 ** 计数=%d，期望 2（源计数）: %q", n, out)
	}
	if strings.Count(out, "הערכת העלות") == 0 {
		t.Errorf("内容丢失: %q", out)
	}
}
