// ============ reply_fabrication_kb_gate_test.go · 职责说明 ============
// G21 的**库驱动**锁（★ 2026-10-31 补齐）：编造守卫必须问库，不是问提示词、也不是写死名单。
//
//	同一句「我们提供 API sandbox 环境，方便您直接联调」：
//	  ① kb 里没有这条能力 ⇒ 必须被判 `fabr_unknown_deliverable`；
//	  ② 往 kb 补一条含 sandbox 的启用条目 ⇒ 同一句必须放行；
//	  ③ 再把那条 kb 停用（enabled=0，与运营在管理台点停用同形）⇒ 同一句必须重新被判编造。
//	只写提示词/名单的实现在 ② 或 ③ 上必红——这正是「停用 kb 后同一句必须被删」的反证腿。
//
// =============================================
package engine

import "testing"

const g21Sentence = "我们提供 API sandbox 环境，方便您直接联调。"

func TestGuardReplyFabrication_G21FollowsKB(t *testing.T) {
	e := newTestEngine(t)

	// ① 库里没有 sandbox 能力 ⇒ 判编造。
	mustFabricate(t, fabScopeOf(t, e), g21Sentence, fabrUnknownDeliver, "sandbox")

	// ② 补一条启用 kb（运营在管理台新增）⇒ 同一句必须放行，不需要发版。
	id, err := e.db.Create("kb_entries", map[string]any{
		"key": "g21-sandbox", "category": "feature", "title": "API 联调",
		"priority": 5, "enabled": 1,
		"content": "支持 API sandbox 联调环境", "keywords": "sandbox,联调", "link_keys": "",
	})
	if err != nil {
		t.Fatalf("G21：新增 kb 失败：%v", err)
	}
	mustPass(t, fabScopeOf(t, e), g21Sentence)

	// ③ 停用同一行 ⇒ 必须重新被判编造（证明判据真走库，不是靠提示词/写死名单）。
	if err := e.db.Update("kb_entries", id, map[string]any{"enabled": 0}); err != nil {
		t.Fatalf("G21：停用 kb 失败：%v", err)
	}
	mustFabricate(t, fabScopeOf(t, e), g21Sentence, fabrUnknownDeliver, "sandbox")
}
