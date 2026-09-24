// ============================================================================
// lang_middleware.go — 后端语言识别中间件（★ 2026-09-24 〇-S #12）
// 职责：把「按用户界面语言返回提示语」接到 HTTP 边界上——
//  1. 判定语种（X-App-Lang > Accept-Language > 默认 zh），注入 ctx 供 i18n.Msg 使用；
//  2. 仅对 en 请求包一层 langWriter：缓存 JSON 响应体，handler 返回后在字节层面
//     统一改写 `"message":"..."` 值为英文（保留字段顺序，不改任何序列化结构）。
//
// 为什么不逐个改 992 处 writeJSON 调用点：改动面爆炸且必然漏；收口在写响应这一层，
//
//	新增 handler 天然被覆盖。中文请求（含全部 curl/UAT 脚本，无头默认 zh）零包装、
//	字节级行为与改造前完全一致。
//
// 安全边界（langWriter 的直通/缓存二态）：
//   - Content-Type 非 application/json（SSE text/event-stream、静态文件、下载）→ 直通；
//   - 缓冲超 1MB → 冲刷已缓冲部分并切直通（防大响应撑内存）；
//   - handler 主动 Flush → 视为流式，冲刷并切直通，SSE 语义不受损。
//
// ============================================================================
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"translator/internal/i18n"
)

// langJSONBufCap 是 JSON 响应缓冲上限；超过即冲刷并切直通（词条都是短消息，超 1MB 的
// JSON 响应没有翻译需求，也不该为它扣住首字节）。
const langJSONBufCap = 1 << 20

// withLang 解析请求语种并（仅对英文请求）包装响应写入器做 message 字段翻译。
// 参数 next: 下一层 Handler。返回: 包装后的 Handler。
func (s *Server) withLang(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang := i18n.FromRequest(r)
		if lang != "en" {
			// zh（含无头的存量请求/脚本）完全不包装：响应字节与改造前一致
			next.ServeHTTP(w, r)
			return
		}
		ctx := i18n.WithLang(r.Context(), lang)
		lw := &langWriter{real: w, ctx: ctx}
		next.ServeHTTP(lw, r.WithContext(ctx))
		lw.finish()
	})
}

// langWriter 是英文请求专用的响应包装器：决定「缓存 JSON 后翻译」或「直通」。
type langWriter struct {
	real     http.ResponseWriter
	ctx      context.Context
	status   int  // WriteHeader 记录的期望状态码（finish 时才真正下发）
	decided  bool // 缓存/直通二态是否已定
	bufJSON  bool // true=缓冲模式；false=直通模式
	buf      bytes.Buffer
	headered bool // 真实 WriteHeader 是否已发出（直通/冲刷后为 true）
}

// decide 依据当前已设置的 Content-Type 确定二态；调用方保证只在首次写入前调用。
func (lw *langWriter) decide() {
	lw.decided = true
	ct := lw.real.Header().Get("Content-Type")
	lw.bufJSON = strings.Contains(ct, "application/json")
}

// Header 透传真实响应头集合（内层中间件/Handler 的 Header().Set 直接落在真实 map 上）。
func (lw *langWriter) Header() http.Header { return lw.real.Header() }

// WriteHeader 记录状态码；直通模式下立即转发，缓冲模式下扣住到 finish 再发。
func (lw *langWriter) WriteHeader(code int) {
	if lw.status == 0 {
		lw.status = code
	}
	if !lw.decided {
		lw.decide()
	}
	if !lw.bufJSON {
		lw.emitHeader()
	}
}

// emitHeader 把扣住的状态码真正下发一次（幂等保护由 headered 承担）。
func (lw *langWriter) emitHeader() {
	if !lw.headered {
		lw.headered = true
		code := lw.status
		if code == 0 {
			code = http.StatusOK
		}
		lw.real.WriteHeader(code)
	}
}

// Write 缓冲或直通。缓冲超上限时冲刷已有内容并切直通（此后不再改写 message）。
func (lw *langWriter) Write(b []byte) (int, error) {
	if !lw.decided {
		lw.decide()
	}
	if !lw.bufJSON {
		return lw.real.Write(b)
	}
	lw.buf.Write(b)
	if lw.buf.Len() > langJSONBufCap {
		lw.flushBuffered()
	}
	return len(b), nil
}

// flushBuffered 把已缓冲内容原样冲给真实写入器并切直通（用于超限/主动 Flush）。
func (lw *langWriter) flushBuffered() {
	lw.bufJSON = false
	lw.emitHeader()
	if lw.buf.Len() > 0 {
		_, _ = lw.real.Write(lw.buf.Bytes())
		lw.buf.Reset()
	}
	if f, ok := lw.real.(http.Flusher); ok {
		f.Flush()
	}
}

// Flush 实现 http.Flusher：任何显式 flush 都意味着流式响应，立刻切直通。
func (lw *langWriter) Flush() {
	if !lw.decided {
		lw.decide()
	}
	lw.flushBuffered()
}

// finish 在 handler 返回后收尾：缓冲模式下的 JSON 响应逐条改写 message 值再下发。
func (lw *langWriter) finish() {
	if !lw.decided || !lw.bufJSON {
		return
	}
	body := translateJSONMessages(lw.ctx, lw.buf.Bytes())
	// 翻译可能改变字节数：Content-Length 一律按最终体积重算
	h := lw.real.Header()
	h.Del("Content-Length")
	if len(body) > 0 {
		h.Set("Content-Length", strconv.Itoa(len(body)))
	}
	lw.emitHeader()
	if len(body) > 0 {
		_, _ = lw.real.Write(body)
	}
}

// messageRE 匹配 JSON 响应里的 "message":"..." 字段值（含转义字符序列）。
// 只处理该键：后端面向用户的提示统一走 message 字段（writeJSON 与 apierrors 同口径）。
var messageRE = regexp.MustCompile(`("message"\s*:\s*)"((?:\\.|[^"\\])*)"`)

// translateJSONMessages 在字节层面替换 message 字段值为按语种翻译后的文本，
// 其余字节原样保留（字段顺序、缩进、其它键不动）。未命中词条 Msg 原样返回，不产生替换。
func translateJSONMessages(ctx context.Context, body []byte) []byte {
	return messageRE.ReplaceAllFunc(body, func(m []byte) []byte {
		sub := messageRE.FindSubmatch(m)
		if sub == nil {
			return m
		}
		// 捕获组：sub[1]="message"键与冒号原文，sub[2]=值的引号内内容（转义态）
		valQuoted := make([]byte, 0, len(sub[2])+2)
		valQuoted = append(valQuoted, '"')
		valQuoted = append(valQuoted, sub[2]...)
		valQuoted = append(valQuoted, '"')
		var zh string
		if err := json.Unmarshal(valQuoted, &zh); err != nil {
			return m // 非法转义序列：不动字节，交给客户端解码器处理
		}
		en := i18n.Msg(ctx, zh)
		if en == zh {
			return m
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(en); err != nil {
			return m
		}
		quoted := bytes.TrimRight(buf.Bytes(), "\n")
		return append(append([]byte{}, sub[1]...), quoted...)
	})
}
