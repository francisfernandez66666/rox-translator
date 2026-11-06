// ============================================================================
// components/ProductAnswers.tsx — 一期「知识层问答与缺项点名」登录态面板（施工_一期 §8.1／§7）
//
// 零 LLM 调用（A2 的唯一证据）：答案只走 /api/product/answer 一次，来源标 knowledge_base，
// 面板自身**不发起第二次模型请求**，也不在前端兜底"再调一次"。缺项由后端在 missing 里点名，
// 前端照实出列，不许悄悄把请求转交模型（§7.3 N2 要防的唯一真实坏法）。
// 本文件由 App.tsx 的 FrontShell 路由 React.lazy 加载，禁止入口包静态 import。
// ============================================================================
import { useEffect, useState } from 'react'
import { useT } from '@/i18n'
import { productList, answerProduct } from '@/api/product'
import type { Product } from '@/api/product'

/** 知识层问答面板（登录态）：带两段短码提问，出结果并点名缺项 */
export default function ProductAnswers() {
  const [, t] = useT()
  const [codes, setCodes] = useState<{ tenantCode: string; pageCode: string }>({ tenantCode: '', pageCode: '' })
  const [question, setQuestion] = useState('')
  const [answer, setAnswer] = useState('')
  const [missing, setMissing] = useState<string[]>([])

  useEffect(() => {
    let alive = true
    productList()
      .then((r) => {
        if (!alive || !r.success) return
        const first: Product | undefined = Array.isArray(r.products) ? r.products[0] : undefined
        if (first) setCodes({ tenantCode: String(first.tenant_code || ''), pageCode: String(first.page_code || '') })
      })
      .catch(() => { /* 取不到产品时提问按钮保持禁用 */ })
    return () => { alive = false }
  }, [])

  /** 提交一问：只打一次 /api/product/answer，零额外模型请求 */
  const ask = () => {
    const q = question.trim()
    if (!q || !codes.tenantCode || !codes.pageCode) return
    answerProduct(codes.tenantCode, codes.pageCode, q)
      .then((r) => {
        if (!r.success) return
        setAnswer(String(r.answer || ''))
        setMissing(Array.isArray(r.missing) ? r.missing : [])
      })
      .catch(() => { /* 失败保留上一次答案，不转交模型 */ })
  }

  return (
    <section>
      <h2>{t('product.panel.answers')}</h2>
      <input value={question} onChange={(e) => setQuestion(e.target.value)}
             placeholder={t('product.panel.answerPlaceholder')} aria-label={t('product.panel.answerPlaceholder')} />
      <button onClick={ask} disabled={!codes.tenantCode || !codes.pageCode}>{t('product.panel.answerSend')}</button>
      {answer && (
        <div>
          <p data-testid="knowledge-answer">{answer}</p>
          <small>{t('product.panel.answerSource')}</small>
        </div>
      )}
      {missing.length > 0 && (
        <p data-testid="answer-missing">{t('product.panel.answerMissing')}：{missing.join('、')}</p>
      )}
    </section>
  )
}
