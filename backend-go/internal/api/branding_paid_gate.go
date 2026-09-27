// ============ 本文件职责中文说明 ============
// 品牌定制「展示侧」的付费闸与 30 天宽限期（★ F-75，2026-09-27 〇-X 用户拍板：
// 「套餐到期后，宽限期 30 天＋站内信通知」）。
//
// 修的是什么：保存侧一直有闸（tenantBrandingUnlocked：企业根租户／付费套餐在效／超管授权
// 三选一），但**出栈侧一个判定都没有**——套餐到期后客户的登录页照样挂着租户品牌，
// 等于付费权益到期不回收，是收入漏口。
//
// 三条展示判定（任一满足即向访客展示品牌）：
//
//	① 企业根租户（is_personal=false）或超管显式授权 → 恒展示（与保存侧同源，不看到期）；
//	② 付费套餐在效内 → 展示；
//	③ 套餐已到期、但仍在品牌展示宽限期内（brand_grace_expires_at 未到）→ 展示。
//
// 为什么不把闸做成"回收即清空数据"：库里品牌字段原样保留，只改出栈视图。
// 客户续费当天品牌立刻恢复展示，无需重传 logo 与背景图；这也让"到期就删配置"
// 这种不可逆动作彻底不可能发生。
//
// ★ 关键豁免：本闸**只作用于匿名访客视角**。租户自己的管理员与平台超管永远看到真值——
//
//	否则后台品牌设置页会把"被隐藏的空字段"当作现值载入，客户点一次保存就把真配置覆盖没了
//	（读写同源的接口绝不能让展示闸污染写侧回显）。
//
// 触达（站内信，两条各一次）：进入宽限期一条（说清"何时停止展示"）、宽限结束回收一条。
// 一律「先置位标记、后发通知」，且整轮跑在 watchdog 的 runExclusive("subscription-scan")
// 单跑者里 ⇒ 多实例不重复发（口径同 #74 订阅宽限）。
//
// 配置优先序（AGENTS §一·3：环境变量 > 数据库配置 > 代码默认）：
//
//	env BRAND_GRACE_DAYS > system_config.brand_grace_days > 30
//
// 配 0＝关闭宽限（到期即回收展示，仍发一条回收通知）；非法值回落 30，上限 90 天防呆。
package api

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"translator/internal/auth"
	"translator/internal/observability"
	"translator/internal/store"
	"translator/internal/tenant"
)

// defaultBrandGraceDays 品牌展示宽限缺省天数（用户 2026-09-27 拍板 30 天）。
const defaultBrandGraceDays = 30

// cfgBrandGraceDays system_config 键名（管理台暂不做表单：这是运营侧低频参数，
// 与订阅续费宽限 subscription_grace_days 同一口径——只能命令行/DB 改，改库即生效不发版）。
const cfgBrandGraceDays = "brand_grace_days"

// brandGraceDays 读取品牌展示宽限天数：环境变量 > 库配置 > 默认 30，非法值一律回落。
// 复用 subscription_grace.go 的 clampGraceDays（[0,90]）：0＝不留宽限、立即回收。
func (s *Server) brandGraceDays() int {
	if v := strings.TrimSpace(os.Getenv("BRAND_GRACE_DAYS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return clampGraceDays(n, defaultBrandGraceDays)
		}
	}
	if s.Store != nil {
		if v, _ := s.Store.GetConfig(cfgBrandGraceDays); strings.TrimSpace(v) != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				return clampGraceDays(n, defaultBrandGraceDays)
			}
		}
	}
	return defaultBrandGraceDays
}

// brandGraceDeadline 解析权限快照里的品牌宽限截止时刻。
// 返回 (deadline, 是否已到期前)：valid=false 表示不在宽限期或值不可解析（脏值按不展示处理，
// 宁可少展示也不给到期租户白送品牌——方向与 F-41 的"保守"一致）。
func brandGraceDeadline(perms *tenant.Perms, now time.Time) (time.Time, bool) {
	if perms == nil {
		return time.Time{}, false
	}
	v := strings.TrimSpace(perms.BrandGraceExpiresAt)
	if v == "" {
		return time.Time{}, false
	}
	g, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, false
	}
	if !now.Before(g) {
		return g, false
	}
	return g, true
}

