// ============ admin_authz_scale_gate_test.go · 职责说明 ============
// G7／G16／G19 家族级静态闸：**"是不是超管"只有 `auth.IsSuperAdmin` 一把尺子**（AGENTS §一·15）。
//
// 缺陷形态（同一族的两个方向）：
//
//	· G7／G19：吃角色字符串 `u.Role == "super_admin"` ⇒ 现网 role='admin' 的平台账号漏判；
//	· G16：吃等级 `auth.RoleLevel(u.Role) >= 4`（不含 tenant_id 口径）⇒ role='admin'+tenant>0 的
//	  存量账号可写任意租户品牌（越权）。
//
// 判据只抓**调用方权限判定**两种形态，不误伤合法用途（AGENTS §一·14：判红一次的代价是闸门被人摘掉）：
//
//	· `.Role == "<非空>"` / `.Role != "<非空>"`（空串校验如 `req.Role == ""` 是输入校验，放行）；
//	· `auth.RoleLevel(u.Role)` 与 3/4 比较（对**当前用户** u 判超管档）；
//	· `auth.RoleLevel(<其它>)`（校验请求/导入行的目标角色）与 `RoleLevel(u.Role) < 2`（租管档）
//	  是另外两回事，**刻意放行**——射程收窄到"u 的超管档 + 角色字符串字面量"这一族。
//
// 射程自证：全包 `auth.IsSuperAdmin(` 正锚计数 ≥ 20（锚点漂了闸门会失明地变绿）。
// 反证：造一处 `u.Role == "super_admin"`、一处 `RoleLevel(u.Role) >= 4`，本闸必须红。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestAdminAuthzScaleGate
// =============================================
package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

var (
	// 角色字符串比较（非空字面量）——如 `u.Role == "super_admin"`。
	authzRoleLiteralRE = regexp.MustCompile(`\.Role\s*(?:==|!=)\s*"([^"]+)"`)
	// 对当前用户判超管档的等级比较——如 `auth.RoleLevel(u.Role) >= 4` / `< 4`（等价写法 >3 / <=3）。
	// 只抓**超管档**（4）：`>= 3` / `< 2` 是租管/部门档，属另外两回事，刻意放行。
	authzLevel4RE = regexp.MustCompile(`RoleLevel\(u\.Role\)\s*(?:>=\s*4|>\s*3|<\s*4|<=\s*3)\b`)
	// 正锚：全站唯一那把尺子。
	authzIsSuperAdminRE = regexp.MustCompile(`auth\.IsSuperAdmin\(`)
)

// authzLiteralOffenders 返回剥注释后仍出现角色字符串字面量比较、或对 u 判超管档等级比较的行。
// 参数 src: 单个 .go 源文件原文。返回: "行号: 内容" 形式的违规行列表。
func authzLiteralOffenders(src string) []string {
	code := stripCodeComments(src)
	var out []string
	for _, ln := range strings.Split(code, "\n") {
		if authzRoleLiteralRE.MatchString(ln) || authzLevel4RE.MatchString(ln) {
			out = append(out, "行: "+strings.TrimSpace(ln))
		}
	}
	return out
}

// TestAdminAuthzScaleGate 家族级静态闸：internal/api 非测试 .go 不许出现角色字面量/超管档等级比较。
func TestAdminAuthzScaleGate(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取包目录失败：%v", err)
	}
	files, anchor := 0, 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files++
		b, rerr := os.ReadFile(name)
		if rerr != nil {
			t.Fatalf("读取 %s 失败：%v", name, rerr)
		}
		code := stripCodeComments(string(b))
		anchor += len(authzIsSuperAdminRE.FindAllString(code, -1))
		if off := authzLiteralOffenders(string(b)); len(off) > 0 {
			t.Errorf("%s 仍用字面量/等级判超管（应改 auth.IsSuperAdmin）：%v", name, off)
		}
	}
	if files < 80 {
		t.Fatalf("射程自证失败：只扫到 %d 个非测试 .go（<80）", files)
	}
	if anchor < 20 {
		t.Fatalf("射程自证失败：全包 auth.IsSuperAdmin 正锚计数 %d < 20（锚点漂了闸门已失明）", anchor)
	}
}

// TestAdminAuthzScaleGateCatchesLiterals 反证：两种违规形态都必被抓到，合法用途不误伤。
func TestAdminAuthzScaleGateCatchesLiterals(t *testing.T) {
	bad := []string{
		`if u.Role == "super_admin" {`,
		`if u.Role != "super_admin" {`,
		`if auth.RoleLevel(u.Role) >= 4 {`,
		`if auth.RoleLevel(u.Role) < 4 && tid != u.TenantID {`,
	}
	for _, s := range bad {
		if len(authzLiteralOffenders(s+"\n")) == 0 {
			t.Errorf("反证失效：%q 未被家族闸抓到", s)
		}
	}
	good := []string{
		`if !auth.IsSuperAdmin(u) {`,
		`if req.Role == "" {`,
		`if auth.RoleLevel(u.Role) < 2 {`,
		`if auth.RoleLevel(u.Role) >= 3 {`,
		`if auth.RoleLevel(req.Role) >= 4 && !auth.IsSuperAdmin(u) {`,
		`if auth.RoleLevel(row.Role) >= 4 && tid > 0 {`,
	}
	for _, s := range good {
		if off := authzLiteralOffenders(s + "\n"); len(off) != 0 {
			t.Errorf("误伤合法用途 %q：%v", s, off)
		}
	}
	// 注释里的旧写法不算违规（剥注释后判）。
	commented := "// 旧写法 u.Role == \"super_admin\" 已改\nfunc f() {}\n"
	if off := authzLiteralOffenders(commented); len(off) != 0 {
		t.Errorf("注释里的旧写法被误判：%v", off)
	}
}
