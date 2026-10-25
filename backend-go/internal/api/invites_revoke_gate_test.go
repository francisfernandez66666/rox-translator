// ============ invites_revoke_gate_test.go · 职责说明 ============
// G9（2026-10-19）建立、★ 2026-10-24 多身份 B3 重定靶：邀请码「状态判定只有一把尺子」的全集闸门。
//
// 判据本体没变，变的是**射程**：
//
//	旧形态要求 register.go 里每一处 `inv.Used == 0/1` 都同时判 `inv.Revoked`
//	（防止「撤销后注册口仍放行」——写侧加了撤销位、读侧忘了跟的典型形态）。
//	B3 §6.1 把注册口的凭据判定收进 register_identity.go，并改成**只读 store 派生的 Status**
//	（revoked > used > expired > active 的顺序只在 store.inviteStatusOf 一处），
//	于是 register.go 里的 `inv.Used` 直判应当一个都不剩。
//	如果只把这一条锁删掉，下一个人就会把「在 API 层再比一次 used/revoked」当成正常写法搬回来
//	——那正是 G9 当年要堵的洞，只是这次的形状是「第五把尺子」。
//
// 三段判据：
//
//	R1 负向（注册链零直判）：register.go 与 register_identity.go 剥注释后不得出现
//	   `.Used ==`／`.Revoked ==`／`.IsExpired()` 任何一处状态直判，也不得再直接比较 expires_at。
//	R2 正向（唯一尺子在位且被走到）：register_identity.go 必须按 `Status != "active"` 拒收；
//	   store/invites_revoke.go 的 inviteStatusOf 必须仍然先判 Revoked 再判 Used（顺序翻了＝撤销位失效）。
//	R3 射程自证 + 反证：喂一段「只判 Used==1 不判 Revoked」的样本，负向扫描必须点名它；
//	   喂一段「按 Status 判定」的合规样本，必须零命中（否则 R1 是恒红的瞎锁）。
//
// ⚠️ 剥注释与读文件都复用**包级共享助手**（upload_formats_gate_test.go 的 stripLineComments／readGateFile）：
//
//	本包里有十几条静态锁各写一份「读源码」和「剥注释」迟早打架，且共享那份的注释里写明了
//	「路径写错必须当场红、不许退成读不到所以没命中所以干净」。
//	stripLineComments 是**整行丢弃**注释行，所以行号会前移——R1 的报错因此点名**违规文本**而不是行号。
//
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestInviteRevokeGate
// =============================================
package api

import (
	"regexp"
	"strings"
	"testing"
)

// inviteStateDirectCheckRE 匹配「在 API 层直接判断码的状态位」的形态：
// 状态位直判（.Used==0/1、.Revoked==0/1）、到期直判（.IsExpired()、直接比较 ExpiresAt）。
var inviteStateDirectCheckRE = regexp.MustCompile(`\.(Used|Revoked)\s*==\s*[01]\b|\bIsExpired\s*\(\)|ExpiresAt\s*[!=]=`)

// inviteStatusReadRE 唯一尺子的消费形态：注册口按派生状态拒收。
var inviteStatusReadRE = regexp.MustCompile(`Status\s*!=\s*"active"`)

// inviteStateDirectChecks 返回 src 里「API 层直判码状态位」的违规行（已剥注释、按原文文本报出）。
// 纯函数，便于反证直接喂样本。
func inviteStateDirectChecks(src string) []string {
	var bad []string
	for _, line := range strings.Split(stripLineComments(src), "\n") {
		if inviteStateDirectCheckRE.MatchString(line) {
			bad = append(bad, strings.TrimSpace(line))
		}
	}
	return bad
}

// TestInviteRevokeGate 注册链不得自己判码的状态位，判定只经 store 派生的 Status。
func TestInviteRevokeGate(t *testing.T) {
	// R1 负向：两条注册链文件里零直判。
	for _, f := range []struct{ name, rel string }{
		{"register.go", "register.go"},
		{"register_identity.go", "register_identity.go"},
	} {
		if bad := inviteStateDirectChecks(readGateFile(t, f.rel)); len(bad) > 0 {
			t.Fatalf("%s 在 API 层直接判断了邀请码状态位（撤销/已用/到期必须只读 store.inviteStatusOf 派生的 Status，"+
				"两处各判迟早打架，且 B3 已把凭据判定收在 register_identity.go 一处）：%v", f.name, bad)
		}
	}

	// R2 正向：唯一尺子的消费点与本体都必须在位。
	if !inviteStatusReadRE.MatchString(stripLineComments(readGateFile(t, "register_identity.go"))) {
		t.Fatalf("register_identity.go 未出现按派生 Status 拒收的形态（Status != \"active\"）——凭据判定大概已被改回直判状态位")
	}
	storeSrc := readGateFile(t, "../store/invites_revoke.go")
	// 顺序判据：Revoked 分支必须排在 Used 分支之前（G9 的原意：撤销优先于已用）。
	ri := strings.Index(storeSrc, "Revoked == 1")
	ui := strings.Index(storeSrc, "Used == 1")
	if ri < 0 || ui < 0 {
		t.Fatalf("store.inviteStatusOf 里找不到 revoked/used 判定锚点（改名式破坏会让本闸失明）")
	}
	if ri > ui {
		t.Fatalf("store.inviteStatusOf 的判定顺序翻了（used 抢在 revoked 前）——已撤销的码会被说成『已使用』")
	}

	// R3 反证：违规样本必须被抓到；合规样本必须零命中。
	mutated := "if err != nil || inv.Used == 1 {\n\treject()\n}"
	if bad := inviteStateDirectChecks(mutated); len(bad) == 0 {
		t.Fatalf("反证失败：漏判 Revoked 的样本未被识别，闸门已退化成恒绿")
	}
	compliant := "if row.Status != \"active\" {\n\treturn nil, reject(row.Status)\n}"
	if bad := inviteStateDirectChecks(compliant); len(bad) != 0 {
		t.Fatalf("正对照失败：按派生 Status 判定的合规样本被误报为违规：%v", bad)
	}
}
