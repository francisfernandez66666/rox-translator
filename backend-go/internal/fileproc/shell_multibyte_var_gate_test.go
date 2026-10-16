// ============================================================================
// shell_multibyte_var_gate_test.go — ★ 2026-10-16 把 AGENTS §一·7 那条
// 「变量名紧挨中文等多字节字符时一律写 ${VAR}」从**文字纪律**收成机械闸门。
//
// 因果链（为什么要有这条锁）：
//
//	2026-10-16 现网实证过一次「本机段没跑、公网段一行没出」——`deploy/postdeploy_20261012.sh`
//	第 333 行写成 `code=$CODE）`（变量名后面直接跟中文右括号），macOS 自带的 **bash 3.2**
//	把那个多字节字节当成变量名的一部分 ⇒ `set -u` 下 `unbound variable` 当场中断脚本。
//	生产跑 Linux bash ≥4 不受影响，**所以它只在"本机跑的那一段"发作**：屏幕上一行英文报错，
//	外面的人极易读成"这段本来就不跑"。全仓同族一次扫出 276 处、散在几十个脚本里。
//
// 三条设计（对齐仓内既有口径）：
//
//	① 判据是**纯函数** shellMultibyteVarProblems：仓库里每一份脚本与反证用的合成件
//	   都走同一个函数（§三「判据级断言代替不了守卫级」）。
//	② 射程**派生**而非写死名单：deploy/ + scripts/ 下全部 *.sh（取证目录 `发布前E2E_UAT_*`
//	   是历史证据、不属代码，按 §一·9 的排除口径剔除）。写死名单等于"下一份新脚本不受保护"，
//	   而这一族正是"照上一份抄"长出来的。射程为空即 Fatal（闸门空转不许静默绿）。
//	③ 反证**打在靶子上**：合成件里 `$X）` 必须被抓、`${X}）`／单引号里／注释里／后面是 ASCII
//	   的一律不许抓——一条"永远不报"的规则也能让正测全绿，所以每条腿都要有对照。
//
// ⚠️ 只判**非整行注释**且**不在单引号内**的位置（与改写脚本同口径）：注释不喂给 bash；
// 单引号内 `$VAR` 与 `${VAR}` 都是字面量，改了反而是**改文案**（显示字节变了）。
// 本文件只读仓库文本，绝不执行、不 ssh、不碰现网。
// ============================================================================
package fileproc

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shellMultibyteVarScanRoots 射程＝仓库下这两个目录（相对仓库根）。
var shellMultibyteVarScanRoots = []string{"deploy", "scripts"}

// shellMultibyteVarExcludedPrefixes 排除的**顶层目录名**前缀：取证材料不是代码
// （与 §一·9 的 EXCL「文档/取证不外推」同一口径，也与 push_code_only.sh 的清单同向）。
var shellMultibyteVarExcludedPrefixes = []string{"发布前E2E_UAT_"}

// isShellVarNameByte 判一个字节能不能出现在「$ 后面那个未加花括号的变量名」里
// （字母／下划线／数字；首个字符的限制由调用方另判）。
func isShellVarNameByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// shellMultibyteVarProblems 判一段 shell 文本里有没有「未加花括号的 $VAR 紧跟多字节字符」。
// 返回的问题串形如 `行号: 片段`，空＝干净。三档跳过（都是刻意放行，各有理由）：
//
//	· 整行注释（trim 后以 # 开头）——不喂给 bash；
//	· 单引号内——`$VAR` 与 `${VAR}` 在那里都是字面量，改它＝改文案字节；
//	· `\$VAR`（被反斜杠转义）——本来就是字面美元号。
func shellMultibyteVarProblems(rel string, content string) []string {
	var problems []string
	lines := strings.Split(content, "\n")
	for ln, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		inSingle := false
		for i := 0; i < len(line); i++ {
			c := line[i]
			if c == '\'' {
				inSingle = !inSingle
				continue
			}
			if inSingle {
				continue
			}
			if c == '\\' {
				i++ // 跳过被转义的下一字节（含 \$）
				continue
			}
			if c != '$' {
				continue
			}
			// `$` 之后到这里：先认「已加花括号」形态（正是修法，放行）。
			if i+1 < len(line) && line[i+1] == '{' {
				continue
			}
			j := i + 1
			if j >= len(line) {
				continue
			}
			// 名字首字符：字母／下划线，或数字（`$1` 这类位置参数）。`$?`／`$#`／`$(`／`$ `
			// 一律放行。⚠️ 位置参数只吃**数字连排**——bash 对 `$1_判绿` 是 `$1` + 字面 `_判绿`
			// （2026-10-16 在 macOS bash 3.2.57 上实测：`$1_判绿` 正常、`$X_判绿` 与 `$X判绿`
			// 一律 `unbound variable`）⇒ 按"字母名最长匹配"去判位置参数会造出 4 条假红。
			if !isShellVarNameByte(line[j]) {
				continue
			}
			if line[j] >= '0' && line[j] <= '9' {
				for j < len(line) && line[j] >= '0' && line[j] <= '9' {
					j++
				}
			} else {
				for j < len(line) && isShellVarNameByte(line[j]) {
					j++
				}
			}
			// 变量名到此结束：紧跟的第一个字节是多字节（UTF-8 首字节 ≥ 0x80）就要报。
			if j < len(line) && line[j] >= 0x80 {
				problems = append(problems, rel+":"+itoa(ln+1)+": "+strings.TrimSpace(line)+
					"   ⇒ 变量名「"+line[i:j]+"」后紧跟多字节字符，改写成 ${"+line[i+1:j]+"}")
			}
			i = j - 1
		}
	}
	return problems
}

