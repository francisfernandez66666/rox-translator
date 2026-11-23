// ============ 本文件职责中文说明 ============
// 2026-11-23 用户裁决：tier3 采集语义闸的「回译模型」改由平台超管在管理台配置。
//
// 接口（仅平台超管，形态照抄 text_line_config.go，不新造 KV 编辑面）：
//   - GET  /api/admin/config/scrape-backtranslate  回显回译模型的生效值＋库内原值＋代码缺省
//   - POST /api/admin/config/scrape-backtranslate  保存 {"model":"tencent/Hunyuan-MT-7B"} 或 {"reset":true}
//
// 为什么这一个键要能配：语义闸的回译腿用哪个模型直接决定错译拦不拦得住——
// 配错模型（如塞个只会原样吐回的）等于语义闸恒失效，而调档不该要求发版。
//
// ★ 生效值的唯一口径在 internal/api/scrape_quality.go 的 defaultBacktranslateModel +
//
//	newQualityJudge 的读键逻辑；本文件只回显同一常量与同一键，不自造第二份缺省。
//	模型名白名单也只在下面一处声明（allow 列表），写入侧与前端预拦共用（前端从 GET 回显取）。
//
// ★ 鉴权只走 requireAdminUser 一道共享闸（内含平台超管判定，见 text_line_config.go
//
//	175-193 行的复跑结论），不自造第二套 IsSuperAdmin 分支。
//
// ==========================================
package api

import (
	"encoding/json"
	"net/http"
	"strings"

	apierrors "translator/internal/errors"
	"translator/internal/store"
)

const scrapeBacktranslateConfigKey = "scrape_tier3_backtranslate_model"

// scrapeBacktranslateAllowed 允许作为回译腿的模型白名单。
// 判据：只有翻译专精、实测能把 he 正确回译中文的模型才进；evals/判定类模型不进
// （实测 HY 直接做 CORRECT/WRONG 判别≈抛硬币，放进来等于把语义闸打断）。
var scrapeBacktranslateAllowed = []string{
	"tencent/Hunyuan-MT-7B",
}

// handleAdminScrapeBacktranslate GET/POST /api/admin/config/scrape-backtranslate。
func (s *Server) handleAdminScrapeBacktranslate(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if _, err := s.requireScrapeQualityAdmin(w, r); err != nil {
			return
		}
		writeJSON(w, 200, s.scrapeBacktranslateEffective())
	case http.MethodPost:
		s.handleAdminScrapeBacktranslateSave(w, r)
	default:
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不允许"))
	}
}

// scrapeBacktranslateEffective 回显生效值（库内配了就用它、没配就是代码缺省）＋库内原值＋缺省＋白名单。
// GET 与 POST 保存后回显都走这里，保证"保存成功回显的就是生效值"。
func (s *Server) scrapeBacktranslateEffective() map[string]interface{} {
	stored := ""
	if s.Store != nil {
		stored, _ = s.Store.GetConfig(scrapeBacktranslateConfigKey)
	}
	stored = strings.TrimSpace(stored)
	effective := defaultBacktranslateModel
	if stored != "" {
		effective = stored
	}
	return map[string]interface{}{
		"success":    true,
		"model":      effective, // 生效值
		"stored":     stored,    // 库内原值（空＝走代码缺省）
		"default":    defaultBacktranslateModel,
		"allowed":    scrapeBacktranslateAllowed,
		"config_key": scrapeBacktranslateConfigKey,
	}
}

// handleAdminScrapeBacktranslateSave 校验先行：模型名必须在白名单内才落库。
func (s *Server) handleAdminScrapeBacktranslateSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireScrapeQualityAdmin(w, r)
	if err != nil {
		return
	}
	var req struct {
		Model *string `json:"model"`
		Reset bool    `json:"reset"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation,
			`请求格式错误：需为 {"model":"tencent/Hunyuan-MT-7B"}`))
		return
	}
	if req.Reset {
		if err := s.Store.SetConfig(scrapeBacktranslateConfigKey, ""); err != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "恢复缺省失败："+err.Error()))
			return
		}
		s.Store.LogAudit(s.effTenant(r, u), u.ID, "scrape_backtranslate_reset", "system_config",
			"采集回译模型已清空，回落代码缺省 "+defaultBacktranslateModel)
		writeJSON(w, 200, s.scrapeBacktranslateEffective())
		return
	}
	if req.Model == nil || strings.TrimSpace(*req.Model) == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "model 不能为空；恢复缺省用 {\"reset\":true}"))
		return
	}
	model := strings.TrimSpace(*req.Model)
	ok := false
	for _, a := range scrapeBacktranslateAllowed {
		if model == a {
			ok = true
			break
		}
	}
	if !ok {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation,
			"回译模型不在白名单内：只允许 "+strings.Join(scrapeBacktranslateAllowed, "、")+
				"；其它模型未经 he→zh 回译实测，配置上去等于把语义闸打断"))
		return
	}
	if err := s.Store.SetConfig(scrapeBacktranslateConfigKey, model); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "保存失败："+err.Error()))
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "scrape_backtranslate_save", "system_config",
		"采集回译模型已保存 model="+model)
	writeJSON(w, 200, s.scrapeBacktranslateEffective())
}

// requireScrapeQualityAdmin 平台级档位鉴权：只走 requireAdminUser 一道共享闸（见 text_line_config.go 结论）。
func (s *Server) requireScrapeQualityAdmin(w http.ResponseWriter, r *http.Request) (*store.User, error) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		s.writeAuthzError(w, r, err)
		return nil, err
	}
	return u, nil
}
