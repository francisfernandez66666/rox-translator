// ============ instant_caps_face_test.go · 职责说明 ============
// G5／G6／G18 的**对外面**锁（★ 2026-10-31 断言缺口补齐第二轮）。
//
// 这三条原来只有"纯函数那一半"（`instant_free_gate_test.go` 的 N9／G23 打的是
// `instantFreeEligible` 与 `ops.ValidateWindowOverrides`），**保存面与出栈面一条都没有**：
//
//	G6  保存面：`<-1` 必须 400 且**库里一字不动**；`-1/0/N` 必须存得下；
//	    存下之后**当刻生效**（不重启、不换二进制，同一个 Server 实例的下一个请求就得读到新档）。
//	    这一条原来无人看：去掉 `instantFreeValidate` 那次调用在纯函数锁上照样全绿，
//	    而线上表现是"运营把免费档调成比付费档还宽，保存成功、界面照常，客户升级反被拒"。
//	G18 出栈面：`GET /api/instant/caps` **只出宽度、不出暗额度**——键与数字都不许出现
//	    （原来只有 UAT 脚本的 `ckn T77-caps-no-quota-keys` 一条 shell 判据在守，Go 侧对本 handler 零覆盖）。
//	G5  状态词：`free_daily_points=-1` ⇒ `free_on=false`（对外只有一个状态词，没有数字）。
//
// 判据取向：期望值一律**现读现推**（用一组互不相同的非默认数字），禁止写死"1/3/30/60/40"——
// 写死的那一份只能证明出厂默认还在，证明不了"配置改了跟得上"（AGENTS §一·3 那条尺子）。
//
// 方言：复用 `newPlatformCostProbe`（自钉 SQLite 内存库＋钉死 config.C，AGENTS §一·4）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestInstantCapsFace
// =============================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"translator/internal/ops"
)

// capsProbeSave 走**真实保存面**（handleOpsPolicySave＋超管 token），不是直接 SetConfig——
// 直接写库测不到"校验在保存链路上的哪一步"，而 G6 要判的就是那一步。
func capsProbeSave(t *testing.T, p *platformCostProbe, patch ops.OperationsPolicy) (int, string) {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{"scope": "platform", "policy": patch})
	if err != nil {
		t.Fatalf("序列化策略失败: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/admin/ops/policy/save", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+p.superTok)
	rec := httptest.NewRecorder()
	p.s.handleOpsPolicySave(rec, r)
	return rec.Code, rec.Body.String()
}

// capsProbeGet 匿名打公开档位面（这个接口本来就是匿名可达的）。
func capsProbeGet(t *testing.T, p *platformCostProbe) (int, map[string]interface{}, string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/instant/caps", nil)
	rec := httptest.NewRecorder()
	p.s.handleInstantCaps(rec, r)
	var out map[string]interface{}
	raw := rec.Body.String()
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("caps 响应不是合法 JSON（%d）: %v / body=%s", rec.Code, err, raw)
	}
	return rec.Code, out, raw
}

func iptr(v int64) *int64 { return &v }

// TestInstantCapsFace_G6SaveValidationAndLiveEffect G6：脏值拒收且库不动；合法值当刻生效。
func TestInstantCapsFace_G6SaveValidationAndLiveEffect(t *testing.T) {
	p := newPlatformCostProbe(t)

	// 先存一组**合法且互不相同**的非默认数字（每个数字都只属于一个键，泄漏才能被逐键指认）
	legal := ops.OperationsPolicy{Instant: ops.InstantPatch{
		FreeDailyPoints:       iptr(3141),
		FreeIPDaily:           iptr(2718),
		FreeTenantDailyPoints: iptr(1618),
		FreeImageMaxMB:        iptr(2),
		FreeAudioMaxSec:       iptr(45),
		FreeLangLimit:         iptr(1),
		ImageMaxMB:            iptr(7),
		AudioMaxMB:            iptr(8),
		AudioMaxSec:           iptr(99),
	}}
	if code, body := capsProbeSave(t, p, legal); code != 200 {
		t.Fatalf("G6：合法九键（-1/0/N 档内的正整数）保存被拒（%d）：%s", code, body)
	}
	// **当刻生效**：同一个 Server 实例、不重启，公开面必须立刻读到新档
	code, caps, _ := capsProbeGet(t, p)
	if code != 200 {
		t.Fatalf("G6：保存成功后 caps 接口退 %d", code)
	}
	for key, want := range map[string]int64{
		"free_image_max_mb": 2, "free_audio_max_sec": 45, "image_max_mb": 7,
		"audio_max_mb": 8, "audio_max_sec": 99, "free_lang_limit": 1,
	} {
		if got, okn := caps[key]; !okn {
			t.Fatalf("G6 正向对照：公开档位键 %s 根本不出栈（客户拿不到宽度）", key)
		} else if int64(toFloat(got)) != want {
			t.Fatalf("G6：保存后 caps 的 %s 实得 %v、应为 %d ⇒ 进程侧没跟上库（缓存/热加载那一层断了）", key, got, want)
		}
	}

	// 脏值：任一键 `< -1` ⇒ 400 **且库里一字不动**（半套参数不许落）
	before, err := p.s.Store.GetConfig("ops_policy")
	if err != nil {
		t.Fatalf("读 ops_policy 失败: %v", err)
	}
	dirty := legal
	dirty.Instant.FreeDailyPoints = iptr(-2)
	if code, body := capsProbeSave(t, p, dirty); code != http.StatusBadRequest {
		t.Fatalf("G6：free_daily_points=-2 必须 400，实得 %d（body=%s）", code, body)
	}
	after, err := p.s.Store.GetConfig("ops_policy")
	if err != nil {
		t.Fatalf("读 ops_policy 失败: %v", err)
	}
	if after != before {
		t.Fatalf("G6：400 之后库里却被写了（写侧顺序错了——先碰库/进程侧再校验，正是 AGENTS §一·3 禁止的形态）")
	}
	// 跨键倒挂也要拒（免费比付费还宽＝客户升级反被拒）
	inverted := legal
	inverted.Instant.FreeImageMaxMB = iptr(9) // > image_max_mb(7)
	if code, body := capsProbeSave(t, p, inverted); code != http.StatusBadRequest {
		t.Fatalf("G6：免费档(9 MB) 宽于付费档(7 MB) 必须 400，实得 %d（body=%s）", code, body)
	}
	// 三态都能存：-1（关闭）／0（不限）／N（上限）
	for _, v := range []int64{-1, 0, 500} {
		tri := legal
		tri.Instant.FreeDailyPoints = iptr(v)
		if code, body := capsProbeSave(t, p, tri); code != 200 {
			t.Fatalf("G6：%d 这一档存不下（%d）：%s", v, code, body)
		}
	}
}

