// ============ instant_free_gate_test.go · 职责说明 ============
// 组4 的判据锁（★ 2026-10-30 三事合一批）：
//
//	N3 尺寸/时长**先定档**：超硬顶 ⇒ 引导工单（哪怕额度也用尽，理由必须是尺寸而不是欠费）
//	N9 九键三态：`-1` 关闭 / `0` 不限 / `N` 上限
//	N1 五个入口都挂了免费判定（按 handler 全集数调用点，缺一个即红——P-1 主路径的机械防线）
//	N10 免费档九键不许经时间窗夹带（算子在同包 ops，逐键各验一次）
//	G12 语种分档：≥2 语种＝付费（不拒绝，只是不免费）
//
// 判据全部打**纯函数**（instantFreeEligible / ops.ValidateWindowOverrides）与**源码扫描**（N1），
// 不打 HTTP——HTTP 层的"免费不扣钱"由 ChargeUsageRealtime 单测与 UAT 主矩阵覆盖。
// =============================================
package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"translator/internal/ops"
)

// instCfg 出厂档（与 ops.DefaultEffective().Instant 同值；测试里显式写出来，改默认值必须同步这边）。
func instCfg() ops.InstantEffective {
	return ops.InstantEffective{
		FreeDailyPoints: 300, FreeTenantDailyPoints: 2000, FreeIPDaily: 300,
		FreeImageMaxMB: 1, FreeAudioMaxSec: 30,
		ImageMaxMB: 3, AudioMaxMB: 3, AudioMaxSec: 60,
		FreeLangLimit: 1,
	}
}

// N3：尺寸先定档——超硬顶的图 / 超 60 秒的音，理由必须是"引导工单"，不是"欠费/额度用尽"。
func TestInstantFree_N3SizeBeforeIdentity(t *testing.T) {
	cfg := instCfg()
	// 图 4 MiB、且暗额度也用尽：理由是 too_large + suggest，不是 quota_user
	r := instantFreeEligible(cfg, instantInput{Kind: "image", SizeBytes: 4 * instantMiB, LangCount: 1, FreeUser: 99999})
	if r.Free || r.Reason != instantReasonTooLarge || !r.SuggestTicket {
		t.Fatalf("N3：4 MiB 图必须先判尺寸（too_large + suggest），实得 %+v", r)
	}
	// 图 2 MiB（免费档外、付费档内）⇒ 转付费，不拒绝
	r = instantFreeEligible(cfg, instantInput{Kind: "image", SizeBytes: 2 * instantMiB, LangCount: 1})
	if r.Free || r.Reason != instantReasonTooLargePaid || r.SuggestTicket {
		t.Fatalf("N3：2 MiB 图应转付费档（too_large_paid），实得 %+v", r)
	}
	// 音频 70 秒 ⇒ 工单；45 秒 ⇒ 付费档；20 秒 ⇒ 免费
	if r = instantFreeEligible(cfg, instantInput{Kind: "audio", DurationSec: 70, SizeBytes: 100 * 1024, LangCount: 1}); r.Reason != instantReasonTooLong || !r.SuggestTicket {
		t.Fatalf("N3：70 秒音频应引导工单，实得 %+v", r)
	}
	if r = instantFreeEligible(cfg, instantInput{Kind: "audio", DurationSec: 45, SizeBytes: 100 * 1024, LangCount: 1}); r.Free || r.Reason != instantReasonTooLong || r.SuggestTicket {
		t.Fatalf("N3：45 秒音频应转付费档，实得 %+v", r)
	}
	if r = instantFreeEligible(cfg, instantInput{Kind: "audio", DurationSec: 20, SizeBytes: 100 * 1024, LangCount: 1}); !r.Free {
		t.Fatalf("N3：20 秒音频应在免费档，实得 %+v", r)
	}
}

// N9：九键三态（-1 关 / 0 不限 / N 上限）。
func TestInstantFree_N9ThreeStates(t *testing.T) {
	base := instCfg()
	// -1：免费档整体关闭（两条额度键任一为 -1 同效）
	off := base
	off.FreeDailyPoints = -1
	if r := instantFreeEligible(off, instantInput{Kind: "text", LangCount: 1}); r.Free || r.Reason != instantReasonFreeOff {
		t.Fatalf("N9：-1 必须关闭免费档，实得 %+v", r)
	}
	off2 := base
	off2.FreeIPDaily = -1
	if r := instantFreeEligible(off2, instantInput{Kind: "text", LangCount: 1}); r.Free || r.Reason != instantReasonFreeOff {
		t.Fatalf("N9：IP 键 -1 同样关闭免费档，实得 %+v", r)
	}
	// 0：不限（已用到很大也照免）
	unl := base
	unl.FreeDailyPoints = 0
	unl.FreeIPDaily = 0
	unl.FreeTenantDailyPoints = 0
	if r := instantFreeEligible(unl, instantInput{Kind: "text", LangCount: 1, FreeUser: 1 << 40, FreeIP: 1 << 40, FreeTenant: 1 << 40, EstPoints: 1000}); !r.Free {
		t.Fatalf("N9：0＝不限，必须照免，实得 %+v", r)
	}
	// N：到量就转付费
	lim := base
	lim.FreeDailyPoints = 100
	if r := instantFreeEligible(lim, instantInput{Kind: "text", LangCount: 1, FreeUser: 90, EstPoints: 11}); r.Free || r.Reason != instantReasonQuotaUser {
		t.Fatalf("N9：账号暗额度超限必须转付费，实得 %+v", r)
	}
	limIP := base
	limIP.FreeIPDaily = 5
	if r := instantFreeEligible(limIP, instantInput{Kind: "text", LangCount: 1, FreeIP: 5}); r.Free || r.Reason != instantReasonQuotaIP {
		t.Fatalf("N9：IP 次数到量必须转付费，实得 %+v", r)
	}
	limTenant := base
	limTenant.FreeTenantDailyPoints = 2000
	if r := instantFreeEligible(limTenant, instantInput{Kind: "text", LangCount: 1, FreeTenant: 2000, EstPoints: 1}); r.Free || r.Reason != instantReasonQuotaTenant {
		t.Fatalf("N9：租户池到量必须转付费，实得 %+v", r)
	}
}

