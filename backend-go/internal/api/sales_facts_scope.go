// ============ sales_facts_scope.go · 职责说明 ============
// 四期 W1：销售事实集**作用域解析器**与页面上下文头（施工图
// 个人对外沟通方案/施工_四期_销售主线与架构收敛_20261112.md §4.1.3-①②④）。
//
// 为什么需要这个文件（三期留下的三个缺口，逐条对应 §4.1.2）：
//  1. 匿名访客恒无事实：三期 attachSalesFacts 只认登录会话（u == nil 直接 return），
//     而托管公开页 /p/{tc}/{pc} 的访客**是匿名的** ⇒ inbound 主战场上事实集永远是空集；
//  2. 多产品串台：三期读口恒传 productID=""（＝该租户全部产品），
//     一个租户两个产品时 A 产品的 MOQ 会被拿去回答 B 产品的问题；
//  3. 开闸无粒度：只有"租户两把开关"，没有"这个请求能不能安全地注入"的判据。
//
// ★ 三档口径（这张表就是判据本体，代码与 F1 的表驱动测试必须逐行对应）：
//
//	| 输入                                                   | TenantID  | ProductID | Reason            |
//	| 开关未全开                                             | 0         | ""        | disabled          |
//	| 登录 + /p/{tc}/{pc} + 产品存在 + live + 同租户          | effTenant | p.ID      | product_page      |
//	| 匿名 + 同上（同租户校验按产品自身 p.TenantID）           | p.TenantID| p.ID      | product_page_anon |
//	| 登录 + 无作用域 + 租户产品数 == 1                      | effTenant | 唯一产品   | single_product    |
//	| 登录 + 无作用域 + 产品数 >= 2                          | effTenant | ""        | ambiguous         |
//	| 登录 + 无作用域 + 产品数 == 0                          | effTenant | ""        | denied            |
//	| 匿名 + 无作用域                                        | 0         | ""        | denied            |
//	| 产品不存在 / 非 live / 跨租户                          | 0         | ""        | denied            |
//
// ★ 为什么匿名档不校验访问域名：公开页 /p/{tc}/{pc} 的设计就是"任何域名都能看"，
// 跨租户拼接由 store.GetProductByCodes 的两段等值查询本身挡掉（两段码必须同时命中同一行）。
// 再加一层域名校验会让"主站域名访问租户页"这条合法路径失效（F-79 那族教训：公开首屏不许被打成 403）。
//
// ★ 页面头是**链路卫生**、不是安全边界（§4.1.3-③-c）：它由浏览器发出，任何人都能改，
// 最坏后果只是"访客在自己的会话里让某个产品的**已公开事实**注入自己的答复"——
// 那些事实本来就印在那个公开页上（准入读口硬带 status='confirmed' AND visibility='public' AND locked=0），
// 坏的只是"答案描述错产品"。真正的边界是登录态的租户一致性校验（F6）。
//
// 本文件只读配置与产品行，不写任何业务数据；所有计数只服务 /api/health 的 sales_facts 段，
// **绝不出事实值、产品名、租户名**（同 §一·12 usdt_watch 纪律）。
// ================================================
package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"translator/internal/observability"
	"translator/internal/store"
)

// productPageHeader 页面上下文头（前端 assist.ts 的 PAGE_HEADER 与此逐字对应）。
// ★ 只有 greet/chat 送——history/features 不产生答复，注入事实无处可用（见 assist.ts 的豁免注释）。
const productPageHeader = "X-LangCross-Page"

// 作用域档位词（对外排障契约：/api/health 的 sales_facts 段与日志的 reason 字段同源）。
const (
	salesScopeDisabled        = "disabled"          // 两把开关未全开（缺省态）
	salesScopeProductPage     = "product_page"      // 登录 + 显式产品页
	salesScopeProductPageAnon = "product_page_anon" // 匿名 + 显式产品页
	salesScopeSingleProduct   = "single_product"    // 登录 + 租户只有一个产品（自动定档）
	salesScopeAmbiguous       = "ambiguous"         // 登录 + 多产品且无作用域（fail-closed）
	salesScopeDenied          = "denied"            // 其余一律拒绝
)

// salesFactsScope 本轮事实注入的唯一作用域判据（纯数据，不碰 DB 之外的副作用）。
// TenantID 为 0 或 ProductID 为空 ⇒ **不可注入**（绝不回退"全租户"那档）。
type salesFactsScope struct {
	TenantID  int64  // 0 = 不可注入
	ProductID string // "" = 不可注入
	Lang      string // 本轮作答语言码（seoLangFromRequest 的现读值）
	Reason    string // 上面六个档位词之一
}

// injectable 作用域是否可用（唯一判据：租户与产品同时有效）。
func (sc salesFactsScope) injectable() bool {
	return sc.TenantID > 0 && strings.TrimSpace(sc.ProductID) != ""
}