// tenantBrandingDisplayUnlocked 展示侧综合判定：保存侧三条件（tenantBrandingUnlocked）
// 任一成立，或处于品牌展示宽限期内。宽限只补"付费到期"这一种情形——
// 企业根租户与超管授权本就不依赖套餐，走上面那条已经为真。
func tenantBrandingDisplayUnlocked(s *Server, tid int64) bool {
	if tid <= 0 {
		return false
	}
	if tenantBrandingUnlocked(s, tid) {
		return true
	}
	if s.Store == nil {
		return false
	}
	perms, err := s.Store.GetTenantPerms(tid)
	if err != nil {
		return false
	}
	_, ok := brandGraceDeadline(perms, time.Now())
	return ok
}

// brandingViewerExempt 判定当前请求是否属于「必须看到品牌真值」的内部视角：
// 平台超管（含 ?tenant_id= 预览任意租户）与该租户自己的租户管理员及以上。
// 这类视角若也被闸挡住，后台设置页会把空字段当现值载回，客户一保存就覆盖掉真配置。
func brandingViewerExempt(s *Server, r *http.Request, tid int64) bool {
	if s == nil || r == nil {
		return false
	}
	u := s.authUser(r)
	if u == nil {
		return false
	}
	if auth.IsSuperAdmin(u) {
		return true
	}
	return u.TenantID == tid && auth.RoleLevel(u.Role) >= 3
}

// hideTenantBrandVisuals 把租户品牌视觉字段在出栈前抹掉（保留企业身份字段不动）。
// 只动视觉四组：品牌名（含多语种与英文）、logo、首页背景与样式、登录卡片位置与布局。
// 保留 domain／name／code／industry／dedicated_register：这些不是"品牌权益"，
// 且注册页与排障要靠它们定位归属。
func hideTenantBrandVisuals(m map[string]interface{}) {
	for _, k := range []string{
		"brand_name", "brand_names", "brand_name_en", "brand_logo",
		"brand_home_bg", "brand_home_bg_style", "brand_login_card_pos", "brand_login_layout",
	} {
		m[k] = ""
	}
	// brand_paid 本来就是 false（否则不会走到这里），显式钉一遍防止后续改动引入矛盾值
	m["brand_paid"] = false
}

// brandingHasVisuals 判断租户是否真的配过品牌（任一视觉字段非空）。
// 用途：没配过品牌的租户到期时不该收到「品牌将停止展示」——那是一条纯噪音通知。
func brandingHasVisuals(t *tenant.Tenant) bool {
	if t == nil {
		return false
	}
	return strings.TrimSpace(t.BrandName) != "" || strings.TrimSpace(t.BrandNameEn) != "" ||
		strings.TrimSpace(t.BrandLogo) != "" || strings.TrimSpace(t.BrandHomeBg) != "" ||
		strings.TrimSpace(t.BrandHomeBgStyle) != ""
}

// brandGraceApplies 判定该租户到期后是否**会因失去付费身份而丢品牌展示**：
// 个人（非企业根）租户 + 套餐是付费包 + 未被超管单独授权 + 确实配过品牌。
// 四个条件任一不满足就不起宽限、也不发通知（企业根租户本就不靠付费解锁品牌）。
// 参数 code=本次被摘除的套餐编码。
func brandGraceApplies(s *Server, t *tenant.Tenant, code string) bool {
	if t == nil || t.ID <= 0 || s.Store == nil {
		return false
	}
	if !t.IsPersonal {
		return false // 企业根租户：品牌与套餐无关，到期不动展示
	}
	if tenantBrandingGranted(s, t.ID) {
		return false // 超管显式授权：回收与否由授权决定，不由宽限期决定
	}
	if !brandingHasVisuals(t) {
		return false
	}
	if pkg, err := s.Store.GetPackageByCode(t.ID, code); err == nil && pkg != nil && pkg.PType == store.PackageFree {
		return false // 免费包到期不欠费，谈不上"权益回收"
	}
	return true
}

