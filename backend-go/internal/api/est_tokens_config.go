// ============ 本文件职责中文说明 ============
// 计费预估参数（★ F-72 两段式预估，2026-09-27 〇-W 落地、〇-X 第 5 项进管理台）的超管配置口。
//
// 接口（仅平台超管，鉴权/审计/「校验先行整批落库」写法参照 quote_currency.go）：
//   - GET  /api/admin/config/est-tokens  回显四系数（生效值＋库内原值＋代码缺省＋公式与汇率）
//   - POST /api/admin/config/est-tokens  保存 {"k_pro":160,"k_fast":60,"fixed_pro":3000,"fixed_fast":1200}
//     或 {"reset":true}（四项一律回落代码缺省）。
//
// 为什么要这个口子：这四键此前只能靠 psql 往 system_config 插行（两库都没有这四行，
// 线上跑的是代码缺省 160/60 + 3000/1200）。预检系数直接决定「客户能不能建单」，
// 一个手滑的 SQL 就是全量放行或全量误拦，必须有表单、有校验、有审计。
//
// ★ 与 estimateTicketTokens 的口径必须一致（改这里先看 tickets.go）：
//
//	K 档（每字符×每语种 token）清零＝全放行，是 F-41 的事故形态，所以 K 必须 >0；
//	F 档（一次性固定开销）允许显式 0＝退回纯线性，这是运维的合法选择，不拦；
//	负数与非数字两侧都不允许落库（读侧会静默回退缺省，形成「保存成功却不生效」的假配置）。
//	上限是防呆（把 160 打成 160000 会让所有建单被误拦），不是业务档位。
//
// ★ 不设环境变量接管：预估系数只从 system_config 现读（estConfigFloat），
//
//	所以本口没有 quote_currency 的 env_overridden 一项——别照抄那段逻辑造一个永远为空的字段。
//
// ==========================================
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"translator/internal/auth"
	apierrors "translator/internal/errors"
	"translator/internal/store"
)

// estCfgField 一个预估系数在管理台上的表述（键名＋代码缺省＋是否允许 0）。
type estCfgField struct {
	jsonField string  // 请求体里的字段名
	key       string  // system_config 键名
	def       float64 // 代码缺省（读侧回退值，两侧共用同一常量，禁止前端另写一份）
	allowZero bool    // F 档允许 0（＝关闭固定项）；K 档不允许（0＝全放行）
}

// estCfgFields 管理台可配的四个预估系数，按 pro/fast × 线性 K／固定 F 两两成对。
// 顺序即表单展示顺序，同时也是「四项必须一次交齐」的判据来源。
var estCfgFields = []estCfgField{
	{"k_pro", cfgEstKPro, defEstKPro, false},
	{"k_fast", cfgEstKFast, defEstKFast, false},
	{"fixed_pro", cfgEstFixedPro, defEstFixedPro, true},
	{"fixed_fast", cfgEstFixedFast, defEstFixedFast, true},
}

// estCfgMax 系数上限（防呆）：正常量级 K≤几百、F≤几万，这里给到千万，
// 目的只是拦住「多打几个 0」这种会把预估吹到天上去、进而全量误拦建单的输入。
const estCfgMax = 1e7

// handleAdminEstTokens GET/POST /api/admin/config/est-tokens —— 预估系数超管配置口。
func (s *Server) handleAdminEstTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.handleAdminEstTokensSave(w, r)
		return
	}
	if _, err := s.requireEstCfgAdmin(w, r); err != nil {
		return
	}
	effective := make(map[string]float64, len(estCfgFields))
	stored := make(map[string]string, len(estCfgFields))
	defaults := make(map[string]float64, len(estCfgFields))
	for _, f := range estCfgFields {
		// 生效值走读侧同一函数（estTokensPerChar / estTokensFixed），回显即可信值：
		// 库里躺着脏值时这里显示的就是「系统实际在用哪个数」，而不是把脏值原样吐给界面。
		effective[f.jsonField] = s.estConfigFloat(f.key, f.def, f.allowZero)
		defaults[f.jsonField] = f.def
		raw, _ := s.Store.GetConfig(f.key)
		stored[f.jsonField] = strings.TrimSpace(raw) // 空串＝该键未配置，走代码缺省
	}
	writeJSON(w, 200, map[string]interface{}{
		"success":      true,
		"coefficients": effective,
		"stored":       stored,
		"defaults":     defaults,
		"allow_zero":   map[string]bool{"k_pro": false, "k_fast": false, "fixed_pro": true, "fixed_fast": true},
		"formula":      "est_tokens = F(模式) + 源字符数 × 目标语种数 × K(模式)",
		"formula_note": "K 为线性系数（每字符×每语种 token），F 为每次建单的一次性开销（系统提示词、术语与上下文注入、输出结构）。短单由 F 兜底、长单由 K 决定量级。",
		"config_keys":  estCfgKeyNames(),
		// 积分↔token 汇率只读回显：管理台据此把 token 系数折算成「一次大概多少积分」给客户/自己看。
		// 改汇率是另一条链路（points_tokens_rate，随套餐出厂配置走），本口不提供写入口。
		"points_tokens_rate": s.Store.PointsTokensRate(),
	})
}

