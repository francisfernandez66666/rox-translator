// ============ md_heading_number_test.go · 职责说明 ============
// 2026-11-22 真值验收 L143/L190 的缺陷锁：标题开头的「编号骨架」必须留在翻译片段之外。
//
// 现网形态（Word/飞书导出的中文档，每个数字各包一层加粗，相邻闭/开标记连写成 `****`）：
//
//	### **0****6****  组织与岗位要求**
//
// 旧实现把整段当可译键送模型，模型把标记合并成 `******…******` 并把编号 `06` 一起丢掉
// （L190 实测 `******ארגון ודרישות תפקיד******`）；L143 更坏，正文整段被译成泰文残片。
//
// 本文件钉三件事：
//  1. 提取键＝纯正文（编号与首尾标记都不在键里）——模型看不到畸形标记；
//  2. 写回后编号块与收尾标记**逐字节等于源**（结构性保证，不靠模型自觉）；
//  3. 反向对照：`**A** 与 **B**`、`|**指标**|`、`**2\.1**`、`**5G 网络**` 这类合法形态一个字节都不动。
//
// ============================================
package fileproc

import (
	"strings"
	"testing"
)

// TestHeadingNumberBlockStaysInSkeleton 编号骨架不进翻译键、写回逐字节保留。
func TestHeadingNumberBlockStaysInSkeleton(t *testing.T) {
	cases := []struct {
		name string
		md   string
		key  string // 必须被提取的正文键（不含编号与标记）
		tr   string
		want string // 写回后的整行
		// rawKey=true：该形态刻意**不**享受编号骨架豁免（非标题行），键里保留标记。
		rawKey bool
	}{
		{
			name: "L190 实测形态（6 个标记 token、编号 06）",
			md:   "### **0****6****  组织与岗位要求**\n",
			key:  "组织与岗位要求",
			tr:   "ארגון ודרישות תפקיד",
			want: "### **0****6****  ארגון ודרישות תפקיד**\n",
		},
		{
			name: "L143 实测形态（编号 05）",
			md:   "### **0****5**** 研发预算与选型：自研 vs 供应商**\n",
			key:  "研发预算与选型：自研 vs 供应商",
			tr:   "תקציב פיתוח ובחירת סוג: פיתוח עצמי vs ספקים",
			want: "### **0****5**** תקציב פיתוח ובחירת סוג: פיתוח עצמי vs ספקים**\n",
		},
		{
			name: "规范编号标题（01）",
			md:   "### **01  为什么要做**\n",
			key:  "为什么要做",
			tr:   "מה צריך לעשות",
			want: "### **01  מה צריך לעשות**\n",
		},
		{
			name: "编号自己成对加粗（**05**）",
			md:   "## **05** 研发预算\n",
			key:  "研发预算",
			tr:   "תקציב פיתוח",
			want: "## **05** תקציב פיתוח\n",
		},
		{
			name:   "无标题前缀的加粗行不享受该豁免",
			md:     "**05 标题**\n",
			key:    "**05 标题**",
			tr:     "**05 כותרת**",
			want:   "**05 כותרת**\n",
			rawKey: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			keys := mdKeys(t, c.md)
			// 失败信息按档位点名（变异体 M0364 按「标题编号骨架豁免失效」这一串判
			// "红在锁自己的消息上"，而不是红在某条通用比对差异上）。
			if !c.rawKey {
				found := false
				for _, k := range keys {
					if k == c.key {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("标题编号骨架豁免失效（L143/L190 复发：编号与标记重新进翻译键）：缺少提取键 %q，实际=%#v", c.key, keys)
				}
			}
			mdHas(t, keys, c.key)
			if !c.rawKey {
				for _, k := range keys {
					if strings.Contains(k, "**") {
						t.Fatalf("编号骨架仍进了翻译键：%q（全部键=%#v）", k, keys)
					}
				}
			}
			got := mdApply(t, c.md, map[string]string{c.key: c.tr})
			if got != c.want {
				t.Fatalf("写回不符：\n源=%q\n期望=%q\n实得=%q", c.md, c.want, got)
			}
			// 未翻译时逐字节不变（骨架永不被改动的最强断言）
			mdIdentity(t, c.md)
		})
	}
}

// TestHeadingNumberBlockNegativeControls 合法标记形态一个字节都不许动。
//
// 这些形态**不是**编号骨架：没有数字（`**A** 与 **B**`）、数字后紧跟字母（`**5G`）、
// 带转义点（`**2\.1**`）、表格单元格（`|**指标**|`）、以及不以强调标记起手的标题。
func TestHeadingNumberBlockNegativeControls(t *testing.T) {
	cases := []struct{ name, md, key string }{
		{"多段加粗无编号", "### **A** 与 **B**\n", "**A** 与 **B**"},
		{"编号后紧跟字母", "### **5G 网络**\n", "**5G 网络**"},
		{"带转义点的编号", "### **2\\.1** 小节标题\n", "**2\\.1** 小节标题"},
		{"表格单元格加粗", "|**指标**|值|\n", "**指标**"},
		{"纯数字开头（无标记）", "### 2024 年度回顾\n", "2024 年度回顾"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			keys := mdKeys(t, c.md)
			mdHas(t, keys, c.key)
			mdIdentity(t, c.md)
		})
	}
}
