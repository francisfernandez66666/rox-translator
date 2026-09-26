// ============================================================================
// route_auth_gate_test.go — 路由鉴权结构性闸门（★ #56-② 租户隔离收口，2026-09-22）
//
// 背景（全量 UAT 报告 §4.1-2）：本服务**没有全局鉴权中间件**，`withTenant` 只负责解析租户，
// 鉴权由各 handler 自行调用 `s.authUser / s.requireAdminUser / s.requireTenantAdmin /
// s.requireSuperAdmin / s.authenticateAPIKey*` 完成。更糟的是未登录且无 API Key 的请求会被
// 兜底注入**默认租户 1（rox）**（server.go withTenant 第 4 步），于是「新 handler 漏写鉴权」
// 不会 401，而是静默以 rox 身份读写数据——历史上 P0-2 匿名工单漏洞正是这一形态。
//
// 人工评审挡不住这种漏（评审者只看得到那一个 handler），故改为**结构断言**：
// 静态解析本包全部 `s.mux.HandleFunc(路径, s.处理器)` 注册点，对每个处理器在包内做调用闭包
// 搜索（沿同包方法/函数最多 4 层），要求命中「鉴权动作」；命不中就必须出现在
// publicRouteAllowlist 里并写明公开理由，否则闸门红灯。
//
// 新增路由的两种正确姿势（闸门注释即为此而写）：
//  1. 需要身份：handler（或其调用的同包函数）里调用上面任一鉴权助手；
//  2. 确实公开：把路径加进 publicRouteAllowlist 并写清「为什么可以匿名」，由人评审。
//
// 已知边界（诚实记录，不当成完备）：
//   - 只识别 `s.mux.HandleFunc` 形态的注册点（本包唯一形态）；
//   - 只认包内调用闭包，跨包鉴权（如内部 service 层再判一次）不计入；
//   - 动态注册/包装 http.Handler 的写法会被记为「无法解析」并同样要求进白名单。
//
// 运行：go test ./internal/api/ -run TestRouteAuthGate
// ============================================================================
package api

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// authGateHelpers 视为「执行了鉴权」的函数名（方法名去掉接收者后逐字匹配）。
// 口径：必须**真的会中断请求或返回错误**的凭证校验，不含 `effTenant/tenant.FromContext`
// 这类「读已注入值」的函数——那正是兜底租户 1 会被静默用上的坑，用它过闸等于没闸。
// 名单只放**当前代码里确实存在**的凭证校验函数；新增鉴权助手时在此登记一行。
var authGateHelpers = map[string]bool{
	"authUser":                  true, // JWT 取当前用户（handler 普遍再判 nil 后 401）
	"requireAdminUser":          true, // 超管
	"requireTenantAdmin":        true, // 租户管理员及以上
	"requireSuperAdmin":         true, // 平台超管
	"authenticateAPIKey":        true, // 开放 API Key 鉴权
	"authenticateAPIKeyNoTouch": true, // 同上（不计日配额的变体）
	// 以下为**非会话类凭证**：服务间回调 / IdP / Caddy 走 Token 或签名，不是 JWT 登录态。
	"scimGuard":              true, // SCIM v2：Bearer Token → 租户配置（未过即 401 并返回 nil）
	"caddyAskAuthorized":     true, // Caddy on_demand ask：回环/CIDR 白名单，越权直接 403
	"constantTimeTokenEqual": true, // 仓库内**唯一**的凭证常量时间比较原语（支付回调、告警入口内联验凭证）
}

