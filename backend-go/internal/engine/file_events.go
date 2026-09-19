// ============ 本文件职责中文说明 ============
// ★B3（方案 A2）文件翻译逐段事件层：给 SSE 文件通道提供「边翻边上屏」的中间态事件。
// 事件协议（载荷对齐 api/editor.go EditorSegment，字段全积分口径、零 token 裸值）：
//
//	segment_done  { lang, index, source, source_hash, draft, stage:"initial" }  ← 初翻每批完成即推
//	segment_final { lang, index, source, source_hash, target, stage:"reviewed|gated", [placeholder] } ← 审校/闸门覆写后推
//	segments_sealed { lang }                                                     ← 该语言不再变化（编辑器解锁保存）
//
// 关键口径（方案 A2 第 3 条）：引擎内部是「源文串→译文」映射且提取时全局去重，
// 事件必须带 source_hash + 首次出现的段序号（first）作为稳定身份键，前端据此对号入座；
// 不能用 progress 的 percent（done/total 混合语义、非单调）。
// segEmitter 为 HandleFile 专用（emit=nil 时全部方法空转，工单/非流式路径零开销）；
// done/final 自带「同语言同段同文去重」，收尾 sweep 与闸门覆写 diff 后可保证每段至多一条终稿。
// =============================================
package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// SegmentEmit 逐段事件回调（api 层注入，与 prog 同闭包风格；写 SSE 由调用方持锁串行化）。
// kind=segment_done|segment_final|segments_sealed；payload 见本文件头部协议注释。
type SegmentEmit func(kind string, payload map[string]interface{})

// shortSourceHash 源文稳定散键（sha256 前 16 hex）。前端以「首次出现序号」为主键，
// source_hash 仅作二次校验（重复源文/去重错位时防对错行），非交付物、纯技术键。
func shortSourceHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// segEmitter HandleFile 逐段事件发射器：持有全局段序映射与「最后已发文本」台账。
// 并发安全（KB 并行 8 路、批块并发均直接调用）。
type segEmitter struct {
	emit    SegmentEmit                  // nil = 事件通道未启用（工单 worker / 非流式接口）
	texts   []string                     // 提取顺序的源文段（sweep 按此序回放，前端行序稳定）
	first   map[string]int               // 源文 → 首次出现段序号（去重段的稳定身份，A2 第 3 条）
	blocked map[string]bool              // 敏感词拦截段（payload 带 placeholder:true，前端不得当译文编辑）
	mu      sync.Mutex                   // 保护 last（多语言协程并发写）
	last    map[string]map[string]string // lang → 源文 → 最后一次已发文本（内容级去重）
}

// newSegEmitter 构建发射器；emit==nil 返回 nil（方法全部 nil-safe 空转）。
func newSegEmitter(emit SegmentEmit, texts []string, blocked map[string]bool) *segEmitter {
	if emit == nil {
		return nil
	}
	first := make(map[string]int, len(texts))
	for i, t := range texts {
		if _, ok := first[t]; !ok {
			first[t] = i
		}
	}
	return &segEmitter{emit: emit, texts: texts, first: first, blocked: blocked,
		last: map[string]map[string]string{}}
}

// on 事件通道是否启用（供调用点做「仅发事件」的分支，如拦截段预推）。
func (se *segEmitter) on() bool { return se != nil && se.emit != nil }

// send 内部统一出口：记录台账 → 构造载荷 → emit。同语言同段同文本自动去重（返回 false 不发）。
// kind 为 segment_done 时文本落 draft 字段，segment_final 时落 target 字段（协议对齐方案 A2）。
func (se *segEmitter) send(kind, lc, src, text, stage string) bool {
	if !se.on() {
		return false
	}
	se.mu.Lock()
	m := se.last[lc]
	if m == nil {
		m = map[string]string{}
		se.last[lc] = m
	}
	if m[src] == text && text != "" {
		se.mu.Unlock()
		return false // 内容未变不重发（初翻回调与审校循环可能同值两次触达）
	}
	m[src] = text
	se.mu.Unlock()

	payload := map[string]interface{}{
		"lang":        lc,
		"index":       se.first[src], // 首次出现段序号（重复段共享同一行键，与去重语义一致）
		"source":      src,
		"source_hash": shortSourceHash(src),
		"stage":       stage,
	}
	if kind == "segment_final" {
		payload["target"] = text
	} else {
		payload["draft"] = text
	}
	if se.blocked[src] || text == SensitivePlaceholderText {
		payload["placeholder"] = true // 敏感词占位段：前端展示为「已拦截」，不得当译文编辑（A2 第 6 条）
	}
	se.emit(kind, payload)
	return true
}

// done 初翻落地上屏事件（stage 恒为 initial；KB 直译与批翻共用）。
func (se *segEmitter) done(lc, src, draft string) { se.send("segment_done", lc, src, draft, "initial") }

// final 终稿事件（stage=reviewed 审校完成 / gated 闸门覆写收尾）。
func (se *segEmitter) final(lc, src, target, stage string) {
	se.send("segment_final", lc, src, target, stage)
}

// sealed 语言封印事件：该语言全部段不再变化，前端解锁编辑器保存、聊天气泡切下载卡。
func (se *segEmitter) sealed(lc string) {
	if !se.on() {
		return
	}
	se.emit("segments_sealed", map[string]interface{}{"lang": lc})
}

// batchCB 构造 BatchTranslate 段回调（★B3 升级形态）：既推进度（保持旧 prog 口径不变），
// 又按块内每条非失败译文逐段发 segment_done。
// gidx 把批输入下标 → texts 全局段下标（源文映射用原始 texts，规避品牌保护改写后的 protSrc
// 与前端行对不上号）；nil 表示恒等映射。
func (se *segEmitter) batchCB(lc string, prog func(done, total int), gidx []int) BatchChunkDone {
	return func(done, total, start int, sources, results []string) {
		if prog != nil {
			prog(done, total)
		}
		if !se.on() {
			return
		}
		for j := range sources {
			tr := results[j]
			if tr == "" || tr == "[翻译失败]" {
				continue // 失败段不发初翻，留给硬闸补漏/收尾 sweep
			}
			src := sources[j]
			if gidx != nil && start+j < len(gidx) {
				src = se.texts[gidx[start+j]]
			}
			if tr == src {
				continue // ★ 回显（原样返回源文）不算译出，与调用点回显检测同口径
			}
			se.done(lc, src, tr)
		}
	}
}

// sweep 语言收尾：品牌归一/敏感词兑底/质量闸门三道覆写全部落地后，对各语言与
// 「最后已发文本」做 diff——变了的补 segment_final(stage=gated)，没变的靠已发事件即可；
// 每语言扫完即 segments_sealed。按 texts 原序回放，前端行入场顺序稳定。
// 调用点必须处于单协程阶段（wg.Wait 与闸门之后），故内部台账加锁仅为防御。
func (se *segEmitter) sweep(langTranslations map[string]map[string]string, finalLangs []string) {
	if !se.on() {
		return
	}
	for _, lc := range finalLangs {
		tr := langTranslations[lc]
		for i, src := range se.texts {
			if se.first[src] != i {
				continue // 重复源文只在首次出现处发一次
			}
			v := tr[src]
			if v == "" {
				continue // 未译段维持「无行」状态，随工单 Untranslated 走人工补译
			}
			se.final(lc, src, v, "gated")
		}
		se.sealed(lc)
	}
}
