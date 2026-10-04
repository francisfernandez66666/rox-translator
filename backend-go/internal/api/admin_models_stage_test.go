// ============================================================================
// admin_models_stage_test.go — ★ R-1 修法 D 的档名事实源与保存语义断言（A5，2026-10-04）。
//
// 钉住的历史缺陷（三条腿，全都属于「面板上是绿的、库里其实坏了」这一族）：
//
//	① 读面手抄五项、漏 kb_match，而引擎确实拿 kb_match 取模
//	   （orchestrator/workflow.go、engine/file.go、engine/text.go）
//	   ⇒ 运营**看不见也配不了**这一档；
//	② 写面对名单外的键照单全收 ⇒ 阶段名拼错（历史上的 ai_initila / evals 旧键）
//	   会留下一条永不生效的配置，面板还显示「已保存」；
//	③ 写面是**整表替换** ⇒ 客户端只要少渲染一张卡（①里的 kb_match、以及旧键 evals），
//	   保存一次就把那一档的真实配置从库里抹掉，且事后看不出发生过什么。
//
// 修法对应的三条口径，本文件逐条钉死：
//
//	A5-1 读面返回的档名集合必须**逐字等于 config.AllStages() 派生集**（派生式，不钉数量）；
//	A5-2 名单外的键 400 拒收，且库里一个字节都不许变；
//	A5-3 未提交的键保留旧值（含密钥不被抹、不被重复加密），显式提交空值才是清空。
//
// 反证（已在 /tmp 副本跑过，见批次记录）：
//
//	· 把读面循环改回手抄五项 ⇒ A5-1 红（kb_screen/kb_match 少一档）；
//	· 删掉 allowed 判定 ⇒ A5-2 红（拼错键 200 落库）；
//	· 把 stored 的"保留未提交键"两段删掉回到整表替换 ⇒ A5-3 红（kb_match 被抹）；
//	· 把 submitted 的采集挪到校验循环之后 ⇒ A5-3 的清空子案红（被清空的档当成"没提交"而复活）。
//
// 方言口径（AGENTS §一·4）：本测试族固定内存 SQLite，由 newModelsSaveServer 钉好并还原。
// ============================================================================
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/store"
)

// stageSnapshot 读库里 stage_models 的**原始**落库形态（密文态），用于验加密链。
func stageSnapshot(t *testing.T, st *store.Store) config.StageModels {
	t.Helper()
	v, err := st.GetConfig("stage_models")
	if err != nil {
		t.Fatalf("读 stage_models 失败: %v", err)
	}
	m := config.StageModels{}
	if v == "" {
		return m
	}
	if err := json.Unmarshal([]byte(v), &m); err != nil {
		t.Fatalf("stage_models 不是合法 JSON: %v（原文 %q）", err, v)
	}
	return m
}

