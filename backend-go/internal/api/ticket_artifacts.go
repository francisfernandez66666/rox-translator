// ============ ticket_artifacts.go · 职责说明 ============
// 两段式交付（md 先行）的**取件面**（★ 2026-10-30 三事合一批 · 组5）。
//
// 本文件只有三件事，且都是"用户面与开放 API 共用同一把尺子"的落点（方案 §5.4）：
//  1. ticketArtifactFiles：一次下载/打包要取哪几件（新单按 ticket_artifacts 表取，
//     历史单回落 ticket_files/result_path——**回落是读侧行为，不写回、不迁移**）；
//  2. serveTicketPicks：单件直发 / 多件**按需流式打包**（不落盘，zip 条目名＝产物 name，
//     与旧落盘 zip 口径逐字一致）；
//  3. 单件下载与预览两个新端点（aid 定位，路径只从库里取——绝不接受客户端传路径，防目录穿越）。
//
// 判据取向：
//   - aid 不属于该单 ⇒ 404（不泄露存在性，与详情/下载同一条安全口径）；
//   - 行在但文件不在（过期/手工删） ⇒ 404 + 可行动文案「请重新发起工单获取」；
//   - 预览只认 kind=md，超上限截断并带 X-Truncated: 1（前端据此提示"已截断"）。
//
// 错误响应一律 s.writeError（AGENTS §一·8：内联 writeJSON(4xx) 会顶红整包棘轮）。
// ============================================
package api

import (
	"archive/zip"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"translator/internal/auth"
	"translator/internal/engine"
	apierrors "translator/internal/errors"
	"translator/internal/store"
)

// ticketArtifactPreviewMax 预览上限（512 KB）。超出截断并带 X-Truncated: 1。
// 为什么设上限：md 产物可能是一整本手册，把几 MB 的正文塞进一个同步 JSON 轮询里
// 会把详情页拖到几百毫秒级；预览只服务"扫一眼确认交付了"，全文要下载。
const ticketArtifactPreviewMax = 512 << 10

// ticketArtifactPick 一件可取回的产物（磁盘路径 + 下载名）。
// Name 一律取产物行的 name（= 落盘 basename），这样"按需 zip 的条目名"与
// 历史落盘 zip 的条目名逐字一致（B9/T76-2 判据）。
type ticketArtifactPick struct {
	Path string
	Name string
}

// ticketArtifacts 读本单的产物行（失败/无 Store 时返回空，调用方按"历史单"回落）。
func (s *Server) ticketArtifacts(ticketID int64) []*store.TicketArtifact {
	if s.Store == nil || ticketID <= 0 {
		return nil
	}
	list, err := s.Store.ListTicketArtifacts(ticketID)
	if err != nil {
		return nil
	}
	return list
}

// ticketArtifactDownloadName 产物下载名：优先用登记时的 name，缺失才回落 basename。
func ticketArtifactDownloadName(a *store.TicketArtifact) string {
	if a == nil {
		return ""
	}
	if n := strings.TrimSpace(a.Name); n != "" {
		return n
	}
	return filepath.Base(a.Path)
}