// publicRouteAllowlist 确实允许匿名访问的路径 → 公开理由（缺理由视为不合格）。
// 这里的每一条都是**人工评审过的例外**（2026-09-22 逐条读 handler 确认），
// 新增时必须写清「为什么可以匿名」以及「有无限流/凭证兜底」，由人评审。
var publicRouteAllowlist = map[string]string{
	// —— 静态资源与站点外壳 ——
	"/":                     "SPA 前端外壳（纯静态 index.html，无数据访问）",
	"/office/manifest.xml":  "Office 加载项清单：静态 XML，Office 客户端匿名拉取",
	"/office/taskpane.html": "Office 任务窗格页面：静态 HTML 外壳，数据由已鉴权 API 提供",
	"/docs/terms":           "服务条款公开页",
	"/docs/sla":             "SLA 公开页",
	"/docs/privacy":         "隐私政策公开页",
	// ★ F-69（2026-09-26 批 I-8）：手册 PDF 公网下载面。语种码先过 normalizeMailLang 的
	//   12 码白名单（白名单外一律 400，同时也是路径穿越闸门），内容只有对外产品说明、
	//   零租户数据；与 /docs/terms|sla|privacy 同级公开，故不加登录门槛。
	"/docs/manual/": "12 语种《产品手册》PDF 公开下载：只读 manual_pdf_dir 下的 {lang}.pdf，无租户数据、语种码过白名单",
	// ★ F-46（2026-09-26 批 I-9）：品牌图改「落静态件 + 只注入 URL」后新增的直出面。
	//   只读 <UserDataDir>/brand/ 下单层文件、扩展名过白名单、字节还要过魔数核验，
	//   零租户数据零鉴权语义（品牌本来就是给该域名访客看的）；缺件回 404 JSON 而非 SPA 壳。
	"/brand/":                   "品牌 Logo/首页背景静态件直出：内容寻址文件名、只读 brand 目录单层件、格式白名单+魔数核验",
	"/openapi/docs":             "开放 API 文档页（对外公开，内容本身即产品说明）",
	"/openapi/v1.json":          "开放 API OpenAPI 规范 JSON（供 SDK 生成，公开）",
	"/api/skills":               "已启用技能列表：仅能力清单，不含租户数据",
	"/api/translation/langs":    "支持语种字典：落地页/翻译页下拉数据源，公开",
	"/api/footer-links":         "落地页页脚链接配置：营销可编辑文案，公开读",
	"/api/plans":                "套餐与定价（★ 落地页价格走此接口，匿名用户需看到价格）",
	"/api/register/industries":  "注册页行业字典",
	"/api/register/personas":    "注册页角色字典",
	"/api/auth/register-config": "注册页运营配置（开关/文案），公开",
	// —— 认证流程本身（匿名是前提；防护靠限流与凭证校验，不靠会话） ——
	"/api/auth/login":           "登录入口：匿名可达是前提，防爆破由 IP/账号限流承担",
	"/api/auth/register":        "注册入口：同上（含邮箱验证与限流）",
	"/api/auth/forgot-password": "找回密码：匿名 + 邮件验证码 + 限流",
	"/api/auth/reset-password":  "重置密码：凭邮件验证码换取，无验证码不放行",
	"/api/auth/email-code":      "邮箱验证码校验/重发：匿名 + 限流",
	"/api/auth/sso/exchange":    "SSO 一次性 ticket 换 JWT：ticket 本身即短时凭证",
	"/api/sso/login":            "SSO 跳转到 IdP：无会话时的入口",
	"/api/sso/callback":         "SSO 回跳：校验 state 并签发 JWT，匿名可达是协议要求",
	"/api/sso/providers":        "SSO 提供方列表：登录页渲染所需，公开",
	// —— 运维探针与对外服务（无租户数据 / 由外部系统调用） ——
	"/api/health": "存活探针：K8s/Caddy/看门狗使用，只返回状态与版本",
	// ★ #42（2026-09-22）探针拆分新增两条（决策口径见 health_probes.go 文件头）：
	//   监控/编排器必须匿名可达，且**绝不能**因为要鉴权而把探针请求变成带会话的调用——
	//   探针带凭证只会让它把「鉴权链路故障」误报成「服务不可用」。
	"/livez":         "存活探针：只回 status/version，零依赖检查（依赖故障不得让它翻红，见 health_probes.go）",
	"/readyz":        "就绪探针：只输出 store/distributed 粗粒度状态词，不含租户数据与内网拓扑（错误原文不外泄）",
	"/status":        "公开状态页数据：仅服务状态，不含租户数据",
	"/metrics":       "Prometheus 抓取端点：默认关闭，需 METRICS_TOKEN Bearer 或显式 METRICS_PUBLIC=1",
	"/api/qr-image/": "支付/邀请二维码图片读取：匿名购买者扫码页需要；路径已做 basename 归一，只读 _qr 目录",
	"/api/lead":      "营销留资（留言获取方案）：匿名 POST，IP 限流 + 蜜罐 + 可选 Turnstile",
	// SCIM 协议发现端点：RFC 7644 规定的静态元数据（Schema / ServiceProviderConfig），
	// 不含任何租户数据；IdP 在配置阶段就要能匿名读到它才能发现鉴权方式。
	// 真正的用户/组同步端点走 s.scimGuard（Bearer Token → 租户），不在本白名单。
	"/api/scim/v2/Schemas":               "SCIM 静态 Schema 描述：RFC 7644 发现端点，无租户数据",
	"/api/scim/v2/ServiceProviderConfig": "SCIM 服务能力描述：RFC 7644 发现端点，无租户数据",
}

