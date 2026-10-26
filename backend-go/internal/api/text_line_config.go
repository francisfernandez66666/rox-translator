// ============ 本文件职责中文说明 ============
// 2026-10-26 用户追加裁决九-4「`text_line_retry_max`／`text_line_upstream_dead_streak` 要给运营配」
// 的实现：逐行翻译两条档位的超管配置口。
//
// 接口（仅平台超管，形态照抄仓内既有的 est-tokens 白名单枚举口，不新造页面、不开第二套 KV 编辑面）：
//   - GET  /api/admin/config/text-lines  回显两档的**生效值**（现读读侧那两个钳位函数）＋库内原值＋代码缺省
//   - POST /api/admin/config/text-lines  保存 {"retry_max":3,"upstream_dead_streak":3} 或 {"reset":true}
//
// 为什么回显必须现读读侧的钳位函数，而不是把配置值原样吐回去：
// 钳位是判据的一部分（裁决九-4 明写）。库里存 9 而读侧实际按 10 用，管理台若回显 9，
// 运营看到的是"我配了 9"，值班排查时按 9 推行为——那是「读数为失败背书」的同族：
// 一个把真实档位撒谎的读数比没有读数更坏。
//
// ★ 与 orchestrator/engine 的口径必须一致：钳位与默认值**只有一份实现**
//
//	（engine.LineRetryMax／engine.LineDeadStreakMin，2026-10-26 从 orchestrator 上收到 engine 一处），
//	本文件不自写第二套 min/max，否则改一侧漏一侧＝两条链两个灵敏度。
//
// ★ 这一族**没有 0 档语义**：0 与负数一律当脏值拒收（不落库、并回明确文案）。
//
//	同 §一.18 那批 `ConfigInt` 把 ≤0 读成"没配"的教训方向相反——那里是"0 是一档真实值"，
//	这里是"0 会把补译轮数/早停灵敏度打成不做事"，所以宁可在写入侧就拦住。
//
// ==========================================
package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	apierrors "translator/internal/errors"
	"translator/internal/store"
)

// textLineCfgField 一条档位键在管理台上的表述。
type textLineCfgField struct {
	jsonField string // 请求体里的字段名
	key       string // system_config 键名
	def       int    // 代码缺省（与读侧同一个常量；本口只用于回显"缺省是多少"）
	lo, hi    int    // 允许区间（含端点）——读侧钳位同一区间，两侧由同一条锁钉住
}

// textLineCfgFields 两条可配档位，顺序即表单展示顺序，也是"两项必须一次交齐"的判据来源。
// ⚠️ lo/hi 不是这里说了算的判据，只是**写入侧的拒收区间**；读侧钳位在 engine 那两个函数里，
//
//	两侧等值由 text_line_config_gate_test.go 逐条比字符串钉住（改一侧必红，防止漂）。
var textLineCfgFields = []textLineCfgField{
	{"retry_max", "text_line_retry_max", 3, 1, 5},
	{"upstream_dead_streak", "text_line_upstream_dead_streak", 3, 2, 10},
}

// handleAdminTextLineConfig GET/POST /api/admin/config/text-lines —— 逐行翻译档位超管口。
func (s *Server) handleAdminTextLineConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleAdminTextLineConfigGet(w, r)
	case http.MethodPost:
		s.handleAdminTextLineConfigSave(w, r)
	default:
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不允许"))
	}
}

// textLineCfgEffective 现读两档的**生效值**（读侧钳位函数）＋库内原值＋代码缺省。
// 单独抽出来是因为 GET 回显与 POST 保存后回显必须是同一个函数——
// 否则"保存成功却回显旧值"这种形态只能靠人眼发现（本仓那条「改完即生效要真改第二次验」）。
func (s *Server) textLineCfgEffective() map[string]interface{} {
	eff := map[string]interface{}{}
	stored := map[string]string{}
	for _, f := range textLineCfgFields {
		raw := ""
		if s.Store != nil {
			raw, _ = s.Store.GetConfig(f.key)
		}
		stored[f.key] = raw
		eff[f.jsonField] = s.textLineCfgLiveValue(f)
	}
	return map[string]interface{}{
		"success":  true,
		"tiers":    eff,    // 生效值（读侧钳位后真正在用的那一档）
		"stored":   stored, // 库内原值（运营要看得见"我存的是什么"，与生效值不同就说明脏值被钳位）
		"defaults": map[string]int{"retry_max": 3, "upstream_dead_streak": 3},
		"ranges": map[string][2]int{
			"retry_max":            {textLineCfgFields[0].lo, textLineCfgFields[0].hi},
			"upstream_dead_streak": {textLineCfgFields[1].lo, textLineCfgFields[1].hi},
		},
	}
}

