// ============ assist_local.go · 职责说明 ============
// 四期 W5（§4.5.2 步骤 1/4）：assist 进程**逻辑收敛**进主服务——只换宿主，
// internal/assist/engine 的包路径与全部导出零改动。
//
// 旧形态：/assist-api/* 经 assist_open_proxy.go 走 HTTP + HMAC 到独立进程 :8790；
// 新形态：主服务进程内本地持有一个 *assist/engine.Engine 与四端点 handler，
//
//	/assist-api 前缀与 greeting/chat/history/features 四个访客端点**原样保留**
//	（线上挂件是缓存包，路由不能删），请求剥前缀后直接转调本地 handler，
//	不再出网、不再签头、不再 32KB 截断跨进程载荷。
//
// 为什么装配点用包级 atomic.Pointer 而不是 Server 结构体字段：
//
//	W5 硬纪律禁止改 server.go（组合根文件），所以本文件提供 InstallAssistLocal
//	供组合根在 NewServer 之后一次装配（接线见 /tmp/w5_wiring.md）。生产单实例；
//	测试可用 serveAssistOpenLocal 直接传 la，不依赖这个单例。
//
// parity（A1）：本地链路把事实集/画像直接注入 r.Context()，注入条件与旧代理
//
//	attachSalesFacts / attachAssistProfile 的发头条件逐项对齐；事实集再经
//	EncodeSalesFacts→DecodeSalesFacts 走一遍，与 assist 侧验签后得到的 Set
//	逐字段同形，于是 guardSalesClaims 读到的 ok 与内容一字不差。assist 侧
//	injectSalesFacts / visitorProfileFromRequest 在「无头」时原样透传 ctx，
//	不会覆盖本次注入（见 sales_facts_from_request.go / profile.go）。
//
// 本文件不得出现任何跨进程头写入 / assistClient / assistBaseURL（A4 源码锁锚定）。
// ================================================
package api

import (
	"bytes"
	"net/http"
	"strings"
	"sync/atomic"

	assistengine "translator/internal/assist/engine"
	mainengine "translator/internal/engine"
	apierrors "translator/internal/errors"
)

// localAssist 主服务进程内本地持有的一份接待引擎 + 四端点 handler。
type localAssist struct {
	engine  *assistengine.Engine
	handler http.Handler
}

// assistLocalPtr 包级装配点（见文件头理由）。atomic.Pointer 保证组合根装配与
// 并发请求读取之间无数据竞争（-race 口径）。
var assistLocalPtr atomic.Pointer[localAssist]

// InstallAssistLocal 组合根唯一装配入口（接线见 /tmp/w5_wiring.md）。
// eng 与 h 必须来自同一个 *assist/store.DB（sessKey／会话数据同源），否则
// greet 下发的 tok 与 history 的校验会对不上——这是本地链路唯一不能错的一处。
func InstallAssistLocal(eng *assistengine.Engine, h http.Handler) {
	if eng == nil || h == nil {
		return
	}
	assistLocalPtr.Store(&localAssist{engine: eng, handler: h})
}

// AssistLocalEngine 供 /api/health 与面检读「本地引擎是否装配」；未装配返回 nil。
// 只回引擎指针，不回 handler（handler 只经 serveAssistOpenLocal 转调）。
func AssistLocalEngine() *assistengine.Engine {
	if la := assistLocalPtr.Load(); la != nil {
		return la.engine
	}
	return nil
}

// currentAssistLocal 取当前本地装配；nil 表示未装配（分发器回落旧代理）。
func currentAssistLocal() *localAssist { return assistLocalPtr.Load() }

// localAssistOpenPaths 本地链路的访客端点白名单（与 assistOpenPaths 逐项同形）。
// A3 锁以运行时 map 比对钉住它与旧白名单一致；删一项即判红（M0300）。
var localAssistOpenPaths = map[string]bool{
	"/api/assist/greeting": true,
	"/api/assist/chat":     true,
	"/api/assist/history":  true,
	"/api/assist/features": true,
}

// assistLocalResponseCapture 只做「先答客后回写」的字节留痕：
// 访客拿到的字节仍由真实 ResponseWriter 写出，本壳只额外留一份供
// writeBackAttribution 解析 attribution 段（顺序同旧代理 A04b/M0210）。
type assistLocalResponseCapture struct {
	http.ResponseWriter
	buf bytes.Buffer
}

