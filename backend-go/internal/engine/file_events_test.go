// ============ 本文件职责中文说明 ============
// ★B3（方案 A2）逐段事件层单测：segment_done/segment_final/segments_sealed 的
// 载荷 schema（对齐 EditorSegment + source_hash + 首次出现序号）、内容级去重、
// 敏感词占位段 placeholder 标记、批回调下标映射（gidx→原始源文）、回显过滤、
// 收尾 sweep 的 diff 语义与语言封印顺序，以及「零 token 裸值」积分口径红线断言。
// 纯内存测试，不依赖 LLM/DB。
// =============================================
package engine

import (
	"regexp"
	"strings"
	"testing"
)

type gotEvent struct {
	kind    string
	payload map[string]interface{}
}

// helperSeg 构建带收集器的发射器（texts/blocked 由用例给定）。
func helperSeg(texts []string, blocked map[string]bool) (*segEmitter, *[]gotEvent) {
	var got []gotEvent
	se := newSegEmitter(func(kind string, payload map[string]interface{}) {
		got = append(got, gotEvent{kind: kind, payload: payload})
	}, texts, blocked)
	return se, &got
}

// TestSegEmitterPayloadSchema 协议红线：三类事件的字段、首次出现序号、稳定散键、
// 以及对外载荷禁止出现 token 裸值字段（AGENTS.md 约定 5 / 既定积分口径）。
func TestSegEmitterPayloadSchema(t *testing.T) {
	se, got := helperSeg([]string{"甲", "乙", "甲"}, map[string]bool{"乙": true})

	se.done("en", "甲", "A-one")
	if len(*got) != 1 {
		t.Fatalf("应发出 1 条事件，实际 %d", len(*got))
	}
	ev := (*got)[0]
	if ev.kind != "segment_done" {
		t.Fatalf("kind=%s", ev.kind)
	}
	if ev.payload["lang"] != "en" || ev.payload["index"] != 0 || ev.payload["source"] != "甲" ||
		ev.payload["draft"] != "A-one" || ev.payload["stage"] != "initial" {
		t.Fatalf("segment_done 载荷不符: %+v", ev.payload)
	}
	if _, hasTarget := ev.payload["target"]; hasTarget {
		t.Fatal("segment_done 不应带 target 字段")
	}
	if _, hasDraft := (*got)[0].payload["draft"]; !hasDraft {
		t.Fatal("缺 draft")
	}
	h, _ := ev.payload["source_hash"].(string)
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(h) {
		t.Fatalf("source_hash 应为 16 位 hex: %q", h)
	}
	// 重复源文共享首次出现序号（前端行键与去重语义一致）
	se.done("en", "甲", "A-two")
	if (*got)[1].payload["index"] != 0 {
		t.Fatalf("重复段 index 应为首次序号 0: %+v", (*got)[1].payload)
	}
	// 内容未变不重发
	n := len(*got)
	se.done("en", "甲", "A-two")
	if len(*got) != n {
		t.Fatal("同语言同段同文应去重不重发")
	}
	// 占位段：blocked 集合命中 → placeholder:true
	se.done("en", "乙", SensitivePlaceholderText)
	last := (*got)[len(*got)-1]
	if last.payload["placeholder"] != true {
		t.Fatalf("拦截段应带 placeholder:true: %+v", last.payload)
	}
	// 输出侧兑底替换出的占位译文（不在 blocked 集合）同样打标
	se.done("de", "甲", SensitivePlaceholderText)
	if (*got)[len(*got)-1].payload["placeholder"] != true {
		t.Fatal("占位译文（value 命中）应带 placeholder")
	}
	// segment_final：文本落 target 字段
	se.final("en", "甲", "A-final", "reviewed")
	fv := (*got)[len(*got)-1]
	if fv.kind != "segment_final" || fv.payload["target"] != "A-final" || fv.payload["stage"] != "reviewed" {
		t.Fatalf("segment_final 载荷不符: %+v", fv.payload)
	}
	if _, hasDraft := fv.payload["draft"]; hasDraft {
		t.Fatal("segment_final 不应带 draft 字段")
	}
	// segments_sealed：仅 lang
	se.sealed("en")
	sv := (*got)[len(*got)-1]
	if sv.kind != "segments_sealed" || len(sv.payload) != 1 || sv.payload["lang"] != "en" {
		t.Fatalf("segments_sealed 应只带 lang: %+v", sv.payload)
	}
	// ★ 积分口径红线：任何事件载荷不得出现 token 类裸值字段
	for _, e := range *got {
		for k := range e.payload {
			lk := strings.ToLower(k)
			if strings.Contains(lk, "token") || strings.Contains(lk, "cost") || lk == "usage" {
				t.Fatalf("事件载荷含禁用字段 %q（%s）：对外积分口径、零 token 裸值", k, e.kind)
			}
		}
	}
}