// salesFactsCounters 销售事实注入的进程内计数（只服务 /api/health 的 sales_facts 段）。
// ★ 只存计数、不存任何事实/产品/租户的可读文本：健康面匿名可达（同 #42 口径）。
type salesFactsCounters struct {
	scopeOK        atomic.Int64 // product_page
	scopeAnon      atomic.Int64 // product_page_anon
	scopeAmbiguous atomic.Int64 // ambiguous
	scopeDenied    atomic.Int64 // denied
	injected       atomic.Int64 // 实际进入签名头的事实条数累计
}

// stripProductPageHeader 删除入站请求上的页面上下文头。
//
// ★ 这是**链路卫生**（本进程只认自己解析出的作用域，不把客户端带来的那份继续往下传），
// 不是安全边界——伪造它能拿到的只有"别人已经公开的事实"，见本文件头 ③-c 的记录。
// 与 stripSalesFactsHeaders 放同一族位置（同一族的删头动作不许分住两处）。
func stripProductPageHeader(h http.Header) {
	if h == nil {
		return
	}
	h.Del(productPageHeader)
}

// pageContextOf 取客户端自报的页面上下文，优先级：头 > query `page` > POST body 的 `page`。
// ★ 客户端送两处（头 + 老字段）是为了"头是唯一正式通道，body/query 只是兼容老包"：
// 未来若有人把 body 字段改名，头这条腿不该跟着一起断。
func pageContextOf(r *http.Request) string {
	if r == nil {
		return ""
	}
	if v := strings.TrimSpace(r.Header.Get(productPageHeader)); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.URL.Query().Get("page")); v != "" {
		return v
	}
	if r.Method == http.MethodPost && r.Body != nil {
		return pageFromJSONBody(r)
	}
	return ""
}

