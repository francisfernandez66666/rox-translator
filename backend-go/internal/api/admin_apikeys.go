// ============ admin_apikeys.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 开放 API Key：签发 / 启停 / 轮换 / 删除（handleAPIKeys 系列）
// 安全要点：所有写操作均记录审计日志（LogAudit）；API Key 密钥仅明文返回一次，前端立即保存。
// ★ F-64②（批 I-10）口径：本文件失败响应已统一走 s.writeError + apierrors 出口，
//   不再用「HTTP 200 + success:false」壳承载失败（DB 写失败 500 / Key 不存在 404）。
// ========================================

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	apierrors "translator/internal/errors"
)

// ============ 开放 API Key ============

// handleAPIKeys 查询当前租户下的开放 API Key 列表（密钥已脱敏展示，不可复原明文）。参数 w/r：标准 HTTP；鉴权：租户管理员及以上；按 effTenant 租户隔离；返回 keys 数组。
func (s *Server) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	keys, err := s.Store.ListAPIKeys(s.effTenant(r, u))
	if err != nil {
		// F-64②：列 Key 读的是本进程存储层，失败＝服务端出错（500）；
		// 旧写法回 200 会让面板把「查询失败」当「该租户一个 Key 都没有」渲染成空列表。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "keys": keys})
}

// handleAPIKeyCreate 为当前租户签发新的开放 API Key（明文仅本次返回，前端须立即保存）。参数 w/r：body 含 name/perms/daily_call_limit；鉴权：租户管理员及以上；副作用：写入 api_keys 表并记审计 apikey_create；平台上下文自动落到首个活跃租户避免任务归属悬空。
func (s *Server) handleAPIKeyCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		Name       string `json:"name"`             // Key 名称（便于管理识别）
		Perms      string `json:"perms"`            // 权限范围（all/translate/kb/billing）
		DailyLimit int64  `json:"daily_call_limit"` // 每日调用上限（0=不限，R4 Key 级配额）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "name 不能为空"})
		return
	}
	tidForKey := s.effTenant(r, u)
	if tidForKey <= 0 {
		// ★ 平台上下文签发的 Key 必须落到具体租户：取首个活跃租户（否则任务归属悬空导致回读404）
		tidForKey = s.Store.FirstActiveTenantID()
	}
	plain, err := s.Store.CreateAPIKey(tidForKey, u.ID, req.Name, req.Perms, req.DailyLimit)
	if err != nil {
		// F-64②：签发失败只有两类——随机源不可用（拒绝落弱密钥）与 api_keys 插入失败（数据库写故障），
		// 两者都是本层服务端出错（500）；api_keys.name 无唯一约束，故不存在「名称重复」这条 409 分支。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "apikey_create", "api_keys", req.Name)
	writeJSON(w, 200, map[string]interface{}{"success": true, "api_key": plain, "note": "请立即保存，仅显示一次"})
}

// handleAPIKeyStatus 启用/停用指定 API Key（status=active/disabled）。参数 w/r：body 含 id 与 status；鉴权：租户管理员及以上；副作用：更新状态并写审计；按租户隔离。
func (s *Server) handleAPIKeyStatus(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		ID     int64  `json:"id"`     // 目标 API Key ID
		Status string `json:"status"` // 目标状态：active（启用）/ disabled（停用）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	tid := s.effTenant(r, u)
	if _, err := s.Store.GetAPIKey(req.ID, tid); err != nil {
		// F-64②：跨租户越权启停别人的 Key、或 id 根本不存在，一律「Key 不存在」404
		// （租户隔离下不回 403，避免泄露他租户 Key 的存在性）。
		// ★ 2026-10-16：这一道**存在性前置**是本批补的——UPDATE 的 0 行影响不报错，
		// 少了它就会把「没改到任何东西」回成 success（现网「后台删不掉/停不掉」的形态）。
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "Key 不存在"))
		return
	}
	if err := s.Store.SetAPIKeyStatus(req.ID, tid, req.Status); err != nil {
		// F-64②：走到这里＝数据库执行失败＝服务端出错（500）。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(tid, u.ID, "apikey_set_status", "api_keys", fmt.Sprintf("%d status=%s", req.ID, req.Status))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleAPIKeyRotate 轮换 API Key（本租户，旧 Key 立即失效）
func (s *Server) handleAPIKeyRotate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		ID int64 `json:"id"` // 待轮换 API Key ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	tid := s.effTenant(r, u)
	old, err := s.Store.GetAPIKey(req.ID, tid)
	if err != nil {
		// F-64②：GetAPIKey 按 id（+tid>0 时的租户）精确查一行，查不到（含跨租户越权轮换别人的 Key）
		// 就是「本租户下没有这条 Key」→ 404；租户隔离下不回 403，避免泄露他租户 Key 的存在性。
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "Key 不存在"))
		return
	}
	if err := s.Store.DeleteAPIKey(req.ID, tid); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	// ★ 2026-10-16：新 Key 必须继承**被轮换那把 Key 的租户**（old.TenantID），不能用 tid——
	// 超管在平台上下文（tid=0）轮换他租户的 Key 时，用 tid 会把新 Key 落成 tenant_id=0 的孤儿
	// （列表聚合里还看得见，但任务归属校验与租户面再也查不到）。审计同理落 old.TenantID。
	newTid := old.TenantID
	plain, err := s.Store.CreateAPIKey(newTid, old.UserID, old.Name, old.Perms, old.DailyCallLimit) // 轮换继承旧限额与归属用户
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	s.Store.LogAudit(newTid, u.ID, "apikey_rotate", "api_keys", old.Name)
	writeJSON(w, 200, map[string]interface{}{"success": true, "api_key": plain, "note": "旧 Key 已失效，新 Key 仅显示一次"})
}

