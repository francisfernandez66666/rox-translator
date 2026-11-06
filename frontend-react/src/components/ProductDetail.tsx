// ============================================================================
// components/ProductDetail.tsx — 一期 X2/X1「卖家写侧单页详情」（2026-11-06 第四轮裁定）
//
// 为什么要有它：一期商品卖的第一件是「备好的多语言物料」，而首版只交付了三张**读**面板——
// `productSave`／`factUpsert`／`assetPut`／`assetReview`／`productStatus`／`seoSet` 六个写口
// 在产品代码里零消费者 ⇒ 客户既建不了档也传不了物料，"备好"这一件只能由 API 或 UAT 夹具产出，
// 销售话术里那一句无法由客户自证（PROGRESS 2026-11-06 §四）。
//
// ★ 四条硬口径（改这一页前必读）：
//  ① **提交必须显式带上界面上呈现的 `visibility`／`source`**。后端 `update` 语义是
//     "省略＝没表态"（AGENTS §29），但那是为了救"只改一格被打回 gated"那一族；
//     界面既然把档位显示给人看了，就该把人看到的那一份原样交回去——两边同尺，不靠猜。
//  ② **确认框只在动作函数里弹一次**＋破坏性动作带 `danger:true`
//     （AGENTS §一·5 那条 doubleConfirmGate 按面板全集机械扫，外层 `onClick` 再弹一次当场红）。
//  ③ 本文件**只许**被 App.tsx 的 React.lazy 引到（入口包 T8 上限 420,000 B 不因它上涨）；
//     取词一律走懒加载档 `product.detail.*`／`product.panel.*`，禁止内联中文。
//  ④ 齐备度六格与对外页读侧仍由 `ProductShelf`／`ProductPublicPage` 承担，本页只补"写"这一半，
//     **不复制第二份读数逻辑**（同一个事实两个地方各算一遍＝迟早打架）。
//
// 本文件由 App.tsx 的 FrontShell 路由 React.lazy 加载，禁止入口包静态 import。
// ============================================================================
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useT } from '@/i18n'
import { useNav } from '@/lib/nav'
import { confirmDialog } from '@/components/uiDialogs'
import {
  productList, productSave, productDelete, productStatus, seoGet, seoSet,
  factList, factUpsert, assetList, assetPut, assetReview, assetDownload, assetUploadFile, assetFileDownload,
  kbPhrases, kbPhraseSave, type Product, type ProductFact, type ProductAsset, type PhrasesResp,
} from '@/api/product'

/** 事实项 kind 九值（与 store.ProductFactKinds 同一份名单，译文走 facts.kind.*） */
const FACT_KINDS = ['spec', 'material', 'price', 'moq', 'cert', 'capacity', 'warranty', 'usecase', 'other'] as const
/** 物料类型六档（与 store.ProductAssetTypes 同源；新增要两侧一起加） */
const ASSET_TYPES = ['tagline', 'onepager', 'quote_sheet', 'image_card', 'voice_script', 'qa_pack'] as const
/** 站点语种（与挂件/界面前端口径同一份，不在此再造档位） */
const ASSET_LANGS = ['en', 'ja', 'ko', 'de', 'fr', 'es', 'pt', 'ru', 'ar', 'th', 'zh-hant', 'zh'] as const

/** 一行「键值对」编辑态（事实与说法都用它，避免长出第二份表单逻辑） */
type Draft = { id?: number; kind: string; key: string; value: string; visibility: string; source: string }