// G12：多语种＝付费权益（不拒绝）。
func TestInstantFree_LangLimitIsPaidNotRejected(t *testing.T) {
	r := instantFreeEligible(instCfg(), instantInput{Kind: "text", LangCount: 2})
	if r.Free || r.Reason != instantReasonLangLimit || r.SuggestTicket {
		t.Fatalf("G12：2 个语种应转付费档（不是拒绝、不是引导工单），实得 %+v", r)
	}
	if r := instantFreeEligible(instCfg(), instantInput{Kind: "text", LangCount: 1}); !r.Free {
		t.Fatalf("G12：1 个语种应免费，实得 %+v", r)
	}
}

// N10：免费档九键逐键都不许经时间窗夹带（每键各喂一次，改哪一项都必须被拦）。
func TestInstantFree_N10WindowOverrideRejectedPerKey(t *testing.T) {
	one := int64(1)
	keys := map[string]func(p *ops.InstantPatch){
		"free_daily_points":        func(p *ops.InstantPatch) { p.FreeDailyPoints = &one },
		"free_tenant_daily_points": func(p *ops.InstantPatch) { p.FreeTenantDailyPoints = &one },
		"free_ip_daily":            func(p *ops.InstantPatch) { p.FreeIPDaily = &one },
		"free_image_max_mb":        func(p *ops.InstantPatch) { p.FreeImageMaxMB = &one },
		"free_audio_max_sec":       func(p *ops.InstantPatch) { p.FreeAudioMaxSec = &one },
		"image_max_mb":             func(p *ops.InstantPatch) { p.ImageMaxMB = &one },
		"audio_max_mb":             func(p *ops.InstantPatch) { p.AudioMaxMB = &one },
		"audio_max_sec":            func(p *ops.InstantPatch) { p.AudioMaxSec = &one },
		"free_lang_limit":          func(p *ops.InstantPatch) { p.FreeLangLimit = &one },
	}
	for name, set := range keys {
		w := ops.PromoWindow{ID: "w1"}
		set(&w.Overrides.Instant)
		if err := ops.ValidateWindowOverrides(w); err == nil {
			t.Fatalf("N10：%s 经时间窗夹带必须被拒（一次促销改掉成本顶，现场指不到）", name)
		}
	}
	// 正向对照：不带 instant 的窗口必须放行（否则这条锁会把所有窗口一起打死）
	if err := ops.ValidateWindowOverrides(ops.PromoWindow{ID: "w2"}); err != nil {
		t.Fatalf("N10 正向对照：普通窗口必须放行，实得 %v", err)
	}
}

// N1：五个入口都挂了免费判定（按 handler 全集数调用点）。
// 判据按 AST 数：handleChatStream / handleChat / handleChatImage / handleChatASR / handleChatTTS
// 五个函数体里必须各自出现至少一次 instantDecide( 或 gateUsageInstant(。
func TestInstantFree_N1AllFiveEntriesWired(t *testing.T) {
	got := map[string]bool{}
	fset := token.NewFileSet()
	for _, file := range []string{"stream.go", "chat_image.go", "chat_audio.go"} {
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", file, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				return true
			}
			ast.Inspect(fn.Body, func(inner ast.Node) bool {
				call, ok := inner.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if sel.Sel.Name == "instantDecide" || sel.Sel.Name == "gateUsageInstant" {
					got[fn.Name.Name] = true
				}
				return true
			})
			return true
		})
	}
	for _, want := range []string{"handleChatStream", "handleChat", "handleChatImage", "handleChatASR", "handleChatTTS"} {
		if !got[want] {
			t.Fatalf("N1：%s 没有挂免费判定（P-1 的机械防线：只挂 /api/chat 会让主路径照旧扣钱）", want)
		}
	}
}

// 文件存在性自证：上面按文件名解析，文件改名会让 N1 静默失明。
func TestInstantFree_N1SourceFilesExist(t *testing.T) {
	for _, f := range []string{"stream.go", "chat_image.go", "chat_audio.go", "instant_free.go"} {
		if _, err := os.Stat(filepath.Join(".", f)); err != nil {
			t.Fatalf("N1 的判据源文件不在了：%s（闸门已失明）", f)
		}
	}
	if !strings.Contains(instantReasonUseTicket, "ticket") {
		t.Fatal("原因码契约漂了：use_ticket 字面必须含 ticket（前端按它出引导卡）")
	}
}