// handleAPIKeyDelete 删除指定 API Key（按租户隔离）。参数 w/r：body 含 id；鉴权：租户管理员及以上；副作用：删除记录并写审计。
func (s *Server) handleAPIKeyDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		ID int64 `json:"id"` // 待删除 API Key ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	tid := s.effTenant(r, u)
	if _, err := s.Store.GetAPIKey(req.ID, tid); err != nil {
		// 不存在/跨租户一律 404「Key 不存在」（不泄露他租户存在性）。
		// ★ 2026-10-16：删不掉的那条现网缺陷正是缺了这道存在性前置——见 handleAPIKeyStatus 同处注释。
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "Key 不存在"))
		return
	}
	if err := s.Store.DeleteAPIKey(req.ID, tid); err != nil {
		// F-64②：真删失败了才是 500（存在性已在上面判过，删到 0 行在这里已不可能）。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(tid, u.ID, "apikey_delete", "api_keys", fmt.Sprintf("%d", req.ID))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleAPIKeyLimit 设置 API Key 每日调用上限（R4 Key 级配额；0=不限）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id 与 daily_call_limit，租户管理员及以上）。
func (s *Server) handleAPIKeyLimit(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		ID    int64 `json:"id"`               // Key 主键 ID
		Limit int64 `json:"daily_call_limit"` // 每日上限（0=不限）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 || req.Limit < 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	tid := s.effTenant(r, u)
	if _, err := s.Store.GetAPIKey(req.ID, tid); err != nil {
		// F-64②：文案「Key 不存在」按原样保留，状态码随文案取 404（本租户下查不到这条 Key 的语义，
		// 且租户隔离下跨租户改限额也只回「不存在」不泄露存在性）。
		// ★ 2026-10-16 修正：旧注释写着「store 侧是 UPDATE，id 不存在只算 0 行影响、并不报错，
		// 这条分支实际只会被数据库执行失败触发」——那是把缺陷当契约登记的写法（同 handleAPIKeyDelete）。
		// 现在这里**显式前置存在性检查**，所以这条 404 就是「真查不到」，不再是空转分支。
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "Key 不存在"))
		return
	}
	if err := s.Store.SetAPIKeyDailyLimit(req.ID, tid, req.Limit); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(tid, u.ID, "apikey_set_limit", "api_keys",
		fmt.Sprintf("%d limit=%d", req.ID, req.Limit))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// issueDefaultAPIKey 为新建租户签发默认开放 API Key（translate 权限、不限量）。
// 最佳努力语义：签发失败仅记日志，不阻断租户创建/注册主流程。
// 返回: 明文 Key（仅此一次返回；空串表示签发失败或存储未就绪）。
func (s *Server) issueDefaultAPIKey(tid int64, name string) string {
	return s.issueDefaultAPIKeyFor(tid, 0, name)
}

// issueDefaultAPIKeyFor 指定归属用户签发租户默认 Key（uid>0 即时强绑定，免等重启回填）。
func (s *Server) issueDefaultAPIKeyFor(tid, userID int64, name string) string {
	if s.Store == nil || tid <= 0 {
		return ""
	}
	plain, err := s.Store.CreateAPIKey(tid, userID, name, "translate", 0)
	if err != nil {
		log.Printf("[tenant-default-key] 租户 %d 默认 API Key 签发失败: %v", tid, err)
		return ""
	}
	s.Store.LogAudit(tid, 0, "apikey_issue_default", "api_keys", name)
	return plain
}

// handleAPIKeyReveal 解密返回 Key 明文（固定复制能力：前端只复制不展示）。
// 鉴权：租户管理员及以上 + 租户隔离。
func (s *Server) handleAPIKeyReveal(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		ID int64 `json:"id"` // Key 主键 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	plain, err := s.Store.GetAPIKeyPlain(req.ID, s.effTenant(r, u))
	if err != nil || plain == "" {
		writeJSON(w, 404, map[string]interface{}{"success": false, "message": "Key 不存在或未存储可复制密文"})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "api_key": plain})
}