func (c *assistLocalResponseCapture) Write(b []byte) (int, error) {
	c.buf.Write(b) // 留痕；bytes.Buffer.Write 永不返回错误，不影响访客字节
	return c.ResponseWriter.Write(b)
}

// serveAssistOpenLocal 本地四端点转调：剥前缀 → 白名单/方法/限长 → ctx 注入 → 本地 handler。
// 不构造 http.NewRequest、不调 assistClient、不写任何跨进程头（A4 锁锚定本文件）。
func (s *Server) serveAssistOpenLocal(w http.ResponseWriter, r *http.Request, la *localAssist) {
	if la == nil || la.handler == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUpstreamUnavailable, "AI 助手服务暂不可用"))
		return
	}
	// 页面上下文先捕获再删（与旧代理 sales_facts_scope.go 同一卫生口径；
	// 不是跨进程 strip，只是不让客户端自带的页面头在同一请求里继续被读到）。
	page := pageContextOf(r)
	stripProductPageHeader(r.Header)
	up := strings.TrimPrefix(r.URL.Path, assistOpenPrefix)
	if up == "" || up[0] != '/' {
		up = "/" + up // 容错：/assist-apiapi/... 这类畸形路径不该被拼成合法上游
	}
	if !localAssistOpenPaths[up] {
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "接口不存在"))
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
		return
	}
	if r.Method == http.MethodPost {
		// 限长仍用旧代理同一个常量：本地不出网，但浏览器侧的超大 body 一样要挡。
		r.Body = http.MaxBytesReader(w, r.Body, assistOpenMaxBody)
	}
	// ★ 作用域只算一次（W1 §4.1.3-⑥ 口径 2）：事实注入与归因回写共用同一个 sc。
	sc := s.resolveSalesFactsScope(r, page)
	r = s.injectLocalAssistContext(r, sc)
	// 剥 /assist-api 前缀；本地 handler 注册的是 /api/assist/* 裸路径。
	// RawQuery 由 Clone 原样带过来，无需重写。
	r2 := r.Clone(r.Context())
	r2.URL.Path = up
	r2.URL.RawPath = ""
	r2.RequestURI = up
	cw := &assistLocalResponseCapture{ResponseWriter: w}
	la.handler.ServeHTTP(cw, r2)
	// ★ 先答客后回写：字节已落字，回写成败与访客答复无关（与旧链同序）。
	s.writeBackAttribution(r.Context(), cw.buf.Bytes(), r, sc)
}

// injectLocalAssistContext 把事实集与画像直接放进 ctx，替代跨进程签名头。
//
// 注入条件逐项对齐旧代理：事实集＝sc.injectable() 且共享签名密钥在位；
// 画像＝登录用户且密钥在位。二者都不满足时 ctx 原样返回（守卫仍 fail-closed，
// 与「无头」那一档逐字节等价）。
func (s *Server) injectLocalAssistContext(r *http.Request, sc salesFactsScope) *http.Request {
	if r == nil {
		return r
	}
	key := s.assistProfileKey()
	ctx := r.Context()
	if sc.injectable() && len(key) > 0 {
		set := mainengine.LimitSalesFacts(s.buildSalesFactsSet(r, sc))
		// ★ 与 assist 侧验签解码链同形：Encode→Decode 让 nil/空切片与 omitempty
		//   字段跟跨进程形态逐字段一致，guardSalesClaims 读到的 ok 与内容一字不差（A1）。
		if payload, err := mainengine.EncodeSalesFacts(set); err == nil {
			if decoded, derr := mainengine.DecodeSalesFacts(payload); derr == nil {
				ctx = s.localFactsContext(ctx, decoded)
			}
		}
	}
	if len(key) > 0 {
		if u := s.authUser(r); u != nil && u.ID > 0 {
			// Visitor 全为导出标量字段，与旧链 Encode→Verify 往返字段级等值；
			// 本地直注与验签后得到的是同一份画像（不写任何一个跨进程头）。
			if vp := s.buildVisitorProfile(r, u); vp != nil {
				ctx = assistengine.WithVisitorProfile(ctx, vp)
			}
		}
	}
	return r.WithContext(ctx)
}