/** 卖家产品详情页（登录态）：?id= 为产品主键；不带 id 时取列表第一份，都没有就进"建第一份"态 */
export default function ProductDetail() {
  const [, t] = useT()
  const nav = useNav()
  const queryId = useMemo(() => {
    try { return new URLSearchParams(nav.search || '').get('id') || '' } catch { return '' }
  }, [nav.search])

  const [products, setProducts] = useState<Product[]>([])
  const [current, setCurrent] = useState<Product | null>(null)
  const [facts, setFacts] = useState<ProductFact[]>([])
  const [assets, setAssets] = useState<ProductAsset[]>([])
  const [phrases, setPhrases] = useState<PhrasesResp['phrases']>([])
  const [seoOn, setSeoOn] = useState(true)
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const fileRef = useRef<HTMLInputElement | null>(null)

  // 基本信息表单（受控；保存时**整份**提交，避免"只发改过那一格"撞上 update 语义）
  const [form, setForm] = useState({ name: '', tagline: '', audience: '', use_case: '', style_notes: '', banned_notes: '' })
  const [factDraft, setFactDraft] = useState<Draft>({ kind: 'spec', key: '', value: '', visibility: 'gated', source: 'manual' })
  const [assetDraft, setAssetDraft] = useState({ id: 0, asset_type: 'onepager', lang: 'en', body: '', artifact_ref: '', visibility: 'gated', file_name: '' })
  const [phraseDraft, setPhraseDraft] = useState({ source_text: '', target_text: '', target_lang: 'en' })

  /** 拉一份产品清单，并把"当前这一份"定下来（query 优先，其次列表第一份） */
  const loadProducts = useCallback(async () => {
    const r = await productList().catch(() => null)
    const list = r && r.success && Array.isArray(r.products) ? (r.products as Product[]) : []
    setProducts(list)
    const pick = list.find((p) => String(p.id) === queryId) || list[0]
    setCurrent(pick || null)
    return pick || null
  }, [queryId])

  /** 按产品把四个段的数据一次拉齐（读侧失败只留空态，绝不清空已经填了一半的表单） */
  const loadAll = useCallback(async (p: Product | null) => {
    if (!p) { setFacts([]); setAssets([]); setPhrases([]); return }
    const pid = String(p.id)
    const [fr, ar, pr, sr] = await Promise.all([
      factList(pid).catch(() => null),
      assetList(pid).catch(() => null),
      kbPhrases(pid).catch(() => null),
      seoGet(pid).catch(() => null),
    ])
    setFacts(fr && fr.success && Array.isArray(fr.facts) ? (fr.facts as ProductFact[]) : [])
    setAssets(ar && ar.success && Array.isArray(ar.assets) ? (ar.assets as ProductAsset[]) : [])
    setPhrases(pr && pr.success && Array.isArray(pr.phrases) ? pr.phrases : [])
    if (sr && sr.success) setSeoOn(sr.seo_indexable !== false)
    setForm({
      name: String(p.name || ''), tagline: String(p.tagline || ''), audience: String(p.audience || ''),
      use_case: String(p.use_case || ''), style_notes: String(p.style_notes || ''), banned_notes: String(p.banned_notes || ''),
    })
  }, [])

  useEffect(() => { let alive = true; void (async () => { const p = await loadProducts(); if (alive) await loadAll(p) })(); return () => { alive = false } }, [loadProducts, loadAll])

  /** 保存基本信息：无 current 即建档（建档不收费＝零 token，§6.1），建档后立刻把两段短码显示出来 */
  const saveProduct = async () => {
    if (!form.name.trim()) { setNotice(t('product.detail.needName')); return }
    setBusy(true)
    const r = await productSave({ id: current ? String(current.id) : undefined, ...form }).catch(() => null)
    setBusy(false)
    if (!r || !r.success) { setNotice((r && (r as { message?: string }).message) || t('product.detail.saveFailed')); return }
    setNotice(t('product.detail.saved'))
    const p = await loadProducts()
    await loadAll(p)
  }

  /** 切换状态三值（draft／live／archived）；第四值由后端拒，界面根本不摆出来 */
  const changeStatus = async (status: string) => {
    if (!current) return
    setBusy(true)
    const r = await productStatus(String(current.id), status).catch(() => null)
    setBusy(false)
    if (!r || !r.success) { setNotice((r && (r as { message?: string }).message) || t('product.detail.saveFailed')); return }
    setNotice(t('product.detail.statusSaved'))
    await loadProducts()
  }

  /** 删除产品（破坏性）：确认只在动作函数里弹一次，且带 danger */
  const removeProduct = async () => {
    if (!current) return
    if (!(await confirmDialog({ header: t('product.detail.deleteTitle'), body: t('product.detail.deleteBody'), danger: true }))) return
    setBusy(true)
    const r = await productDelete(String(current.id)).catch(() => null)
    setBusy(false)
    if (!r || !r.success) { setNotice((r && (r as { message?: string }).message) || t('product.detail.saveFailed')); return }
    setNotice(t('product.detail.deleted'))
    const p = await loadProducts()
    await loadAll(p)
  }

  /** SEO 开关：唯一写点就是 /api/product/seo（改名与 live 翻转不许把它重新打开，§5.8 S4） */
  const toggleSeo = async () => {
    if (!current) return
    const next = !seoOn
    const r = await seoSet(String(current.id), next).catch(() => null)
    if (r && r.success) { setSeoOn(next); setNotice(t('product.detail.saved')) }
  }

  /** 复制对外链接（两段短码，创建即定、改名不改链） */
  const copyLink = async () => {
    if (!current) return
    const url = `${window.location.origin}/p/${current.tenant_code}/${current.page_code}`
    try { await navigator.clipboard.writeText(url); setNotice(t('product.detail.linkCopied')) } catch { setNotice(url) }
  }

  /** 事实项新增／更新：visibility 与 source 一定显式带上（口径①） */
  const saveFact = async () => {
    if (!current || !factDraft.key.trim() || !factDraft.value.trim()) { setNotice(t('product.detail.needFact')); return }
    const r = await factUpsert({
      product_id: String(current.id), id: factDraft.id, kind: factDraft.kind, key: factDraft.key,
      value: factDraft.value, visibility: factDraft.visibility, source: factDraft.source,
    }).catch(() => null)
    if (!r || !r.success) { setNotice((r && (r as { message?: string }).message) || t('product.detail.saveFailed')); return }
    setFactDraft({ kind: 'spec', key: '', value: '', visibility: 'gated', source: 'manual' })
    setNotice(t('product.detail.saved'))
    await loadAll(current)
  }

  /** 物料新增／更新（附件经 assetUploadFile 先落盘，这里只带 basename） */
  const saveAsset = async () => {
    if (!current || !assetDraft.body.trim()) { setNotice(t('product.detail.needAsset')); return }
    const r = await assetPut({
      product_id: String(current.id), id: assetDraft.id || undefined, asset_type: assetDraft.asset_type,
      lang: assetDraft.lang, body: assetDraft.body, artifact_ref: assetDraft.artifact_ref, visibility: assetDraft.visibility,
    }).catch(() => null)
    if (!r || !r.success) { setNotice((r && (r as { message?: string }).message) || t('product.detail.saveFailed')); return }
    setAssetDraft({ id: 0, asset_type: 'onepager', lang: 'en', body: '', artifact_ref: '', visibility: 'gated', file_name: '' })
    setNotice(t('product.detail.saved'))
    await loadAll(current)
  }

  /** 选文件即上传（失败只出声，不把已填的正文清掉） */
  const pickFile = async (file?: File) => {
    if (!file) return
    setBusy(true)
    const r = await assetUploadFile(file).catch(() => null)
    setBusy(false)
    if (!r || !r.success || !r.artifact_ref) { setNotice((r && (r as { message?: string }).message) || t('product.detail.uploadFailed')); return }
    setAssetDraft((d) => ({ ...d, artifact_ref: String(r.artifact_ref), file_name: file.name }))
    setNotice(t('product.detail.uploaded'))
  }

  /** 取回物料：JSON 正文口与文件口**共用同一条作废尺子**（后端拦 expired，这里如实显示那句话） */
  const fetchAsset = async (row: ProductAsset, kind: 'body' | 'file') => {
    if (kind === 'body') {
      const r = await assetDownload(Number(row.id)).catch(() => null)
      if (!r || !r.success) { setNotice((r && (r as { message?: string }).message) || t('product.detail.downloadBlocked')); return }
      setNotice(t('product.detail.bodyShown'))
      return
    }
    try { await assetFileDownload(Number(row.id)) } catch (e) { setNotice((e as Error).message || t('product.detail.downloadBlocked')) }
  }

  /** 已批准说法批量保存：入参形状只有一种 `{phrases:[…]}`（§29 那条"前端送数组、后端解单条"的裂缝防线） */
  const savePhrases = async () => {
    if (!current) return
    // 已存的行原样交回（后端按同一份源文幂等 upsert），加上草稿里新填的那一条
    const rows: Array<{ source_text: string; target_text: string; target_lang: string }> = phrases.map((p) => ({
      source_text: String(p.source_text || ''), target_text: String(p.target_text || ''),
      target_lang: String((p as { target_lang?: unknown }).target_lang || phraseDraft.target_lang),
    })).concat(
      phraseDraft.source_text.trim() && phraseDraft.target_text.trim()
        ? [{ source_text: phraseDraft.source_text, target_text: phraseDraft.target_text, target_lang: phraseDraft.target_lang }]
        : [],
    )
    if (rows.length === 0) { setNotice(t('product.detail.needPhrase')); return }
    const r = await kbPhraseSave(String(current.id), rows).catch(() => null)
    if (!r || !r.success) { setNotice((r && (r as { message?: string }).message) || t('product.detail.saveFailed')); return }
    setPhraseDraft({ source_text: '', target_text: '', target_lang: 'en' })
    setNotice(t('product.detail.imported'))
    await loadAll(current)
  }

  const publicPath = current ? `/p/${current.tenant_code}/${current.page_code}` : ''
  const statusOf = current ? String(current.status || 'draft') : 'draft'

  return (
    <section data-testid="product-detail">
      <h2>{t('product.detail.title')}</h2>
      {products.length > 1 && (
        <select data-testid="pd-switch" value={current ? String(current.id) : ''} onChange={(e) => nav.navigate(`/products/detail?id=${encodeURIComponent(e.target.value)}`)}>
          {products.map((p) => <option key={String(p.id)} value={String(p.id)}>{p.name || String(p.id)}</option>)}
        </select>
      )}
      {!current && <p data-testid="pd-empty">{t('product.detail.noProduct')}</p>}
      {notice && <p data-testid="pd-notice" role="status">{notice}</p>}

      {/* ── 段一：基本信息＋状态＋对外链接＋SEO ───────────────────────── */}
      <div data-testid="pd-basic">
        <h3>{t('product.detail.basic')}</h3>
        <label>{t('product.detail.name')}
          <input data-testid="pd-name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder={t('product.detail.namePh')} />
        </label>
        <label>{t('product.detail.tagline')}
          <input value={form.tagline} onChange={(e) => setForm({ ...form, tagline: e.target.value })} />
        </label>
        <label>{t('product.detail.audience')}
          <input value={form.audience} onChange={(e) => setForm({ ...form, audience: e.target.value })} />
        </label>
        <label>{t('product.detail.useCase')}
          <input value={form.use_case} onChange={(e) => setForm({ ...form, use_case: e.target.value })} />
        </label>
        <label>{t('product.detail.style')}
          <input value={form.style_notes} onChange={(e) => setForm({ ...form, style_notes: e.target.value })} />
        </label>
        <label>{t('product.detail.banned')}
          <input value={form.banned_notes} onChange={(e) => setForm({ ...form, banned_notes: e.target.value })} />
        </label>
        <button data-testid="pd-save" disabled={busy} onClick={() => void saveProduct()}>{current ? t('product.detail.save') : t('product.detail.createFirst')}</button>

        {current && (
          <>
            <p data-testid="pd-status">{t('product.detail.statusNow')}: {t(`product.detail.status.${statusOf}`)}</p>
            <div data-testid="pd-status-actions">
              {statusOf !== 'draft' && <button onClick={() => void changeStatus('draft')}>{t('product.detail.toDraft')}</button>}
              {statusOf !== 'live' && <button data-testid="pd-to-live" onClick={() => void changeStatus('live')}>{t('product.detail.toLive')}</button>}
              {statusOf !== 'archived' && <button onClick={() => void changeStatus('archived')}>{t('product.detail.toArchive')}</button>}
            </div>
            <p data-testid="pd-link">{t('product.detail.publicLink')}: <code>{publicPath}</code></p>
            <button onClick={() => void copyLink()}>{t('product.detail.copyLink')}</button>
            <label data-testid="pd-seo">
              <input type="checkbox" checked={seoOn} onChange={() => void toggleSeo()} />
              {t('product.detail.seoLabel')}
            </label>
            <p>{t('product.detail.seoHint')}</p>
            <button data-testid="pd-delete" onClick={() => void removeProduct()}>{t('product.detail.deleteBtn')}</button>
          </>
        )}
      </div>

      {/* ── 段二：事实项（只有 confirmed 才进对外内容；cert 只许人工来源确认）── */}
      <div data-testid="pd-facts">
        <h3>{t('product.panel.facts')}</h3>
        <select value={factDraft.kind} onChange={(e) => setFactDraft({ ...factDraft, kind: e.target.value })}>
          {FACT_KINDS.map((k) => <option key={k} value={k}>{t(`facts.kind.${k}`)}</option>)}
        </select>
        <input placeholder={t('product.detail.factKey')} value={factDraft.key} onChange={(e) => setFactDraft({ ...factDraft, key: e.target.value })} />
        <input placeholder={t('product.detail.factValue')} value={factDraft.value} onChange={(e) => setFactDraft({ ...factDraft, value: e.target.value })} />
        <select aria-label={t('product.detail.visibility')} value={factDraft.visibility} onChange={(e) => setFactDraft({ ...factDraft, visibility: e.target.value })}>
          <option value="gated">{t('product.detail.visGated')}</option>
          <option value="public">{t('product.detail.visPublic')}</option>
        </select>
        <select aria-label={t('product.detail.source')} value={factDraft.source} onChange={(e) => setFactDraft({ ...factDraft, source: e.target.value })}>
          <option value="manual">{t('product.detail.srcManual')}</option>
          <option value="ocr">{t('product.detail.srcOcr')}</option>
          <option value="external_ref">{t('product.detail.srcExternal')}</option>
        </select>
        <button data-testid="pd-fact-save" onClick={() => void saveFact()}>{t('product.detail.addFact')}</button>
        <p>{t('product.detail.certHint')}</p>
        <ul>
          {facts.map((f) => (
            <li key={String(f.id)} data-testid={`pd-fact-${f.status}`}>
              {t(`facts.kind.${f.kind}`)} · {f.key} · {f.value}
              {f.visibility === 'gated' && <em> · {t('product.panel.gatedBadge')}</em>}
              {f.source === 'external_ref' && <em> · {t('product.detail.refBadge')}</em>}
              <button onClick={() => setFactDraft({ id: Number(f.id), kind: String(f.kind), key: String(f.key), value: String(f.value), visibility: String(f.visibility), source: String(f.source ?? 'manual') })}>{t('product.detail.edit')}</button>
            </li>
          ))}
        </ul>
      </div>

      {/* ── 段三：物料货架（文本＋文件，审核三态，取回两个口）──────────── */}
      <div data-testid="pd-assets">
        <h3>{t('product.detail.assets')}</h3>
        <select value={assetDraft.asset_type} onChange={(e) => setAssetDraft({ ...assetDraft, asset_type: e.target.value })}>
          {ASSET_TYPES.map((k) => <option key={k} value={k}>{t(`product.detail.type.${k}`)}</option>)}
        </select>
        <select aria-label={t('product.detail.lang')} value={assetDraft.lang} onChange={(e) => setAssetDraft({ ...assetDraft, lang: e.target.value })}>
          {ASSET_LANGS.map((k) => <option key={k} value={k}>{k}</option>)}
        </select>
        <textarea data-testid="pd-asset-body" placeholder={t('product.detail.assetBody')} value={assetDraft.body} onChange={(e) => setAssetDraft({ ...assetDraft, body: e.target.value })} />
        <select aria-label={t('product.detail.visibility')} value={assetDraft.visibility} onChange={(e) => setAssetDraft({ ...assetDraft, visibility: e.target.value })}>
          <option value="gated">{t('product.detail.visGated')}</option>
          <option value="public">{t('product.detail.visPublic')}</option>
        </select>
        <input ref={fileRef} data-testid="pd-asset-file" type="file" onChange={(e) => void pickFile(e.target.files?.[0])} />
        {assetDraft.artifact_ref && <span data-testid="pd-asset-ref">{assetDraft.file_name || assetDraft.artifact_ref}</span>}
        <button data-testid="pd-asset-save" disabled={busy} onClick={() => void saveAsset()}>{assetDraft.id ? t('product.detail.save') : t('product.detail.addAsset')}</button>
        <ul>
          {assets.map((a) => (
            <li key={String(a.id)} data-testid={`pd-asset-${a.review_status}-${a.validity}`}>
              {t(`product.detail.type.${a.asset_type}`)} · {a.lang} · {t(`product.detail.review.${a.review_status}`)} · {t(`product.detail.validity.${a.validity}`)}
              <button onClick={() => void assetReview(Number(a.id), 'approved')}>{t('product.detail.approve')}</button>
              <button onClick={() => void assetReview(Number(a.id), 'rejected')}>{t('product.detail.reject')}</button>
              <button onClick={() => void fetchAsset(a, 'body')}>{t('product.detail.viewBody')}</button>
              {Boolean(a.artifact_ref) && <button data-testid="pd-asset-file-get" onClick={() => void fetchAsset(a, 'file')}>{t('product.detail.downloadFile')}</button>}
              <button onClick={() => setAssetDraft({ id: Number(a.id), asset_type: String(a.asset_type), lang: String(a.lang), body: String(a.body), artifact_ref: String(a.artifact_ref || ''), visibility: String(a.visibility), file_name: '' })}>{t('product.detail.edit')}</button>
            </li>
          ))}
        </ul>
      </div>

      {/* ── 段四：已批准说法（薄委托 kb_entries，不另开口径）──────────── */}
      <div data-testid="pd-phrases">
        <h3>{t('product.detail.phrases')}</h3>
        <p>{t('product.detail.phraseHint')}</p>
        <input placeholder={t('product.detail.phraseSrc')} value={phraseDraft.source_text} onChange={(e) => setPhraseDraft({ ...phraseDraft, source_text: e.target.value })} />
        <input placeholder={t('product.detail.phraseTgt')} value={phraseDraft.target_text} onChange={(e) => setPhraseDraft({ ...phraseDraft, target_text: e.target.value })} />
        <select aria-label={t('product.detail.lang')} value={phraseDraft.target_lang} onChange={(e) => setPhraseDraft({ ...phraseDraft, target_lang: e.target.value })}>
          {ASSET_LANGS.map((k) => <option key={k} value={k}>{k}</option>)}
        </select>
        <button data-testid="pd-phrase-save" onClick={() => void savePhrases()}>{t('product.detail.importBtn')}</button>
        <ul>
          {phrases.map((p, i) => <li key={`${p.source_text}-${i}`}>{p.source_text} → {p.target_text} <em>({String((p as { target_lang?: unknown }).target_lang ?? '')})</em></li>)}
        </ul>
      </div>
    </section>
  )
}