// postStages 以超管身份提交一次阶段配置保存，返回状态码与响应体。
func postStages(t *testing.T, s *Server, token string, stages map[string]interface{}) (int, map[string]interface{}) {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{"stages": stages})
	if err != nil {
		t.Fatalf("序列化请求失败: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/models/stage/save", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.handleStageModelsSave(rec, req)
	out := map[string]interface{}{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// getStages 以超管身份读一次阶段配置，返回 stages 字段的键集合。
func getStages(t *testing.T, s *Server, token string) []string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/models/stage", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.handleStageModels(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("读阶段配置应 200，实际 %d：%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Success bool                       `json:"success"`
		Stages  map[string]json.RawMessage `json:"stages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析响应失败: %v（原文 %s）", err, rec.Body.String())
	}
	keys := make([]string, 0, len(payload.Stages))
	for k := range payload.Stages {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// A5-1（文档里记作 TestStageWhitelistCoversAllRuntimeStages）：读面档位集合必须逐字等于
// config.AllStages() 派生集，且 kb_match／kb_screen 这类"引擎在用、面板当年看不见"的档必须在内。
// 判据写成「集合相等」而不是「至少 N 项」：这样将来往名单里加一档、
// 而读面没跟上（正是 kb_match 当年的形态）会当场红，反之名单腐烂也红。
func TestStageWhitelistCoversAllRuntimeStages(t *testing.T) {
	s, token := newModelsSaveServer(t)

	want := append([]string{}, config.AllStages()...)
	sort.Strings(want)
	got := getStages(t, s, token)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("读面档位集合 %v ≠ config.AllStages() 派生集 %v ⇒ 有一面在抄第二份名单", got, want)
	}
	// 决定性正向：引擎真在用的两档必须在名单里（历史上的黑洞就是这两档）
	for _, must := range []string{config.StageKBMatch, config.StageKBScreen} {
		if !containsString(got, must) {
			t.Fatalf("档位 %s 没出现在读面 ⇒ 运营看不见它，而引擎拿它取模；配合整表替换的旧写面就是「每保存一次删掉一档」", must)
		}
	}
	// 空库也必须每档都回一条空档：前端要靠这份列表渲染卡片，缺项＝那张卡长不出来
	if len(got) != len(config.AllStages()) {
		t.Fatalf("空库下读面应补齐全部档位，实际 %d 条对 %d 条", len(got), len(config.AllStages()))
	}
}

// containsString 线性判存在（本文件只用一次，不为它引额外包）。
func containsString(hay []string, needle string) bool {
	for _, v := range hay {
		if v == needle {
			return true
		}
	}
	return false
}

// A5-2：名单外的键一律 400，且库里字节不变。
// 钉的是「拼错的阶段名静静落库」这一形态：写进去的那条永不生效，
// 运营在面板上看到"保存成功"，真实路由却一直在打全局默认端点。
func TestStageSaveRejectsUnknownStageKey(t *testing.T) {
	s, token := newModelsSaveServer(t)

	// 先落一份合法基线，再拿"改前快照"和拼错键的提交做逐字节对比
	code, _ := postStages(t, s, token, map[string]interface{}{
		config.StageAIInitial: map[string]interface{}{
			"api_base": "https://initial.example/v1", "api_key": "sk-initial-key", "model": "initial/Model",
		},
	})
	if code != http.StatusOK {
		t.Fatalf("基线保存应 200，实际 %d", code)
	}
	beforeRaw, _ := s.Store.GetConfig("stage_models")

	// ai_initila＝当年真实的拼错形态（少一个 l）；另试一条完全无关的键
	for _, badKey := range []string{"ai_initila", "not_a_stage"} {
		code, body := postStages(t, s, token, map[string]interface{}{
			badKey: map[string]interface{}{
				"api_base": "https://typo.example/v1", "api_key": "sk-typo", "model": "typo/Model",
			},
		})
		if code != http.StatusBadRequest {
			t.Fatalf("名单外的键 %s 应 400 拒收，实际 %d（body=%v）⇒ 又回到「原样落库成死配置」的旧形态", badKey, code, body)
		}
		msg, _ := body["message"].(string)
		if !strings.Contains(msg, "未知流程阶段") {
			t.Fatalf("400 文案要点名是哪个档拼错了，实际 %q", msg)
		}
	}
	afterRaw, _ := s.Store.GetConfig("stage_models")
	if afterRaw != beforeRaw {
		t.Fatalf("拒收必须「一个字节都不写」，实际库里已变：\n改前 %s\n改后 %s", beforeRaw, afterRaw)
	}
}

// A5-3：只合并"本次提交过的键"，未提交的档保留旧值；显式空值才算清空。
// 这是整表替换那一条的正面锁——旧形态下 kb_match 的遭遇（面板没渲染⇒每保存一次被抹一次）
// 现在必须在同一个用例里可见地「活下来」。
func TestStageSaveMergesOnlySubmittedKeys(t *testing.T) {
	s, token := newModelsSaveServer(t)

	// 建立两档真实配置：ai_initial（本次会被再次提交）＋ kb_match（本次**不提交**，代表旧版少渲染的那张卡）
	code, _ := postStages(t, s, token, map[string]interface{}{
		config.StageAIInitial: map[string]interface{}{
			"api_base": "https://initial.example/v1", "api_key": "sk-initial-key", "model": "initial/Model",
		},
		config.StageKBMatch: map[string]interface{}{
			"api_base": "https://kb.example/v1", "api_key": "sk-kb-secret-key", "model": "kb/Model",
		},
	})
	if code != http.StatusOK {
		t.Fatalf("建立基线应 200，实际 %d", code)
	}
	snap := stageSnapshot(t, s.Store)
	if got, err := decodeStageKey(t, snap, config.StageKBMatch); err != nil {
		t.Fatalf("基线 kb_match 密钥异常: %v", err)
	} else if got != "sk-kb-secret-key" {
		t.Fatalf("基线 kb_match 密钥应回明文，实际 %q", got)
	}
	// 密文态正向对照：库里存的必须是 enc:v1: 前缀，不是明文、也不是二次加密
	if raw := snap[config.StageKBMatch].APIKey; !strings.HasPrefix(raw, store.SecretEncPrefix) {
		t.Fatalf("阶段密钥必须密文落库（前缀 %s），实际 %q", store.SecretEncPrefix, raw)
	}

	// 只提交 ai_initial（改 model），kb_match 不在请求里 ⇒ 必须原样保留
	code, _ = postStages(t, s, token, map[string]interface{}{
		config.StageAIInitial: map[string]interface{}{
			"api_base": "https://initial.example/v1", "api_key": "sk-initial-key", "model": "initial/Model-v2",
		},
	})
	if code != http.StatusOK {
		t.Fatalf("单档保存应 200，实际 %d", code)
	}
	snap = stageSnapshot(t, s.Store)
	kb, ok := snap[config.StageKBMatch]
	if !ok {
		t.Fatalf("kb_match 被整表替换抹掉了 ⇒ 未提交的键必须保留旧值（修法 D 的核心一条）")
	}
	if kb.APIBase != "https://kb.example/v1" || kb.Model != "kb/Model" {
		t.Fatalf("kb_match 旧值被改写：base=%s model=%s", kb.APIBase, kb.Model)
	}
	if got, err := decodeStageKey(t, snap, config.StageKBMatch); err != nil || got != "sk-kb-secret-key" {
		t.Fatalf("kb_match 密钥没保留（或被重复加密）：got=%q err=%v raw=%q", got, err, kb.APIKey)
	}
	if snap[config.StageAIInitial].Model != "initial/Model-v2" {
		t.Fatalf("提交的档位没覆盖成功：model=%s", snap[config.StageAIInitial].Model)
	}

	// 显式提交空值＝清空该档（语义没有变松：清空只走这条正路）
	code, _ = postStages(t, s, token, map[string]interface{}{
		config.StageKBMatch: map[string]interface{}{"api_base": "", "model": ""},
	})
	if code != http.StatusOK {
		t.Fatalf("清空提交应 200，实际 %d", code)
	}
	snap = stageSnapshot(t, s.Store)
	if _, ok := snap[config.StageKBMatch]; ok {
		t.Fatalf("显式空值必须删掉该档（否则「清空」在面板上点了没用、库里还在打旧端点）")
	}
	// 而这次提交同样没带 ai_initial ⇒ 它还得活着
	if _, ok := snap[config.StageAIInitial]; !ok {
		t.Fatalf("清空 kb_match 时把没提交的 ai_initial 一起抹了 ⇒ 合并语义退化回整表替换")
	}

	// 掩码回填：提交值仍是掩码时，按该档旧真值回填，绝不把 sk-**** 写进库
	before := snap[config.StageAIInitial].APIKey
	code, _ = postStages(t, s, token, map[string]interface{}{
		config.StageAIInitial: map[string]interface{}{
			"api_base": "https://initial.example/v1", "api_key": maskKey("sk-initial-key"), "model": "initial/Model-v3",
		},
	})
	if code != http.StatusOK {
		t.Fatalf("掩码回填提交应 200，实际 %d", code)
	}
	snap = stageSnapshot(t, s.Store)
	if got, err := decodeStageKey(t, snap, config.StageAIInitial); err != nil || got != "sk-initial-key" {
		t.Fatalf("掩码位应回填旧真值，实际 %q（err=%v）⇒ 会把 sk-**** 当密钥存进库", got, err)
	}
	if snap[config.StageAIInitial].APIKey == before {
		t.Fatalf("model 改了却整条没变？说明本次提交根本没生效")
	}
	if snap[config.StageAIInitial].Model != "initial/Model-v3" {
		t.Fatalf("掩码回填不该影响同档其他字段：model=%s", snap[config.StageAIInitial].Model)
	}
}

// decodeStageKey 把库里某档的密文解回明文（顺带守住「没被二次加密」这一条）。
func decodeStageKey(t *testing.T, m config.StageModels, stage string) (string, error) {
	t.Helper()
	sm, ok := m[stage]
	if !ok {
		return "", &stageError{stage: stage, reason: "该档不存在"}
	}
	plain := store.DecryptSecret(sm.APIKey)
	if strings.HasPrefix(plain, store.SecretEncPrefix) {
		return "", &stageError{stage: stage, reason: "解密后仍是密文 ⇒ 被重复加密了"}
	}
	return plain, nil
}

// stageError 本测试族的小错误类型（只为把「哪一档、为什么」带进 t.Fatalf）。
type stageError struct {
	stage  string
	reason string
}

func (e *stageError) Error() string { return e.stage + ": " + e.reason }