// TestInstantCapsFace_G18NeverLeaksDarkQuota G18：出栈面零暗额度键与数字；正向对照宽度与状态词在位。
func TestInstantCapsFace_G18NeverLeaksDarkQuota(t *testing.T) {
	p := newPlatformCostProbe(t)
	patch := ops.OperationsPolicy{Instant: ops.InstantPatch{
		FreeDailyPoints:       iptr(3141),
		FreeIPDaily:           iptr(2718),
		FreeTenantDailyPoints: iptr(1618),
		FreeImageMaxMB:        iptr(2),
		FreeAudioMaxSec:       iptr(45),
		FreeLangLimit:         iptr(1),
		ImageMaxMB:            iptr(7),
		AudioMaxMB:            iptr(8),
		AudioMaxSec:           iptr(99),
	}}
	if code, body := capsProbeSave(t, p, patch); code != 200 {
		t.Fatalf("前置保存失败（%d）：%s", code, body)
	}
	code, caps, raw := capsProbeGet(t, p)
	if code != 200 {
		t.Fatalf("G18：caps 退 %d", code)
	}
	// ① 三个暗额度键**与它们的数字**一个都不许出现（数字一旦出栈，薅羊毛的算力立刻排在这条链上）
	for _, bad := range []string{"free_daily_points", "free_ip_daily", "free_tenant_daily_points"} {
		if _, okk := caps[bad]; okk {
			t.Fatalf("G18：暗额度键 %s 出栈了：%s", bad, raw)
		}
	}
	for _, n := range []string{"3141", "2718", "1618"} {
		if strings.Contains(raw, n) {
			t.Fatalf("G18：响应体里出现了暗额度数字 %s：%s", n, raw)
		}
	}
	// ② 正向对照（没有这条，上面那三条负向是永真空转）：宽度与状态词必须在
	for _, want := range []string{"free_image_max_mb", "free_audio_max_sec", "image_max_mb", "audio_max_mb", "audio_max_sec", "free_lang_limit", "ticket_max_mb", "free_on"} {
		if _, okk := caps[want]; !okk {
			t.Fatalf("G18 正向对照：公开字段 %s 缺席（实得 %s）⇒ 负向腿此刻在替一个坏实现说话", want, raw)
		}
	}
	// ③ 方法闸门：POST 必须 405（与其余公开面同口径）
	r := httptest.NewRequest(http.MethodPost, "/api/instant/caps", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	p.s.handleInstantCaps(rec, r)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("G18：caps 用 POST 访问应 405，实得 %d", rec.Code)
	}
}

// TestInstantCapsFace_G5FreeOffIsAStateWord G5：`-1` ⇒ 对外只有状态词 `free_on=false`，零数字。
func TestInstantCapsFace_G5FreeOffIsAStateWord(t *testing.T) {
	p := newPlatformCostProbe(t)
	patch := ops.OperationsPolicy{Instant: ops.InstantPatch{
		FreeDailyPoints:       iptr(-1),
		FreeIPDaily:           iptr(2718),
		FreeTenantDailyPoints: iptr(1618),
		ImageMaxMB:            iptr(7),
		AudioMaxMB:            iptr(8),
		AudioMaxSec:           iptr(99),
		FreeImageMaxMB:        iptr(2),
		FreeAudioMaxSec:       iptr(45),
		FreeLangLimit:         iptr(1),
	}}
	if code, body := capsProbeSave(t, p, patch); code != 200 {
		t.Fatalf("前置保存失败（%d）：%s", code, body)
	}
	code, caps, raw := capsProbeGet(t, p)
	if code != 200 {
		t.Fatalf("G5：caps 退 %d", code)
	}
	if on, _ := caps["free_on"].(bool); on {
		t.Fatalf("G5：free_daily_points=-1（关闭）时 free_on 仍为 true（读数与事实相反）：%s", raw)
	}
	// 关闭态也不许把任何暗额度数字念出来
	for _, n := range []string{"2718", "1618"} {
		if strings.Contains(raw, n) {
			t.Fatalf("G5：关闭态响应里出现了额度数字 %s：%s", n, raw)
		}
	}
	// 状态词只有布尔那一颗，**没有**"剩余 N"这种存量字段
	if strings.Contains(raw, "remain") || strings.Contains(raw, "left") {
		t.Fatalf("G5：caps 里长出了存量字段（对外只出宽度与状态词）：%s", raw)
	}
}

// toFloat 把 JSON 数字（float64）转 int64（判据只问等值，不做任何换算）。
func toFloat(v interface{}) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return -1
}