// pageFromJSONBody 从 POST body 里取 `page`，**无论成败都把同一具 body 原样放回**——
// 本请求后续还要把它转发给上游，读走不放回等于把访客消息吃掉。
//
// 用 MultiReader(已读字节, 原 body 剩余段) 复原而不是「再包一层 bytes.Reader」：
// body 超过 assistOpenMaxBody 时只读了前缀，后一种写法会把超限那一段**静默截掉**，
// 上游于是收到一具坏 JSON（400），而屏幕上的病因指向"挂件坏了"。
// 读取上限与 assistOpenMaxBody 同档（上游那边还有 MaxBytesReader 兜底）。
func pageFromJSONBody(r *http.Request) string {
	orig := r.Body
	raw, err := io.ReadAll(io.LimitReader(orig, assistOpenMaxBody))
	r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(raw), orig))
	if err != nil {
		return ""
	}
	var p struct {
		Page string `json:"page"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return ""
	}
	return strings.TrimSpace(p.Page)
}

// productPageCodes 把页面上下文解析成 (租户段, 页面段)。
// 只认 /p/{tc}/{pc} 这一形态：段数必须恰好 3、首段必须是 "p"、两段都非空。
// 兼容 query/fragment 与结尾斜杠，其余形态一律不算产品页（⇒ 走 single_product / ambiguous 那几档）。
func productPageCodes(page string) (string, string, bool) {
	p := strings.TrimSpace(page)
	if p == "" {
		return "", "", false
	}
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	p = strings.TrimSuffix(p, "/")
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	if len(segs) != 3 || segs[0] != "p" {
		return "", "", false
	}
	tc, pc := strings.TrimSpace(segs[1]), strings.TrimSpace(segs[2])
	if tc == "" || pc == "" {
		return "", "", false
	}
	return tc, pc, true
}

// resolveSalesFactsScope 按「显式产品页 > 单产品租户 > 其余 fail-closed」三档解析作用域。
// 参数：r=入站请求（只读会话与租户上下文）；page=调用方在 stripProductPageHeader **之前**捕获的
// 客户端自报页面（pageContextOf 已收口优先级）；返回：作用域（不可注入时 TenantID=0/ProductID=""）。
//
// ★ 解析只做一次、由 handleAssistOpenProxy 转给事实注入与归因回写两条路径：
// 两处各算一次会在 TTL 窗口内给出不同答案（一次 product_page、一次 denied），
// 于是"事实按 A 产品注入、归因记成空产品"，而两条路径各自都"正常"（AGENTS §三 那族）。
func (s *Server) resolveSalesFactsScope(r *http.Request, page string) salesFactsScope {
	sc := salesFactsScope{Lang: seoLangFromRequest(r), Reason: salesScopeDisabled}
	if s == nil || s.Store == nil || r == nil {
		return sc
	}
	// 第一道：两把开关必须同时开（缺省关）。关闭态连"解析"都不该被当成"我们会注入"。
	if !s.salesFactsGateFullyOpen() {
		return sc
	}
	defer func() { s.salesFactsCountersAdd(sc.Reason) }()

	// ① 显式产品页
	if tc, pc, ok := productPageCodes(page); ok {
		p, found, err := s.Store.GetProductByCodes(tc, pc)
		if err != nil {
			observability.Warn(r.Context(), "产品页作用域解析失败，按不可注入处理",
				"tenant_code", tc, "page_code", pc, "err", err)
			sc.Reason = salesScopeDenied
			return sc
		}
		if !found || p.Status != store.ProductStatusLive {
			sc.Reason = salesScopeDenied
			return sc
		}
		u := s.authUser(r)
		if u != nil && u.ID > 0 {
			// 登录态：租户一致性是**真边界**（F6）。跨租户 ⇒ 空集，绝不注入别人产品的事实。
			if tid := s.effTenant(r, u); tid > 0 && tid == p.TenantID {
				sc.TenantID, sc.ProductID, sc.Reason = tid, p.ID, salesScopeProductPage
				return sc
			}
			sc.Reason = salesScopeDenied
			return sc
		}
		// 匿名：租户取**产品自身**的（不看访问域名，理由见文件头）。
		sc.TenantID, sc.ProductID, sc.Reason = p.TenantID, p.ID, salesScopeProductPageAnon
		return sc
	}

	// ② 无作用域：只有登录态才有"租户"可言
	u := s.authUser(r)
	if u == nil || u.ID <= 0 {
		sc.Reason = salesScopeDenied
		return sc
	}
	tid := s.effTenant(r, u)
	if tid <= 0 {
		// 平台上下文（超管未选租户）没有产品事实语义
		sc.Reason = salesScopeDenied
		return sc
	}
	sc.TenantID = tid
	// ★ 复用现成读口 ListProducts(tid, 1, 2)：只要 total，**不为这个判据新增 CountProducts**
	// （AGENTS §一·1／§27：新方法要按域新建文件，而一条现成读口就够）。
	products, total, err := s.Store.ListProducts(tid, 1, 2)
	if err != nil {
		observability.Warn(r.Context(), "租户产品数读取失败，按不可注入处理", "tenant_id", tid, "err", err)
		sc.Reason = salesScopeDenied
		return sc
	}
	switch {
	case total == 0:
		sc.Reason = salesScopeDenied
	case total == 1:
		// 单产品租户自动定档：没有页面上下文也能安全注入（产物就是唯一那一份档案）。
		if len(products) == 1 {
			sc.ProductID = products[0].ID
			sc.Reason = salesScopeSingleProduct
			return sc
		}
		// total 说 1 而列表空 ⇒ 读口自相矛盾，按不可注入处理（不猜）。
		sc.Reason = salesScopeDenied
	default:
		// 多产品且无作用域 ⇒ fail-closed（绝不回退"全租户"，那正是三期串台的那一行）。
		sc.Reason = salesScopeAmbiguous
	}
	return sc
}

// salesFactsGateWord 两把开关的三态词：on（全开）/ half（只开一把）/ off（都没开或读不到）。
// 口径与 buildSalesFactsSet 的第二道判据同源：只开一把不算全开（A15）。
func (s *Server) salesFactsGateWord() string {
	if s == nil || s.Store == nil {
		return "off"
	}
	inj, _ := s.Store.GetConfig("sales_facts_injection_enabled")
	guard, _ := s.Store.GetConfig("sales_claim_guard_enabled")
	injOn, e1 := strconv.ParseBool(inj)
	guardOn, e2 := strconv.ParseBool(guard)
	if e1 != nil || e2 != nil || (!injOn && !guardOn) {
		return "off"
	}
	if injOn && guardOn {
		return "on"
	}
	return "half"
}

// salesFactsGateFullyOpen 两把开关是否同时开（作用域解析的第一道）。
func (s *Server) salesFactsGateFullyOpen() bool {
	return s.salesFactsGateWord() == "on"
}

// salesFactsCountersAdd 按档位词累加计数（disabled 不计：它答的是"闸没开"，不是一次作用域判定）。
func (s *Server) salesFactsCountersAdd(reason string) {
	switch reason {
	case salesScopeProductPage:
		s.salesFactsStats.scopeOK.Add(1)
	case salesScopeProductPageAnon:
		s.salesFactsStats.scopeAnon.Add(1)
	case salesScopeAmbiguous:
		s.salesFactsStats.scopeAmbiguous.Add(1)
	case salesScopeDenied:
		s.salesFactsStats.scopeDenied.Add(1)
	}
}

// salesFactsHealth /api/health 的 sales_facts 段：状态词 + 五个计数。
// ★ 绝不出事实值/产品名/租户名，也不出 product_id（那会把公开面变成枚举器）。
func (s *Server) salesFactsHealth() map[string]interface{} {
	out := map[string]interface{}{
		"gate":            s.salesFactsGateWord(),
		"scope_ok":        int64(0),
		"scope_anon":      int64(0),
		"scope_ambiguous": int64(0),
		"scope_denied":    int64(0),
		"injected":        int64(0),
	}
	if s == nil {
		return out
	}
	out["scope_ok"] = s.salesFactsStats.scopeOK.Load()
	out["scope_anon"] = s.salesFactsStats.scopeAnon.Load()
	out["scope_ambiguous"] = s.salesFactsStats.scopeAmbiguous.Load()
	out["scope_denied"] = s.salesFactsStats.scopeDenied.Load()
	out["injected"] = s.salesFactsStats.injected.Load()
	return out
}
