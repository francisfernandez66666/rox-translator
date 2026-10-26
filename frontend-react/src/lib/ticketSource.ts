// ============================================================================
// lib/ticketSource.ts — 工单「来源档」取词的**唯一入口**（★ 2026-10-26 追加裁决九-2）
//
// 背景：`quality.sources` 出栈的是英文档位名（`kb` / `model` / `model_with_source_fallback`
//   / `upstream_failed` / `approved`，名单见 backend-go/internal/orchestrator/workflow.go 的
//   Source* 常量）。此前全仓**没有**任何界面文案覆盖这两族值——管理台 TicketsP 里那个
//   `srcLabel` 说的是 bitext/tmx/feedback/count（导入来源），与这里的**翻译来源档**是两码事，
//   不复用、不混淆。
// 口径：
//   · 值 → 词典键的映射表**只有这一份**（TICKET_SOURCE_KEYS），界面一律经 ticketSourceLabel() 取词；
//     在任何调用方再内联一份 `x === 'model' ? '模型译文' : …` ＝第二把尺子，
//     由 src/lib/ticketSource.test.ts 的「名单外不许长出第二份表」腿当场判红。
//   · 名单外的值（后端将来加第六档）→ 原样回显档位名并照常渲染，**不吞、不判错**
//     （同 qaRuleLabel 的既有回退口径；新档补译是下一批 i18n 的活，不是把界面打 blank）。
//   · 词典键 12 语种逐键补译由 locales.core.test.ts 保证（ALL_KEYS 全量覆盖）。
// ============================================================================
import { t } from '@/i18n'

/** 来源档名单（与 orchestrator 的 Source* 常量逐字相同；等值由 ticketSource.test.ts L1 钉住） */
export const TICKET_SOURCE_VALUES = [
  'kb',
  'model',
  'model_with_source_fallback',
  'upstream_failed',
  'approved',
] as const

/** 档位值 → 界面文案词典键（唯一映射表，禁止在别处再写一份） */
const TICKET_SOURCE_KEYS: Record<string, string> = {
  kb: 'tk.srcKb',
  model: 'tk.srcModel',
  model_with_source_fallback: 'tk.srcModelFallback',
  upstream_failed: 'tk.srcUpstreamFailed',
  approved: 'tk.srcApproved',
}

/**
 * ticketSourceLabel 按当前界面语言取「来源档」文案；
 * 名单外的值原样回显（不吞档位名——排障时看得见原始档位比看见空白强）。
 */
export function ticketSourceLabel(value: string): string {
  const k = TICKET_SOURCE_KEYS[value]
  return k ? t(k) : value
}