// TestSegEmitterNilSafe emit=nil（工单/非流式路径）时全部方法空转不 panic。
func TestSegEmitterNilSafe(t *testing.T) {
	var se *segEmitter
	se.done("en", "甲", "x")
	se.final("en", "甲", "x", "gated")
	se.sealed("en")
	se.sweep(map[string]map[string]string{"en": {"甲": "x"}}, []string{"en"})
	cb := se.batchCB("en", func(done, total int) {}, []int{0})
	cb(1, 1, 0, []string{"甲"}, []string{"x"}) // 不应 panic
	if se.on() {
		t.Fatal("nil 发射器 on() 应为 false")
	}
}

// TestBatchDoneEmitter 批回调升级：进度保持旧口径 + 逐段 segment_done；
// gidx 把批下标映射回 texts 全局段号（源文取原始 texts，而非品牌保护后的 protSrc）；
// 失败/空/回显段不发初翻。
func TestBatchDoneEmitter(t *testing.T) {
	texts := []string{"原文0", "原文1", "原文2", "原文3"}
	gidx := []int{1, 3} // 批输入 = texts[1], texts[3]
	se, got := helperSeg(texts, nil)

	var progDone, progTotal = -1, -1
	cb := se.batchCB("ru", func(done, total int) { progDone, progTotal = done, total }, gidx)
	// 模拟 BatchTranslate 首块回调：start=0，两块输入，结果=[命中, 回显]
	cb(2, 2, 0, []string{"prot0", "prot1"}, []string{"Переводин", "原文3"})
	if progDone != 2 || progTotal != 2 {
		t.Fatalf("进度透传不符: %d/%d", progDone, progTotal)
	}
	if len(*got) != 1 {
		t.Fatalf("回显段不应发初翻事件，实际 %d 条", len(*got))
	}
	ev := (*got)[0]
	if ev.kind != "segment_done" || ev.payload["lang"] != "ru" ||
		ev.payload["index"] != 1 || ev.payload["source"] != "原文1" || ev.payload["draft"] != "Переводин" {
		t.Fatalf("批回调事件不符: %+v", ev.payload)
	}
	// 失败/空结果静默
	cb(3, 3, 2, []string{"prot2"}, []string{"[翻译失败]"})
	if len(*got) != 1 {
		t.Fatalf("失败段不应发事件: %d", len(*got))
	}
}

// TestSegEmitterSweep 收尾 sweep：按 texts 原序只补推「与最后已发文本不同」的终稿
// （gated），未变段靠既有事件 + sealed 定稿；随后每语言恰好一条 segments_sealed。
func TestSegEmitterSweep(t *testing.T) {
	texts := []string{"甲", "乙", "甲"}
	se, got := helperSeg(texts, nil)
	se.done("en", "甲", "keep") // 已发初翻且终稿未变 → sweep 不重发
	se.done("en", "乙", "draft")

	finals := map[string]map[string]string{
		"en": {"甲": "keep", "乙": "gated-text"}, // 乙 被闸门覆写
		"de": {"甲": "neu"},                     // de 全程无事件（如纯 KB 失配走兜底）
	}
	se.sweep(finals, []string{"en", "de"})

	var kinds []string
	var enGated, deGated interface{}
	for _, e := range (*got)[2:] {
		kinds = append(kinds, e.kind)
		if e.kind == "segment_final" {
			if e.payload["lang"] == "en" {
				enGated = e.payload
			} else {
				deGated = e.payload
			}
		}
	}
	// 期望序列：en 仅乙一条 gated → en sealed → de 甲一条 gated → de sealed
	if strings.Join(kinds, ",") != "segment_final,segments_sealed,segment_final,segments_sealed" {
		t.Fatalf("sweep 事件序列不符: %v", kinds)
	}
	if pg, _ := enGated.(map[string]interface{}); pg["target"] != "gated-text" || pg["stage"] != "gated" || pg["index"] != 1 {
		t.Fatalf("en gated 终稿不符: %+v", enGated)
	}
	if pg, _ := deGated.(map[string]interface{}); pg["target"] != "neu" || pg["index"] != 0 {
		t.Fatalf("de 补发终稿不符: %+v", deGated)
	}
	// 未译出段（无译文）不发事件——上面 de 只给了 甲，乙 缺席
	for _, e := range *got {
		if e.payload["source"] == "" && e.kind != "segments_sealed" {
			t.Fatal("空源文事件不应出现")
		}
	}
}
