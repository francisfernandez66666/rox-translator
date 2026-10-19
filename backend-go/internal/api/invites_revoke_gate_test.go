// ============ invites_revoke_gate_test.go · 职责说明 ============
// G9（2026-10-19）：邀请码「撤销位」的全集闸门。
//
// 判据形态（同一族口径，见 admin_authz_scale_gate_test.go）：静态扫描 register.go，
// 凡出现对邀请码使用状态的判定（`inv.Used == 0` / `inv.Used == 1`），同一行必须同时出现
// `inv.Revoked`——否则「撤销后注册口拒收」这条链会在某处被绕过（改了写侧忘了读侧的典型形态）。
//
// 射程自证：命中数必须 ≥3（正锚在位，防「锚点漂了、整段静默失明」）。
// 反证：喂一段只判 Used==1 而不判 Revoked 的源码，判据函数必须判它为违规。
//
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestInviteRevokeGate
// =============================================
package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// inviteUsedCheckRE 匹配「对邀请码使用状态的判定」行（Used==0 或 Used==1）。
var inviteUsedCheckRE = regexp.MustCompile(`\binv\.Used\s*==\s*[01]\b`)

// inviteRevokedGuardMissing 返回 src 中「判了 Used 却没判 Revoked」的行号（1 基）。
// 纯函数，便于反证直接喂样本。
func inviteRevokedGuardMissing(src string) []int {
	var bad []int
	for i, line := range strings.Split(src, "\n") {
		if !inviteUsedCheckRE.MatchString(line) {
			continue
		}
		if !strings.Contains(line, "inv.Revoked") {
			bad = append(bad, i+1)
		}
	}
	return bad
}

// TestInviteRevokeGate register.go 里每处 Used 判定都必须同时判 Revoked。
func TestInviteRevokeGate(t *testing.T) {
	srcB, err := os.ReadFile("register.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(srcB)

	// 射程自证：正锚（Used 判定）至少 3 处，否则说明扫描口径漂了。
	hits := 0
	for _, line := range strings.Split(src, "\n") {
		if inviteUsedCheckRE.MatchString(line) {
			hits++
		}
	}
	if hits < 3 {
		t.Fatalf("邀请码 Used 判定仅命中 %d 处（<3），闸门射程疑似退化", hits)
	}

	if bad := inviteRevokedGuardMissing(src); len(bad) > 0 {
		t.Fatalf("register.go 以下行判了邀请码 Used 却漏判 Revoked（撤销后仍可能放行）：%v", bad)
	}

	// 反证：只判 Used==1 不判 Revoked 的源码必须被判违规。
	mutated := `
	if err != nil || inv.Used == 1 {
		reject()
	}`
	if bad := inviteRevokedGuardMissing(mutated); len(bad) == 0 {
		t.Fatalf("反证失败：漏判 Revoked 的样本未被识别，闸门可能退化成恒绿")
	}
	// 正对照：补上 Revoked 后不再命中。
	ok := `if err != nil || inv.Used == 1 || inv.Revoked == 1 { reject() }`
	if bad := inviteRevokedGuardMissing(ok); len(bad) != 0 {
		t.Fatalf("正对照失败：已判 Revoked 的行被误报为违规：%v", bad)
	}
}
