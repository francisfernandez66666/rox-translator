// ============ admin_purge.go · 职责说明 ============
// 超管「删除任意租户 / 组织 / 用户」的权限收口（★ 2026-10-23 用户令）。
//
// 现状盘点（为什么只新增了一个接口）：
//
//	· **用户**：`handleAdminUserDelete`（auth.go）已存在，超管在平台上下文（X-Tenant-ID 缺省＝0）
//	  时会回落到 ListAllUsers 全局定位，因此**跨租户删除本来就能做**；本批只补测试把它钉住。
//	· **组织**：`handleOrgDelete`（orgs.go）走 `orgTenant`，超管按「组织自身归属租户」解析，
//	  也**本来就能跨租户删**；同样只补测试。
//	· **租户**：**没有任何接口**——后台只能看到租户列表，删不掉。本批新增
//	  `POST /api/admin/tenants/delete`（本文件）。
//
// 三条安全口径（删数据是不可逆动作，宁可多一道确认）：
//
//	① **仅超管**（`auth.IsSuperAdmin`，L4）。租户管理员/部门管理员一律 403——
//	   删租户不是「管理自己的租户」，而是「抹掉一个租户的全部存在」。
//	② **不能删自己所在的租户**：那会把调用者自己的账号、组织、审计一起删掉，
//	   请求成功返回但操作者当场失去登录能力，事后连「谁删的」都查不到（审计行也一起没了）。
//	③ **必须回显确认串**：body 里的 `confirm` 必须与目标租户的 `code` 或 `name` 逐字相等。
//	   这条挡的不是恶意（能调这个接口的已经是超管），是**手滑**：
//	   后台列表上「Hajimi」和「zzfbaiyanhaha」只差几行，删错一个就是一次无法回滚的事故。
//	   为什么不用「再点一次确认框」那种前端交互当判据：前端的确认可以被脚本绕过，
//	   服务端这条字符串比对才是真正的闸。
//
// 关于「任意租户」与保留租户：
//
//	本接口**不**硬编码保护 tenant 1（演示站）。理由是用户要的是「任意」，而硬编码一个 ID
//	会让「换个部署就删不掉」成为新的困惑。取而代之的是上面三条：超管 + 非本租户 + 回显确认串。
//	真要保护演示站，那是**部署策略**（不给超管那个账号 / 不给这台机的超管开这个面），
//	不该写死在删除逻辑里——写死之后，下一次「为什么删不掉」的排查会从这里开始，而线索是错的。
//
// =============================================
package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"translator/internal/auth"
	apierrors "translator/internal/errors"
	"translator/internal/store"
)

// requireSuperAdminPurge 删除类接口的超管鉴权（与 admin_scrape.go 的 requireSuperAdmin 同源判定，
// 只换错误文案：那边说「数据采集」，这边必须说「删除数据」，否则前端弹出的红字会指向错误的模块）。
func (s *Server) requireSuperAdminPurge(w http.ResponseWriter, r *http.Request) (*store.User, error) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		// 未登录 401／等级不足 403（沿用 F-64③ 批 I-10 的分档：前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return nil, err
	}
	if !auth.IsSuperAdmin(u) {
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "仅平台超管可删除租户数据"))
		return nil, &apiErr{"非超管"}
	}
	return u, nil
}

// purgeReading 级联删除结果的有序化形态（前端按它渲染「哪张表删了几行」）。
type purgeReading struct {
	Table string `json:"table"` // 表名
	Rows  int64  `json:"rows"`  // 实际删除行数
}

// handleAdminTenantDelete 删除指定租户及其全部租户级数据（仅超管，不可逆）。
//
// 请求：POST /api/admin/tenants/delete
//
//	body: {"id": 6, "confirm": "BYD"}
//	  · id      目标租户 ID（必填，>0）
//	  · confirm 目标租户的 code 或 name，必须逐字相等（必填，防手滑）
//
// 响应：success=true 时携带
//
//	{tenant_id, tenant_name, deleted_total, deleted:[{table,rows}], failed:[{table,error}], tenant_row_deleted}
//
// 状态码：400 参数/确认串不符；403 非超管或删自己所在租户；404 租户不存在；
// 500 租户行删除失败（此时 data 里仍带逐表读数，便于人工收尾）。
func (s *Server) handleAdminTenantDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireSuperAdminPurge(w, r)
	if err != nil {
		return
	}
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求方法不支持"))
		return
	}
	var req struct {
		ID      int64  `json:"id"`
		Confirm string `json:"confirm"`
	}
	if derr := json.NewDecoder(r.Body).Decode(&req); derr != nil || req.ID <= 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "参数错误：需要正整数 id"))
		return
	}
	// 口径②：不能删自己所在的租户（否则审计行与调用者账号一起消失，事后无从查起）
	if u.TenantID == req.ID {
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "不能删除自己所在的租户"))
		return
	}
	if s.Store == nil {
		// 存储未初始化（只在测试装配不完整时出现）：宁可 500 也不要 nil 解引用把进程打挂
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "存储未初始化"))
		return
	}
	code, name, ok := s.Store.TenantBrief(req.ID)
	if !ok {
		// 404 而非 403：目标不存在与「存在但你没权限」在这里没有区别的必要，
		// 而超管本来就有权限——如实说「不存在」最省事。
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "租户不存在"))
		return
	}
	// 口径③：回显确认串（code 或 name 二者之一逐字相等）
	want := strings.TrimSpace(req.Confirm)
	if want == "" || (want != code && want != name) {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation,
			"确认串不匹配：请在 confirm 里原样填写该租户的编码或名称"))
		return
	}
	res, perr := s.Store.PurgeTenantCascade(req.ID)
	if res == nil {
		// 只可能来自 tid<=0（上面已挡），留一条兜底避免 nil 解引用
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "删除未执行"))
		return
	}
	// 逐表读数整理成有序切片（map 直接进 JSON 顺序不稳，前端没法稳定展示）
	deleted := make([]purgeReading, 0, len(res.Deleted))
	for t, n := range res.Deleted {
		deleted = append(deleted, purgeReading{Table: t, Rows: n})
	}
	sort.Slice(deleted, func(i, j int) bool { return deleted[i].Table < deleted[j].Table })
	failed := make([]purgeReading, 0, len(res.Failed))
	for _, t := range res.FailedTables() {
		failed = append(failed, purgeReading{Table: t, Rows: 0})
	}
	if perr != nil {
		// 走到这里说明「租户行没删掉」——数据可能已清，但租户还在列表里。
		// 不吞成 200：那会让后台显示「已删除」而列表里还在，是最坏的一种不一致。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), perr)))
		return
	}
	// 审计：删完再记（此时目标租户的行已经没了，所以审计必须落在**操作者**的租户下，
	// 否则这条记录会跟着被删的租户一起消失——那正是口径②要防的事）。
	s.Store.LogAudit(u.TenantID, u.ID, "tenant_delete", "tenants",
		"删除租户 id="+itoa64(req.ID)+" code="+code+" name="+name+
			" 共清理 "+itoa64(res.TotalDeleted())+" 行，失败表 "+itoa64(int64(len(res.Failed)))+" 张")
	writeJSON(w, 200, map[string]interface{}{
		"success":            true,
		"tenant_id":          req.ID,
		"tenant_code":        code,
		"tenant_name":        name,
		"deleted_total":      res.TotalDeleted(),
		"deleted":            deleted,
		"failed":             failed,
		"tenant_row_deleted": res.TenantRowDeleted,
	})
}

// itoa64 轻量 int64→string（避免为一个数字引入 strconv 的新依赖面，
// 与本包其它地方拼审计串的写法一致）。
func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
