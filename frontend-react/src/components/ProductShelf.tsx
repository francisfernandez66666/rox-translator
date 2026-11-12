// ============================================================================
// components/ProductShelf.tsx — 一期「物料货架＋齐备度」登录态面板（施工_一期 §8.1／§2.5）
//
// 齐备度六格 × 语种由后端现算（§2.5，SQL 带 product_id 前缀等值条件 R5），本面板只做读数：
// 「缺」与「已过期」必须不同形（missing 不落行、expired 落在行上），空集出空态而不出 null 文本。
// 面板键 product.panel.* 属懒加载档（appPanels.ts），只在登录态 ensureAppPanels() 之后取得到；
// 对外公开页不复用本文件的键（appPanelSplit.test.ts P2 会把公开面 import 图算一遍）。
// 额度耗尽态「两副面孔」文案的唯一渲染出口 ExhaustNotice 也在这里（§8.5 X3）。
// 本文件由 App.tsx 的 FrontShell 路由 React.lazy 加载，禁止入口包静态 import。
// ============================================================================
import { useEffect, useState } from 'react'
import { useT } from '@/i18n'
import { useNav } from '@/lib/nav'
import { productList, productReadiness, type ReadinessCell } from '@/api/product'
import { copyText, type CopyAudience, type ExhaustReason } from '@/lib/productExhaustCopy'
import { Button, DataTable, EmptyState, PageHeader, type TableColumn } from '@/ui/langcross/src'

/** 额度耗尽态提示：两副面孔文案的**唯一渲染出口**（§8.5 X3；任何组件不得自造第三处文案） */
export function ExhaustNotice({ reason, audience, resetsAt }: {
  reason: ExhaustReason
  audience: CopyAudience
  resetsAt?: string
}) {
  return <p data-testid="exhaust-copy">{copyText(reason, audience, resetsAt)}</p>
}

/** 齐备度单格文案：六格现算读数中「缺」与「已过期」必须落不同词（§9.2 readiness 锁的同形防线） */
function cellLabel(worst: string, t: (k: string) => string): string {
  if (worst === 'missing') return t('product.panel.cellMissing')
  if (worst === 'expired') return t('product.panel.cellExpired')
  return t('product.panel.cellValid')
}

/** 物料货架面板（登录态）：自动取首个产品，渲染六格 × 语种齐备度现算读数 */
export default function ProductShelf() {
  const [, t] = useT()
  const nav = useNav()
  const [cells, setCells] = useState<ReadinessCell[]>([])
  const [productId, setProductId] = useState('')

  useEffect(() => {
    let alive = true
    productList()
      .then((r) => {
        if (!alive || !r.success) return
        const first = Array.isArray(r.products) ? r.products[0] : undefined
        if (first) setProductId(String(first.id))
      })
      .catch(() => { /* 取不到产品就停在空态，不打断面板 */ })
    return () => { alive = false }
  }, [])

  useEffect(() => {
    if (!productId) return
    let alive = true
    productReadiness(productId)
      .then((r) => { if (alive && r.success && Array.isArray(r.readiness)) setCells(r.readiness) })
      .catch(() => { /* 齐备度读不到保持空态 */ })
    return () => { alive = false }
  }, [productId])

  const columns: TableColumn<ReadinessCell>[] = [
    { key: 'kind', title: t('product.panel.kind'), render: (c) => `${c.asset_type} / ${c.lang}` },
    { key: 'status', title: t('product.panel.status'), render: (c) => <span data-testid={`readiness-${c.worst}`}>{cellLabel(c.worst, t)}</span> },
  ]

  return (
    <>
      {/* ★ X1（2026-11-06）：货架只回答"缺什么"；这一颗钮把人送到能把缺的补上的那一页。
          旧形态这里连一个写侧入口都没有 ⇒ 客户看得到六个空格子，却没有任何一条路把内容填进去。 */}
      <PageHeader
        title={<span data-testid="product-shelf">{t('product.panel.shelf')}</span>}
        actions={<Button data-testid="shelf-goto-detail" onClick={() => nav.navigate('/products/detail')}>{t('product.panel.gotoDetail')}</Button>}
      />
      {cells.length === 0 ? (
        <EmptyState title={t('product.panel.empty')} />
      ) : (
        <DataTable<ReadinessCell> columns={columns} rows={cells} rowKey={(c, i) => `${c.asset_type}:${c.lang}:${i}`} />
      )}
    </>
  )
}
