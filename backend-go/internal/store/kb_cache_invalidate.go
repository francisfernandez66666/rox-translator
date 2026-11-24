// ============ kb_cache_invalidate.go · 职责说明 ============
// 给不走 store.SaveEntry/DeleteEntry 的事务型写方提供同一个术语缓存失效出口。
// kb-clean 为保证 KB 与 TM 原子提交，直接按 id 在事务内 UPDATE/DELETE；
// 提交后必须调用本函数，否则 60s TTL 内仍可能召回旧译文。
// ================================================================
package store

// InvalidateKBTermCache 清掉术语/包读缓存；事务提交成功后调用，失败或回滚不得调用。
func InvalidateKBTermCache() { invalidateTermCache() }
