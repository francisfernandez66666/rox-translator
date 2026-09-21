// ============================================================================
// i18n/auditActionCoverage.test.ts — ★ 审计动作码 i18n 覆盖棘轮闸门（2026-09-21 #38 建立）
// 背景：后台审计页把 LogAudit 写入的 action 经 auditActionLabel() 映射成本地语言，
//   未登记的动只会回退显示原始 snake_case code —— 读得出，但多语种客户看不懂、也没法按语言核对。
//   这类缺陷是「越用越多」型的：新增 LogAudit 动作而漏补词典不会报错，只会静默降级。
// 口径：扫描 backend-go/internal 源码里 LogAudit 的字符串字面量动作名（不含 _test.go），
//   与合并词典的 audit.action.* 求差集；差集必须不多于 UNMAPPED_BASELINE（棘轮只减不增）。
//   补了词典就顺手从基线删掉对应项；新增动作必须同步补 12 语种，否则本闸门红灯。
// 运行：npx vitest run src/i18n/auditActionCoverage.test.ts
// ============================================================================
// @vitest-environment node
import { describe, expect, it } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { t } from './index'

/** walk 递归收集 .go 文件（跳过 _test.go：测试里的动作名不代表生产口径） */
function goFiles(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = `${dir}/${name}`
    if (p.endsWith('.go')) { if (!name.endsWith('_test.go')) out.push(p); continue }
    if (statSync(p).isDirectory()) goFiles(p, out)
  }
  return out
}

/** 抽取 LogAudit(租户, 用户, "动作", 域, …) 第 3 参数的字符串字面量 */
function collectActions(): Set<string> {
  const root = fileURLToPath(new URL('../../../backend-go/internal', import.meta.url))
  const actions = new Set<string>()
  for (const f of goFiles(root)) {
    const src = readFileSync(f, 'utf8')
    for (const m of src.matchAll(/LogAudit\(\s*[^,;]+,\s*[^,;]+,\s*"([a-z0-9_]+)"/g)) actions.add(m[1])
  }
  return actions
}

// 2026-09-21 建立基线时的存量漏译动作（原 33 项，见 #42 待补清单；补译后请同步删除对应项）
// 2026-09-21 #34：assist_token_read 已补 12 语种文案，基线减为 32 项
const UNMAPPED_BASELINE: string[] = [
  'alert_silence', 'alert_unsilence', 'billing_exempt_superadmin', 'billing_package_reset',
  'industry_create', 'industry_delete', 'industry_status', 'industry_update', 'kb_dual_track_screen',
  'kb_entry_update', 'kb_pack_grant', 'kb_review_reward', 'kb_reward_config', 'kb_scrape_approve',
  'kb_scrape_restore', 'kb_scrape_source_create', 'kb_scrape_source_status', 'kb_scrape_source_update', 'kb_shared_submit',
  'kb_upload_reward', 'ops_policy_save', 'ops_window_save', 'package_upgrade', 'persona_create',
  'persona_delete', 'persona_status', 'persona_update', 's7_rescue_pack', 'sensitive_block',
  'tm_feedback_screen', 'usdt_auto_settle', 'user_bulk_import',
]

describe('审计动作码 i18n 覆盖（棘轮，只减不增）', () => {
  const actions = collectActions()
  const unmapped = [...actions].filter((a) => t(`audit.action.${a}`) === `audit.action.${a}`).sort()

  it('扫描有效：能从后端调用点取到动作码集合', () => {
    expect(actions.size, '未从 backend-go/internal 扫到 LogAudit 动作字面量，正则可能已失配').toBeGreaterThan(50)
    expect(actions.has('login_sso'), 'login_sso 应被扫到').toBe(true)
  })

  it('未登记词典的动作码不得超出基线（新增动作漏补 12 语种文案即红灯）', () => {
    const fresh = unmapped.filter((a) => !UNMAPPED_BASELINE.includes(a))
    expect(fresh, `新增未翻译的审计动作码：${fresh.join(', ')}`).toEqual([])
  })

  it('基线只减不增：漏译总数不得超过建立时的存量', () => {
    expect(unmapped.length, `漏译动作数 ${unmapped.length} > 基线 ${UNMAPPED_BASELINE.length}`).toBeLessThanOrEqual(UNMAPPED_BASELINE.length)
  })
})