// handleAdminEstTokensSave POST 保存四个预估系数。
// 语义：四项必须一次交齐（缺项即整体拒绝，不留半套参数）；reset=true 时四项一律清空回落代码缺省。
func (s *Server) handleAdminEstTokensSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireEstCfgAdmin(w, r)
	if err != nil {
		return
	}
	// 指针字段：nil＝没提交这一项（与"提交了 0"必须可区分，否则 K 档会被静默清零）
	var req struct {
		KPro      *float64 `json:"k_pro"`
		KFast     *float64 `json:"k_fast"`
		FixedPro  *float64 `json:"fixed_pro"`
		FixedFast *float64 `json:"fixed_fast"`
		Reset     bool     `json:"reset"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation,
			"请求格式错误：需为 {\"k_pro\":160,\"k_fast\":60,\"fixed_pro\":3000,\"fixed_fast\":1200}"))
		return
	}
	submitted := map[string]*float64{
		"k_pro": req.KPro, "k_fast": req.KFast, "fixed_pro": req.FixedPro, "fixed_fast": req.FixedFast,
	}
	// —— 恢复缺省：把四键值写成空串，读侧（estConfigFloat）遇空即回代码缺省，
	//    等于「库里不再表态」，与从未配置过的状态完全一致，不留下 0 这种会被误读的中间态。
	if req.Reset {
		for _, f := range estCfgFields {
			if err := s.Store.SetConfig(f.key, ""); err != nil {
				s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "恢复缺省失败："+err.Error()))
				return
			}
		}
		s.Store.LogAudit(s.effTenant(r, u), u.ID, "est_tokens_reset", "system_config",
			"四个预估系数已清空，回落代码缺省 "+estCfgAuditVals(defaultsByField()))
		writeJSON(w, 200, map[string]interface{}{"success": true, "coefficients": s.estEffective()})
		return
	}
	// —— 校验先行：四项都过才落库（半套参数＝短单与长单口径不一致，比不调更糟）
	vals := make(map[string]float64, len(estCfgFields))
	for _, f := range estCfgFields {
		p := submitted[f.jsonField]
		if p == nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation,
				"预估系数必须四项一次提交，缺少 "+f.jsonField))
			return
		}
		v := *p
		if v != v || v > estCfgMax { // NaN 与离谱上限
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation,
				f.jsonField+" 取值超出合理范围（0 ~ "+strconv.FormatFloat(estCfgMax, 'f', 0, 64)+"）"))
			return
		}
		if v < 0 {
			// 负数在两侧都不合法：读侧会静默回退缺省，落库等于留一条「保存成功却不生效」的假配置，
			// 而且它比清零更隐蔽——F 档允许 0，负数会把固定项往回扣。
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation,
				f.jsonField+" 不能为负数（负数会把预估往回扣，比清零更危险）"))
			return
		}
		if v == 0 && !f.allowZero {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation,
				f.jsonField+" 必须大于 0：线性系数清零＝余额预检全放行，正是历史上烧穿事故的形态（固定项 fixed_* 才可以配 0）"))
			return
		}
		vals[f.jsonField] = v
	}
	for _, f := range estCfgFields {
		if err := s.Store.SetConfig(f.key, strconv.FormatFloat(vals[f.jsonField], 'f', -1, 64)); err != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "保存失败："+err.Error()))
			return
		}
	}
	// 审计留痕：这四个数直接决定客户能不能建单，谁改的、改成了什么都得可查
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "est_tokens_save", "system_config", estCfgAuditVals(vals))
	writeJSON(w, 200, map[string]interface{}{"success": true, "coefficients": s.estEffective()})
}

// requireEstCfgAdmin 预估系数口鉴权：平台超管（L4 且 auth.IsSuperAdmin）。
// 判定链与 requireQuoteCfgAdmin 完全一致，只是 403 文案换成本域（不借用「报价」字样）。
func (s *Server) requireEstCfgAdmin(w http.ResponseWriter, r *http.Request) (*store.User, error) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		s.writeAuthzError(w, r, err)
		return nil, err
	}
	if !auth.IsSuperAdmin(u) {
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "仅平台超管可配置计费预估系数"))
		return nil, &apiErr{"非超管"}
	}
	return u, nil
}

// estEffective 回显当前四个系数的生效值（保存成功后复用，避免前端再发一次 GET）。
func (s *Server) estEffective() map[string]float64 {
	out := make(map[string]float64, len(estCfgFields))
	for _, f := range estCfgFields {
		out[f.jsonField] = s.estConfigFloat(f.key, f.def, f.allowZero)
	}
	return out
}

// estCfgKeyNames system_config 四键名（管理台展示"改的是哪几行"，排障时可直接对着 psql 核）。
func estCfgKeyNames() []string {
	out := make([]string, 0, len(estCfgFields))
	for _, f := range estCfgFields {
		out = append(out, f.key)
	}
	return out
}

// defaultsByField 代码缺省表（仅审计串使用）。
func defaultsByField() map[string]float64 {
	out := make(map[string]float64, len(estCfgFields))
	for _, f := range estCfgFields {
		out[f.jsonField] = f.def
	}
	return out
}

// estCfgAuditVals 系数表 → 审计串（按字段声明顺序拼接，保证同一份配置每次留下同一串，可直接 diff）。
func estCfgAuditVals(vals map[string]float64) string {
	parts := make([]string, 0, len(estCfgFields))
	for _, f := range estCfgFields {
		if v, ok := vals[f.jsonField]; ok {
			parts = append(parts, f.jsonField+"="+strconv.FormatFloat(v, 'f', -1, 64))
		} else {
			parts = append(parts, f.jsonField+"=缺省"+strconv.FormatFloat(f.def, 'f', -1, 64))
		}
	}
	return strings.Join(parts, " ")
}