// TestRouteAuthGate 静态断言：本包每条注册路由要么执行鉴权，要么在公开白名单里带理由。
func TestRouteAuthGate(t *testing.T) {
	routes, funcs, err := scanAuthGatePackage(".")
	if err != nil {
		t.Fatalf("解析 internal/api 失败: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("未解析到任何 s.mux.HandleFunc 注册点，闸门本身失效（检查解析逻辑或注册形态是否变更）")
	}
	var missing []string
	for _, rt := range routes {
		if _, ok := publicRouteAllowlist[rt.pattern]; ok {
			continue
		}
		if rt.handler == "" {
			missing = append(missing, fmt.Sprintf("%s（注册参数不是 s.xxx 方法，无法静态判定）", rt.pattern))
			continue
		}
		if authGateReachable(funcs, rt.handler) {
			continue
		}
		missing = append(missing, fmt.Sprintf("%s → %s", rt.pattern, rt.handler))
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("★ 鉴权结构闸门红灯：%d 条路由既不鉴权也不在公开白名单内。\n"+
			"未登录请求会被 withTenant 兜底成默认租户 1（rox），这类路由等于匿名可读写：\n  %s\n"+
			"修法：① handler 内调用 s.authUser/requireAdminUser/requireTenantAdmin/authenticateAPIKey* 等鉴权助手；\n"+
			"     ② 确属公开接口则加入 publicRouteAllowlist 并写明匿名可达的理由（需人工评审）。",
			len(missing), strings.Join(missing, "\n  "))
	}

	// —— 闸门自检（防「永远绿灯」的结构性失效，与 db/guard_test.go 的反影子迁移同思路）——
	// ① 正对照：已知受保护路由必须被解析到，且其调用闭包确实命中鉴权助手；
	// ② 反对照：已知公开 handler 必须判为「不鉴权」，否则说明判据形同虚设；
	// ③ 白名单不允许残留已删除的路径——僵尸例外会把日后**同名**的新路由静默放行。
	pat := map[string]string{}
	for _, rt := range routes {
		if rt.handler != "" {
			pat[rt.pattern] = rt.handler
		}
	}
	if h, ok := pat["/api/admin/orgs"]; !ok {
		t.Errorf("自检失败：未解析到 /api/admin/orgs（注册形态变更或解析器失效）")
	} else if !authGateReachable(funcs, h) {
		t.Errorf("自检失败：受保护路由 /api/admin/orgs → %s 未命中鉴权助手，判据失效", h)
	}
	if authGateReachable(funcs, "handleSPA") {
		t.Error("自检失败：handleSPA 被判为「已鉴权」，说明鉴权判据过宽")
	}
	for p := range publicRouteAllowlist {
		if _, ok := pat[p]; !ok {
			t.Errorf("publicRouteAllowlist 残留已不存在的路由 %q：请删除，否则日后同名新路由会被静默放行", p)
		}
	}
}

// authGateReachable 从入口方法出发做包内调用闭包搜索（BFS，最深 4 层），命中鉴权助手即真。
// 深度上限是刻意的：再深的间接调用通常已离开「请求准入」语义（如落库、计费），
// 搜得过远会把只读查询也当成鉴权，闸门就失去意义。
func authGateReachable(funcs map[string]map[string]bool, entry string) bool {
	seen := map[string]bool{entry: true}
	frontier := []string{entry}
	for depth := 0; depth < 4 && len(frontier) > 0; depth++ {
		var next []string
		for _, name := range frontier {
			if authGateHelpers[name] {
				return true
			}
			for callee := range funcs[name] {
				if !seen[callee] {
					seen[callee] = true
					next = append(next, callee)
				}
			}
		}
		frontier = next
	}
	for _, name := range frontier {
		if authGateHelpers[name] {
			return true
		}
	}
	return false
}

// authGateRoute 一条注册路由（pattern 原始注册串，handler 归一化为方法名）。
type authGateRoute struct {
	pattern string
	handler string
}

// scanAuthGatePackage 解析 dir 下全部非测试 Go 文件，返回路由注册表与包内调用图。
// 调用图键为「方法名/函数名」（不含包名与接收者），值为该函数体内直接调用的同包方法名集合。
func scanAuthGatePackage(dir string) ([]authGateRoute, map[string]map[string]bool, error) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, err
	}
	funcs := map[string]map[string]bool{}
	var routes []authGateRoute
	for _, pkg := range pkgs {
		for fileName, file := range pkg.Files {
			_ = fileName
			for _, decl := range file.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				name := fd.Name.Name
				calls := map[string]bool{}
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					ce, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					switch fn := ce.Fun.(type) {
					case *ast.SelectorExpr: // s.foo(...) / pkg.Foo(...)
						if sel, ok2 := fn.X.(*ast.Ident); ok2 && isServerReceiver(sel.Name) {
							calls[fn.Sel.Name] = true
						}
					case *ast.Ident: // 包内裸函数调用
						calls[fn.Name] = true
					}
					return true
				})
				funcs[name] = calls
				// 注册点：扫描本函数体内的 s.mux.HandleFunc(pattern, s.handler)
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					ce, ok := n.(*ast.CallExpr)
					if !ok || len(ce.Args) < 2 {
						return true
					}
					sel, ok := ce.Fun.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "HandleFunc" {
						return true
					}
					mx, ok := sel.X.(*ast.SelectorExpr)
					if !ok || mx.Sel.Name != "mux" {
						return true
					}
					pattern, ok := stringArg(ce.Args[0])
					if !ok || !strings.HasPrefix(pattern, "/") {
						return true
					}
					handler := ""
					if hs, ok := ce.Args[1].(*ast.SelectorExpr); ok {
						if id, ok := hs.X.(*ast.Ident); ok && isServerReceiver(id.Name) {
							handler = hs.Sel.Name
						}
					}
					routes = append(routes, authGateRoute{pattern: pattern, handler: handler})
					return true
				})
			}
		}
	}
	return routes, funcs, nil
}

// isServerReceiver 判定标识是否为 Server 接收者惯用名（s / srv / server）。
func isServerReceiver(name string) bool {
	switch name {
	case "s", "srv", "server":
		return true
	}
	return false
}

// stringArg 取字符串字面量参数（含反引号串）。
func stringArg(e ast.Expr) (string, bool) {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(bl.Value)
	if err != nil {
		return filepath.Base(bl.Value), true // 极端情况（拼接串）：退化为原文，交由白名单不命中而红灯
	}
	return v, true
}