// startBrandGraceAtExpiry 订阅身份被摘除的当轮起算品牌展示宽限并发出第一条站内信。
// 参数 t=租户实体（摘除前快照），code=被摘除的套餐编码，now=本轮扫描时刻。
//
// 调用点只有一个：watchdog.go runSubscriptionScan 的 ExpirePackage 成功分支之后——
// 必须在那之后，因为 ExpirePackage 会整体复位 permissions 里的订阅与宽限键，
// 先写会被连带清掉（这条顺序是本功能唯一的隐性前提，改扫描逻辑时勿调换）。
//
// 宽限 0 天＝不进入宽限：不发「将停止展示」，直接落一条「已停止展示」，
// 保证配置成 0 的运营环境下客户仍被告知发生了什么。
func (s *Server) startBrandGraceAtExpiry(t *tenant.Tenant, code string, now time.Time) {
	if s.Store == nil || !brandGraceApplies(s, t, code) {
		return
	}
	days := s.brandGraceDays()
	if days <= 0 {
		// 无宽限：直接按"已回收"触达（置位在前、发送在后，同下方口径）。
		// ★ 不写 brand_grace_expires_at：0 天起算等于落一个「已过期」的脏键，
		//   既会被日扫再处理一遍，也会在续费后留下需要清理的遗留状态。
		_ = s.Store.MarkBrandGraceNotice(t.ID, store.BrandGraceNoticeEnd)
		s.notifyTenantAdmins(t.ID, "品牌定制已停止展示",
			"商业包「"+code+"」已到期且未续订，企业品牌（名称／Logo／登录页背景）已停止对客户的展示。"+
				"品牌配置原样保留，重新订阅后即时恢复，无需重新上传。续订入口：管理后台 → 套餐与账单。")
		return
	}
	until := now.AddDate(0, 0, days)
	if err := s.Store.SetBrandGrace(t.ID, until); err != nil {
		observability.Warn(context.Background(), "品牌展示宽限期落库失败（不影响订阅摘除）",
			"tenant", t.ID, "err", err.Error())
		return
	}

	// ★ 先置位再发送：两步之间崩溃只会少一条提醒，反序则每轮扫描重复轰炸。
	if err := s.Store.MarkBrandGraceNotice(t.ID, store.BrandGraceNoticeStart); err != nil {
		return
	}
	s.notifyTenantAdmins(t.ID, "品牌定制进入 "+strconv.Itoa(days)+" 天展示宽限期",
		"商业包「"+code+"」已于 "+now.Format("2006-01-02")+" 到期。企业品牌将在 "+
			until.Format("2006-01-02")+" 前继续对客户展示（宽限期 "+strconv.Itoa(days)+" 天）；"+
			"届时仍未续订，客户的登录页将恢复平台默认外观（您的品牌配置不会删除，续费当天即恢复）。"+
			"续订入口：管理后台 → 套餐与账单。")
	s.notifyBots("品牌宽限起算",
		"租户 #"+strconv.FormatInt(t.ID, 10)+"（"+t.Name+"）订阅到期，品牌展示宽限至 "+
			until.Format("2006-01-02")+"（"+strconv.Itoa(days)+" 天）。")
}

// runBrandGraceScan 品牌宽限期日扫（由 watchdog 的 scanDaily 调用，整体已在单跑者内）：
//
//	① 订阅已续期（付费身份回来了）→ 清掉遗留宽限键，不发任何通知；
//	② 宽限到期 → 回收展示（展示判定天然转 false，无需写数据），发一条站内信并清键。
//
// 为什么单独一轮扫描：runSubscriptionScan 在循环开头就跳过「无订阅」租户，
// 而进入本宽限期的租户恰恰是 package_code 已被清空的租户，塞进那个循环永远走不到。
func (s *Server) runBrandGraceScan() {
	if s.Store == nil || s.Ten == nil {
		return
	}
	tenants, err := s.Ten.List()
	if err != nil {
		return
	}
	now := time.Now()
	for _, t := range tenants {
		if t == nil || t.ID <= 0 {
			continue
		}
		perms := tenant.ParsePerms(t.Permissions)
		if perms == nil || strings.TrimSpace(perms.BrandGraceExpiresAt) == "" {
			continue // 未在宽限期：绝大多数租户在这一行返回
		}
		deadline, inGrace := brandGraceDeadline(perms, now)
		if tenantBrandingPackagePaid(s, t.ID) {
			// ① 已续费：宽限期使命完成，清键（不通知——客户自己看得到品牌还在）
			_ = s.Store.ClearBrandGrace(t.ID)
			continue
		}
		if inGrace {
			continue // ② 宽限期内：展示照常，等下一轮
		}
		// ③ 宽限结束：先置位再通知，随后清键（键清掉即"回收已发生且已告知"的终态）
		if !perms.BrandGraceNoticeEnd {
			_ = s.Store.MarkBrandGraceNotice(t.ID, store.BrandGraceNoticeEnd)
			s.notifyTenantAdmins(t.ID, "品牌定制已停止展示",
				"企业品牌展示宽限期已于 "+deadline.Format("2006-01-02")+" 结束，客户的登录页已恢复平台默认外观。"+
					"品牌配置原样保留，重新订阅后即时恢复，无需重新上传。续订入口：管理后台 → 套餐与账单。")
			s.notifyBots("品牌宽限结束回收",
				"租户 #"+strconv.FormatInt(t.ID, 10)+"（"+t.Name+"）品牌展示宽限结束，出栈已回落平台默认。")
		}
		_ = s.Store.ClearBrandGrace(t.ID)
	}
}