// textLineCfgLiveValue 按字段取读侧此刻真正生效的那一档。
// 走 engine 那两个函数（唯一实现），不在本文件重算钳位——两侧各写一套 min/max 迟早打架。
func (s *Server) textLineCfgLiveValue(f textLineCfgField) int {
	if s.Engine == nil {
		return f.def // 引擎没装配（挂件/工具形态的实例）⇒ 按代码缺省回显，不猜库里脏值
	}
	switch f.key {
	case "text_line_retry_max":
		return s.Engine.LineRetryMax()
	case "text_line_upstream_dead_streak":
		return s.Engine.LineDeadStreakMin()
	}
	return f.def
}

// handleAdminTextLineConfigGet 回显两档。鉴权与保存口同一档（平台级档位，读面也不给租户管理员看）。
func (s *Server) handleAdminTextLineConfigGet(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireTextLineAdmin(w, r); err != nil {
		return
	}
	writeJSON(w, 200, s.textLineCfgEffective())
}

// handleAdminTextLineConfigSave 校验先行、两项都过才整批落库（半套参数＝一条链新档一条链旧档）。
func (s *Server) handleAdminTextLineConfigSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTextLineAdmin(w, r)
	if err != nil {
		return
	}
	// 指针字段：nil＝没提交这一项（与"提交了 0"必须可区分，否则脏值会被当成"清空"落库）
	var req struct {
		RetryMax           *int `json:"retry_max"`
		UpstreamDeadStreak *int `json:"upstream_dead_streak"`
		Reset              bool `json:"reset"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation,
			`请求格式错误：需为 {"retry_max":3,"upstream_dead_streak":3}`))
		return
	}
	submitted := map[string]*int{"retry_max": req.RetryMax, "upstream_dead_streak": req.UpstreamDeadStreak}
	// —— 恢复缺省：写成空串，读侧遇空回代码缺省 ⇒ 与"从未配过"完全同形，
	//    不留 0 这种会被误读成"一枪都不打"的中间态。
	if req.Reset {
		for _, f := range textLineCfgFields {
			if err := s.Store.SetConfig(f.key, ""); err != nil {
				s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "恢复缺省失败："+err.Error()))
				return
			}
		}
		s.Store.LogAudit(s.effTenant(r, u), u.ID, "text_line_config_reset", "system_config",
			"逐行翻译两档已清空，回落代码缺省 retry_max=3 upstream_dead_streak=3")
		writeJSON(w, 200, s.textLineCfgEffective())
		return
	}
	vals := map[string]int{}
	for _, f := range textLineCfgFields {
		p := submitted[f.jsonField]
		if p == nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation,
				"两条档位必须一次提交，缺少 "+f.jsonField))
			return
		}
		v := *p
		if v < f.lo || v > f.hi {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation,
				f.jsonField+" 必须落在 "+strconv.Itoa(f.lo)+" ~ "+strconv.Itoa(f.hi)+
					"（这一族没有 0 档语义：0 或负数＝一枪都不打/一行失败就打死整语种，脏值不落库）"))
			return
		}
		vals[f.key] = v
	}
	for _, f := range textLineCfgFields {
		if err := s.Store.SetConfig(f.key, strconv.Itoa(vals[f.key])); err != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "保存失败："+err.Error()))
			return
		}
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "text_line_config_save", "system_config",
		"逐行翻译档位已保存 retry_max="+strconv.Itoa(vals["text_line_retry_max"])+
			" upstream_dead_streak="+strconv.Itoa(vals["text_line_upstream_dead_streak"]))
	writeJSON(w, 200, s.textLineCfgEffective())
}

// requireTextLineAdmin 平台级档位鉴权：只走 requireAdminUser 这一道共享闸（与 est-tokens 同档）。
// 为什么不给租户管理员：这两条改的是**所有租户每一单**的补译枪数与早停灵敏度——
// 给租户配等于"A 租户能把 B 租户的单拖慢"，方向与租户隔离相反。
//
// ★ 2026-10-27 复跑归因（本批 UAT 首跑两条 403 的真身）：这里原来还挂着第二条
// `!auth.IsSuperAdmin(u)` 的自造 403 分支，而它**永远不会成立**——requireAdminUser 里的
// iam.RequireRole(u,4) 在 09-16 安全整改那批已经带了第二条「required≥4 且 tenant_id≠0 同样拒」，
// 于是"等级够但挂在具体租户"的账号在上一道闸就被拦下（实测读数：租户内 admin 打这个口回
// `403 FORBIDDEN 权限不足：需要 超级管理员 角色`）。留着一条永不成立的分支＝给读不到的路径写判据
// （§一·8 那条「不许把走不到的分支当契约」），删。**「只有平台超管能配」这件事由那一道共享闸保证**，
// 本文件不再造第二套超管判据——同 §一·3 那条「优先级口径只有一份尺子」。
func (s *Server) requireTextLineAdmin(w http.ResponseWriter, r *http.Request) (*store.User, error) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		s.writeAuthzError(w, r, err)
		return nil, err
	}
	return u, nil
}
