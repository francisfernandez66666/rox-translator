// ============================================================================
// components/ProductFacts.tsx — 一期「事实项与确认态」登录态面板（施工_一期 §8.1／§2.2）
//
// 事实项 status 三值 unverified／confirmed／withdrawn；只有 confirmed 可被对外内容引用。
// cert 类合规声明缺省落 visibility='gated'（§2.2 V1），本面板在登录态照常读得到全部行，
// 但对外公开页那一路整段不出栈（见 ProductPublicPage.tsx 的 publicOnly 过滤）。
// kind 九值名单的译文走 facts.kind.*（懒加载档），由 i18n/productKindCoverage.test.ts 守差集。
// 本文件由 App.tsx 的 FrontShell 路由 React.lazy 加载，禁止入口包静态 import。
// ============================================================================
import { useEffect, useState } from 'react'
import { useT } from '@/i18n'
import { productList, factList, factConfirm, factWithdraw, type ProductFact } from '@/api/product'
import { Button, DataTable, EmptyState, PageHeader, type TableColumn } from '@/ui/langcross/src'

/** 事实项面板（登录态）：自动取首个产品，列出事实并提供确认／撤回两个写口 */
export default function ProductFacts() {
  const [, t] = useT()
  const [facts, setFacts] = useState<ProductFact[]>([])
  const [productId, setProductId] = useState('')

  /** 按产品拉取事实列表（空集归一成 []，不出 null） */
  const reload = (pid: string) => {
    factList(pid)
      .then((r) => { if (r.success && Array.isArray(r.facts)) setFacts(r.facts) })
      .catch(() => { /* 读不到保持上一次列表 */ })
  }

  useEffect(() => {
    let alive = true
    productList()
      .then((r) => {
        if (!alive || !r.success) return
        const first = Array.isArray(r.products) ? r.products[0] : undefined
        if (first) setProductId(String(first.id))
      })
      .catch(() => { /* 取不到产品停在空态 */ })
    return () => { alive = false }
  }, [])

  useEffect(() => { if (productId) reload(productId) }, [productId])

  /** 确认／撤回后重新拉一次列表（写侧先落库再碰进程侧，§5.1） */
  const act = (id: number, op: 'confirm' | 'withdraw') => {
    const call = op === 'confirm' ? factConfirm(id) : factWithdraw(id)
    call.then(() => { if (productId) reload(productId) }).catch(() => { /* 写失败保留原态 */ })
  }

  const columns: TableColumn<ProductFact>[] = [
    { key: 'kind', title: t('product.panel.kind'), render: (f) => t(`facts.kind.${f.kind}`) },
    { key: 'value', title: t('product.panel.value'), render: (f) => f.value },
    {
      key: 'status',
      title: t('product.panel.status'),
      render: (f) => (
        <span data-testid={`fact-${f.status}`}>
          {f.status === 'confirmed' ? t('product.panel.confirmed')
            : f.status === 'withdrawn' ? t('product.panel.withdrawn')
              : t('product.panel.unverified')}
          {f.visibility === 'gated' && <em> · {t('product.panel.gatedBadge')}</em>}
        </span>
      ),
    },
    {
      key: 'actions',
      title: '',
      render: (f) => (
        <>
          {f.status !== 'confirmed' && <Button onClick={() => act(f.id, 'confirm')}>{t('product.panel.confirm')}</Button>}
          {f.status !== 'withdrawn' && <Button onClick={() => act(f.id, 'withdraw')}>{t('product.panel.withdraw')}</Button>}
        </>
      ),
    },
  ]

  return (
    <>
      <PageHeader title={t('product.panel.facts')} />
      {facts.length === 0 ? (
        <EmptyState title={t('product.panel.empty')} />
      ) : (
        <DataTable<ProductFact> columns={columns} rows={facts} rowKey={(f) => String(f.id)} />
      )}
    </>
  )
}