// itoa 极简整数转字符串（避免为一条错误消息引入 fmt 依赖而掩盖本文件意图）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	p := len(b)
	for n > 0 {
		p--
		b[p] = byte('0' + n%10)
		n /= 10
	}
	return string(b[p:])
}

// collectShellScriptsUnder 收集 root/<dir> 下所有 *.sh，按 §一·9 口径剔除取证目录。
func collectShellScriptsUnder(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, d := range shellMultibyteVarScanRoots {
		base := filepath.Join(root, d)
		err := filepath.WalkDir(base, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() {
				name := e.Name()
				if p != base {
					for _, pref := range shellMultibyteVarExcludedPrefixes {
						if strings.HasPrefix(name, pref) {
							return fs.SkipDir
						}
					}
				}
				return nil
			}
			if strings.HasSuffix(e.Name(), ".sh") {
				out = append(out, p)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("走 %s 失败：%v", base, err)
		}
	}
	return out
}

// TestShellScriptsBraceMultibyteAdjacentVars 主判据：射程内**每一份**脚本都过同一把尺子。
func TestShellScriptsBraceMultibyteAdjacentVars(t *testing.T) {
	root := findDispatchRepoRoot(t)
	files := collectShellScriptsUnder(t, root)
	if len(files) < 40 {
		t.Fatalf("射程内只有 %d 份 .sh（下限 40）⇒ 目录结构变了或闸门扫空了；"+
			"射程为空时这条锁看起来是绿的，所以这里直接判死", len(files))
	}
	var problems []string
	for _, p := range files {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			t.Fatalf("算相对路径失败：%v", err)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("读 %s 失败：%v", rel, err)
		}
		problems = append(problems, shellMultibyteVarProblems(filepath.ToSlash(rel), string(b))...)
	}
	if len(problems) > 0 {
		t.Errorf("有 %d 处「$VAR 紧跟多字节字符」（macOS bash 3.2 会当场 unbound variable，"+
			"而它偏偏只在本机段发作）：\n  %s\n修法：一律写 ${VAR}（本仓 2026-10-16 已全量收口，"+
			"新写的脚本照这条来）", len(problems), strings.Join(problems, "\n  "))
	}
	t.Logf("射程内 %d 份 .sh，多字节紧邻写法零命中", len(files))
}

// TestShellMultibyteVarGateCounterproof 反证：同一个判据函数喂合成件，五条腿缺一不可。
//
//	正腿（必须被抓）＝$X）／$1）——含位置参数形态，因为它与 $X） 是同一个 bash 解析路径；
//	负腿（必须放行）＝${X}）／单引号内／整行注释／后面是 ASCII。
//
// 若把规则改成"永远返回空"，正腿当场红；若改成"见到 $ 就报"，负腿当场红
// （两个方向都堵住，才不会出现"一条骗人的绿"）。
func TestShellMultibyteVarGateCounterproof(t *testing.T) {
	cases := []struct {
		name string
		line string
		want bool // true＝必须报
	}{
		{"正腿：变量名后紧跟中文", `echo "未知参数：$X（--apply）"`, true},
		{"正腿：名字含下划线再紧跟中文（$X_判绿 在 bash 3.2 实测 unbound）", `echo "a $X_判绿 b"`, true},
		{"正腿：位置参数后紧跟中文", `echo "失败码 $1）" >&2`, true},
		{"正腿：赋值语境同样要报", `code=$CODE）`, true},
		{"负腿：已加花括号（修法形态）", `echo "未知参数：${X}（--apply）"`, false},
		{"负腿：单引号内是字面量，改它等于改文案", `echo '未知参数：$X（--apply）'`, false},
		{"负腿：整行注释不喂给 bash", `# 历史上写过 $X）导致中断`, false},
		{"负腿：后面是 ASCII 空格", `echo "耗时 $1 秒"`, false},
		{"负腿：$1_判绿 位置参数只吃数字（实测正常，报它就是假红）", `expect "$1_判绿" "$out" "1"`, false},
		{"负腿：$? $# $( 这些不是变量名形态", `[ $? -eq 0 ] && echo "ok$#"`, false},
	}
	flagged := 0
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got := len(shellMultibyteVarProblems("合成件.sh", c.line+"\n")) > 0
			if got != c.want {
				t.Fatalf("合成件 %q：期望报=%v，实际报=%v ⇒ 这条腿的射程不对（正腿全绿＝闸门失明；负腿全红＝闸门误伤）",
					c.line, c.want, got)
			}
			if got {
				flagged++
			}
		})
	}
	if flagged == 0 {
		t.Fatalf("八条腿一条都没报 ⇒ 判据退化成恒返回空，绿灯是假的")
	}
}