// ticketArtifactFiles 汇总一次下载/打包所需的（路径, 下载名）清单——用户面与开放 API 共用。
//
// 取件来源三档（新→旧），顺序写死：
//  1. ticket_artifacts 有行 ⇒ 按表取。textOnly 只取 md；否则每个 (file_id, lang) 组取**一件**：
//     original 优先、没有 original 就退回 md（降级组/纯文案组）——这与旧 result_path
//     「多语言=全部打包、降级=md」的交付集合等价，旧客户端拿到的文件集合不变（B9）；
//  2. 无 artifacts（历史单）⇒ ticket_files.result_path / text_result_path；
//  3. 仍无（更老的单文件单）⇒ tickets.result_path / text_result_path。
//
// 返回 used=true 表示第 1 档（调用方据此区分"新单"与"历史单回落"）。
// ⚠️ 磁盘上不存在的文件在这里被剔除（历史行为：单份读失败跳过、不阻断整包）；
//
//	单件端点要判 404，所以它不走本函数（自己 os.Open）。
func (s *Server) ticketArtifactFiles(t *store.Ticket, tfiles []*store.TicketFile, textOnly bool, fileID int64, baseName string) ([]ticketArtifactPick, bool) {
	if t == nil {
		return nil, false
	}
	arts := s.ticketArtifacts(t.ID)
	if len(arts) > 0 {
		picks := make([]ticketArtifactPick, 0, len(arts))
		seen := map[string]bool{}
		add := func(a *store.TicketArtifact) {
			if a == nil || a.Path == "" || seen[a.Path] {
				return
			}
			if _, err := os.Stat(a.Path); err != nil {
				return // 过期/手工删：按需打包跳过这一件（单件端点另判 404）
			}
			seen[a.Path] = true
			picks = append(picks, ticketArtifactPick{Path: a.Path, Name: ticketArtifactDownloadName(a)})
		}
		if textOnly {
			for _, a := range arts {
				if a == nil || a.Kind != store.TicketArtifactKindMD {
					continue
				}
				if fileID > 0 && a.FileID != fileID {
					continue
				}
				add(a)
			}
			return picks, true
		}
		// 每组取一件：original 优先，其次 md。组序按登记顺序（List 已按 file_id,lang,id 排好）。
		chosen := map[string]*store.TicketArtifact{}
		order := make([]string, 0, len(arts))
		for _, a := range arts {
			if a == nil || a.Path == "" || a.Kind == "" {
				continue
			}
			if a.Kind != store.TicketArtifactKindMD && a.Kind != store.TicketArtifactKindOriginal {
				continue
			}
			if fileID > 0 && a.FileID != fileID {
				continue
			}
			key := strconv.FormatInt(a.FileID, 10) + "|" + a.Lang
			cur, ok := chosen[key]
			if !ok {
				chosen[key] = a
				order = append(order, key)
				continue
			}
			if cur.Kind != store.TicketArtifactKindOriginal && a.Kind == store.TicketArtifactKindOriginal {
				chosen[key] = a
			}
		}
		for _, key := range order {
			add(chosen[key])
		}
		return picks, true
	}
	// —— 历史单回落（只读，不写回、不猜产物种类，方案 §5.6）——
	isTextMode := strings.EqualFold(t.Delivery, "text")
	picks := make([]ticketArtifactPick, 0, 2)
	seen := map[string]bool{}
	add := func(p, name string) {
		if p == "" || seen[p] {
			return
		}
		if _, err := os.Stat(p); err != nil {
			return
		}
		seen[p] = true
		picks = append(picks, ticketArtifactPick{Path: p, Name: name})
	}
	matchedFile := false
	for _, f := range tfiles {
		if f == nil {
			continue
		}
		if fileID > 0 && f.ID != fileID {
			continue
		}
		matchedFile = true
		if textOnly {
			p := f.TextResultPath
			if p == "" && isTextMode {
				p = f.ResultPath // 纯文案模式：主产物即 .md
			}
			add(p, textDeliverableName(f))
			continue
		}
		add(f.ResultPath, engine.DeliverableBaseName(f.ResultPath, resultFileName(f)))
	}
	if fileID > 0 && !matchedFile {
		return nil, false // 调用方按"该文件不存在"回 404
	}
	if len(picks) == 0 && len(tfiles) == 0 {
		if textOnly {
			p := t.TextResultPath
			if p == "" && isTextMode {
				p = t.ResultPath
			}
			add(p, baseName+"_译文"+filepath.Ext(p))
		} else {
			add(t.ResultPath, engine.DeliverableBaseName(t.ResultPath, baseName+filepath.Ext(t.ResultPath)))
		}
	}
	return picks, false
}

// serveTicketPicks 流式交付一组产物：单件直发、多件**按需**打 zip（不落盘，方案 §5.4）。
// 为什么不落盘：落盘 zip 只表达"最终态"（两段式的 md 先行在包里完全看不见），
// 且产物进包后没有身份；按需打包让"取件来源"与"交付事实"永远同一份（artifacts 表）。
// 单份读失败跳过而非整体失败——已生成的语言不该被一个坏文件连累。
func (s *Server) serveTicketPicks(w http.ResponseWriter, r *http.Request, picks []ticketArtifactPick, zipName string) {
	if len(picks) == 1 {
		s.serveOneFile(w, r, picks[0].Path, picks[0].Name)
		return
	}
	w.Header().Set("Content-Disposition", contentDisposition(zipName))
	w.Header().Set("Content-Type", "application/zip")
	zw := zip.NewWriter(w)
	for _, p := range picks {
		data, err := os.ReadFile(p.Path)
		if err != nil {
			continue
		}
		fe, _ := zw.Create(p.Name)
		_, _ = fe.Write(data)
	}
	_ = zw.Close()
}

// ticketArtifactItem 详情出栈的产物条目（★ 两段式的客户面形状）。
// ⚠️ 刻意**不出 Path**：磁盘路径不进客户面（本仓"只出 aid 定位"的安全口径），
// 下载一律走 /api/tickets/artifact?id=&aid=，路径只从库里取。
// 字段名与前端 TicketArtifact 三面等值（B13）；file_id/lang/degraded 三列是前端既有契约（R-4）。
type ticketArtifactItem struct {
	ID        int64  `json:"id"`
	FileID    int64  `json:"file_id"`
	Lang      string `json:"lang"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Primary   bool   `json:"primary"`
	Degraded  bool   `json:"degraded"`
	CreatedAt string `json:"created_at,omitempty"`
}

// ticketArtifactItems 产物行 → 客户面清单（空行返回**空切片**，不是 null：前端按数组渲染）。
func ticketArtifactItems(list []*store.TicketArtifact) []ticketArtifactItem {
	out := make([]ticketArtifactItem, 0, len(list))
	for _, a := range list {
		if a == nil {
			continue
		}
		out = append(out, ticketArtifactItem{
			ID: a.ID, FileID: a.FileID, Lang: a.Lang, Kind: a.Kind, Name: a.Name,
			Size: a.Size, Primary: a.Primary, Degraded: a.Degraded, CreatedAt: a.CreatedAt,
		})
	}
	return out
}

// ticketDeliverablesFromArtifacts 由产物行生成交付件清单（保持旧的 kind 取值口径：
// restore=还原回写/主产物、text=纯文案 .md），供前端旧渲染路径零改动消费。
func ticketDeliverablesFromArtifacts(list []*store.TicketArtifact) []ticketDeliverableItem {
	out := make([]ticketDeliverableItem, 0, len(list))
	for _, a := range list {
		if a == nil || a.Path == "" {
			continue
		}
		kind := "restore"
		if a.Kind == store.TicketArtifactKindMD {
			kind = "text"
		}
		name := ticketArtifactDownloadName(a)
		out = append(out, ticketDeliverableItem{Name: name, Size: a.Size, Kind: kind})
	}
	return out
}

// ticketArtifactByQuery 解析 ?id=&aid= 并做权限/归属校验（下载与预览两处共用）。
// 语义：未登录 401／不是创建者且非超管 403／单不存在（含跨租户）404／aid 不属于该单 404。
// 返回 ok=false 表示已经写过错误响应，调用方直接 return。
func (s *Server) ticketArtifactByQuery(w http.ResponseWriter, r *http.Request) (*store.Ticket, *store.TicketArtifact, bool) {
	u := s.authUser(r)
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return nil, nil, false
	}
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	aid, _ := strconv.ParseInt(r.URL.Query().Get("aid"), 10, 64)
	if id <= 0 || aid <= 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "缺少工单 id 或产物 id"))
		return nil, nil, false
	}
	t, err := s.Store.GetTicket(id, s.effTenant(r, u))
	if err != nil {
		// 跨租户同样回 404（不泄露存在性），与详情/下载同一条口径。
		s.writeError(w, r, apierrors.New(apierrors.ErrTicketNotFound, "工单不存在"))
		return nil, nil, false
	}
	if t.CreatedBy != u.ID && !auth.IsSuperAdmin(u) {
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "无权访问他人工单产物"))
		return nil, nil, false
	}
	a, aerr := s.Store.GetTicketArtifact(aid, t.ID)
	if aerr != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "读取产物失败"))
		return nil, nil, false
	}
	if a == nil {
		// aid 存在但不属于该单 / 根本不存在：同一档 404（防"拿别单的 aid 试探"）。
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "产物不存在"))
		return nil, nil, false
	}
	return t, a, true
}

// handleTicketArtifact GET /api/tickets/artifact?id=&aid=
// 下载单件产物（权限与旧下载端点一致：创建者或超管，且须已完成的单）。
func (s *Server) handleTicketArtifact(w http.ResponseWriter, r *http.Request) {
	t, a, ok := s.ticketArtifactByQuery(w, r)
	if !ok {
		return
	}
	if t.Status != store.TicketCompleted {
		// 与旧下载端点同一判据：产物只在完成态可取（md 会先出现，但"取件"仍按整单完成口径，
		// 避免客户在写回中途拿走半套产物、又在下一次轮询里看到集合变了）。
		s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "工单尚未完成"))
		return
	}
	if _, err := os.Stat(a.Path); err != nil {
		// 行在、文件不在（过期清理/手工删）：给可行动文案，而不是把它当成服务端故障。
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "产物不存在或已过保留期，请重新发起工单获取"))
		return
	}
	s.serveOneFile(w, r, a.Path, ticketArtifactDownloadName(a))
}

// handleTicketArtifactPreview GET /api/tickets/artifact/preview?id=&aid=
// 预览纯文案 .md：Content-Type: text/markdown; charset=utf-8，上限 512KB，超出截断并带 X-Truncated: 1。
func (s *Server) handleTicketArtifactPreview(w http.ResponseWriter, r *http.Request) {
	t, a, ok := s.ticketArtifactByQuery(w, r)
	if !ok {
		return
	}
	if a.Kind != store.TicketArtifactKindMD {
		// 只认 md：原格式件（docx/pdf/图片/音频）前端不做预览（要预览请走 md 那一件）。
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "只有纯文案 .md 产物支持预览"))
		return
	}
	if t.Status != store.TicketCompleted {
		s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "工单尚未完成"))
		return
	}
	fh, err := os.Open(a.Path)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "产物不存在或已过保留期，请重新发起工单获取"))
		return
	}
	defer fh.Close()
	b, rerr := io.ReadAll(io.LimitReader(fh, ticketArtifactPreviewMax+1))
	if rerr != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "读取产物失败"))
		return
	}
	truncated := false
	if len(b) > ticketArtifactPreviewMax {
		b = b[:ticketArtifactPreviewMax]
		truncated = true
		// 截断点可能落在一个多字节字符中间：往回退到最后一个完整 rune，
		// 否则前端拿到的是非法 UTF-8（浏览器会显示替换符，看起来像"产物坏了"）。
		for len(b) > 0 {
			rr, size := utf8.DecodeLastRune(b)
			if rr == utf8.RuneError && size <= 1 {
				b = b[:len(b)-1]
				continue
			}
			break
		}
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	if truncated {
		w.Header().Set("X-Truncated", "1")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}
