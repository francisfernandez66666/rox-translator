// ============================================================================
// components/admin/BrandTermsP.tsx — 品牌名设置面板（2026-09-10 需求）
// 对知识库中的品牌术语（module=brand AND layer=1，如 极石→ROX）做独立可见与配置：
//  - 按品牌名分组展示各目标语言规定译法（外语统一为品牌名，如 ROX）
//  - 新增品牌名（自动为该包全部品牌对象补各语言条目；也可手动补单个语言）
//  - 单个条目编辑 / 删除
// 复用了既有 /api/admin/brand-terms 查询与 kb-entries add/update/delete 写接口，
// 保证「云端知识库单独可配」。
// ★ F-25（2026-09-25 发布前 UAT 批G）交互收口：
//  ① 删除钮补齐现成 <CloseIcon size={14}/> 可视图形，删除动作前置 confirmDialog 二次确认；
//  ② 原三处 window.prompt（补语言取语种 / 取译法 / 改译法）全部换成站内 Dialog
//     （editDlg 状态：语言/原文/译文 三字段，语种走 BRAND_LANGS select + langLabel()），
//     提交仍走既有 kbEntryAdd / kbEntryUpdate 接口函数，零新接口。
// ============================================================================
import { useEffect, useMemo, useRef, useState } from 'react'
import { Badge, Button, CloseIcon, DataTable, Dialog } from '@/ui/langcross/src'
import { brandTerms, kbPackages, kbEntryAdd, kbEntryUpdate, kbEntryDelete } from '@/api/kb'
import { langLabel } from '@/lib/langNames'
import { useT, tpl as gtpl } from '@/i18n'
import { toastSuccess, toastError, toastWarn } from '@/lib/toastBus'
// ★ F-25：站内确认弹窗（替代原生确认交互，删除前必过这道闸）
import { confirmDialog } from '@/components/uiDialogs'

/** BrandEntry 品牌固定译法条目（层级/源语/目标语/生效模块） */
type BrandEntry = {
  id: number
  package_id: number
  layer: number
  source_lang: string
  source_text: string
  target_lang: string
  target_text: string
  module: string
}

/** Props 无入参（品牌术语面板为自足组件） */
type Props = Record<string, never>

// 品牌术语支持的目标语言（与「极石→ROX」方言表一致；空值后端兜底 en）
const BRAND_LANGS = ['en', 'ar', 'de', 'es', 'fa', 'fr', 'hi', 'id_lang', 'it', 'ja', 'kk', 'ko', 'ms', 'my', 'pt', 'ru', 'th', 'tr', 'uk', 'vi', 'zh_hant']

// 知识库包精简结构（前端包选择器用）
type PkgItem = { id: number; name: string; code: string }

/** ★ F-25：单语言补充 / 条目编辑弹窗状态（替代原三处 window.prompt）。
 *  entryId>0 = 修改既有条目（提交走 kbEntryUpdate）；entryId=0 = 为既有品牌补单语言条目（走 kbEntryAdd）。 */
type EditDlg = { open: boolean; entryId: number; brand: string; lang: string; text: string }

/** 按品牌名分组：source_text → 各语言译法 {target_lang: target_text} */
function groupByBrand(entries: BrandEntry[]): { brand: string; langs: Record<string, string>; ids: Record<string, number> }[] {
  const map = new Map<string, { brand: string; langs: Record<string, string>; ids: Record<string, number> }>()
  for (const e of entries) {
    const g = map.get(e.source_text) || { brand: e.source_text, langs: {}, ids: {} }
    g.langs[e.target_lang] = e.target_text
    g.ids[e.target_lang] = e.id
    map.set(e.source_text, g)
  }
  return Array.from(map.values()).sort((a, b) => a.brand.localeCompare(b.brand, 'zh'))
}

/**
 * BrandTermsP 品牌名设置面板（2026-09-10 需求：前端可见 + 知识库单独可配）。
 * 顶部自选知识库包（默认加载包列表首个），列出该包内品牌术语并按品牌名分组展示各语言译法。
 * 说明：品牌术语规定译法（如 ROX）会由翻译管线强约束——源文命中品牌名（极石/极石汽车）时，
 * 译文中品牌名统一剥离自创后缀（ROX vehicles/motor 等）并等于此处译法。修改后仅对后续翻译生效。
 */
export default function BrandTermsP(_props: Props) {
  // ★ F-25：uiLang 供 langLabel() 按界面语言展示语种名（zh 界面取中文名，其余取英文名）
  const [uiLang, t] = useT()
  const [packages, setPackages] = useState<PkgItem[]>([])
  const [pkgId, setPkgId] = useState(0)           // 当前选中的知识库包 ID
  const [terms, setTerms] = useState<BrandEntry[]>([])
  const [, setLoading] = useState(false)
  const [brandInput, setBrandInput] = useState('')   // 新增品牌名（中文，如「极石」）
  const [brandEn, setBrandEn] = useState('')          // 该品牌名统一外语译法（如 ROX）
  const [dlg, setDlg] = useState(false)               // 新增弹窗
  // ★ F-58（2026-09-26 〇-U 批 I-8）：21 语种串行写入的可中止/可观测态
  //   adding=是否在提交（按钮禁用 + 重入保护）、done=已完成条数（按钮上显进度）、
  //   cancelRef=取消标志（**用 ref 不用 state**：循环读的是当轮闭包，state 要等重渲染才更新，
  //   用 state 判取消会晚一轮——和 F-44 那类「读侧拿不到最新值」是同一个坑）。
  const [adding, setAdding] = useState(false)
  const [done, setDone] = useState(0)
  const cancelRef = useRef(false)

  // ★ F-25：单语言补充 / 编辑条目弹窗状态（原 window.prompt 流程的站内替代）
  const [editDlg, setEditDlg] = useState<EditDlg>({ open: false, entryId: 0, brand: '', lang: 'en', text: '' })

  // 首次加载包列表，默认选第一个（品牌主站默认包通常即首个）
  useEffect(() => {
    void (async () => {
      try {
        const r = await kbPackages()
        const list = (r.data as any)?.packages || (r as any).packages || []
        const pkgs: PkgItem[] = (Array.isArray(list) ? list : []).map((p: any) => ({ id: Number(p.id ?? p.package_id), name: String(p.name ?? p.code ?? ''), code: String(p.code ?? '') }))
        setPackages(pkgs)
        if (pkgs.length > 0) setPkgId(pkgs[0].id)
      } catch { /* 包列表加载失败静默 */ }
    })()
  }, [])

  const load = async () => {
    if (pkgId <= 0) return
    setLoading(true)
    try {
      const r = await brandTerms(pkgId)
      if (r.success) setTerms((r.terms as BrandEntry[]) || [])
    } catch { /* 静默：网络异常保留旧数据 */ }
    setLoading(false)
  }
  useEffect(() => { void load() }, [pkgId]) // eslint-disable-line react-hooks/exhaustive-deps
  const groups = useMemo(() => groupByBrand(terms), [terms])

  /** 新增品牌名：为该品牌在全部目标语言写入规定译法（源文无译法的语言自动补充）
   *
   * ★ F-58（2026-09-26 〇-U 批 I-8）：21 语种串行写入的**中止与诚实回执**。
   *   旧形态三处失真：① 循环里不看 `r.success`（业务失败回 200+success:false 时静默继续，
   *   最后照样 toastSuccess「品牌名已新增（全语言译为 X）」）；② 弹窗「取消」只 setDlg(false)，
   *   循环照跑完 21 次（本轮 UAT 实测：关掉窗口后请求仍在逐条发出）；③ 提交中按钮不禁用，
   *   可以再点一次保存 ⇒ 同一品牌并发写两轮。
   *   现在：cancelRef 每次 await 后判一次（**已经在途的那一条不打断**——没有 AbortSignal 通道
   *   可用，硬造中断只会把已写入的条目状态搞得更不清楚），按钮在 adding 期间改为进度文案，
   *   回执按实际结果分三档（全成 / 部分失败 / 被中止），部分失败时逐语种列出失败原因。
   *   ★ 不要改成「一次请求带多语种」：后端 kb 条目走单语种白名单
   *   （admin_kb.go isValidLangColumn 不认 `en|ar|de` 这类拼接串，直接 400——报告里那句已被证伪）。 */
  const addBrand = async () => {
    if (adding) return // 提交中重入保护（按钮已禁用，这里再兜一层，防键盘回车二次触发）
    const brand = brandInput.trim()
    const en = brandEn.trim()
    if (!brand) { toastWarn(t('bt.needBrand')); return }
    if (!en) { toastWarn(t('bt.needEn')); return }
    cancelRef.current = false
    setAdding(true)
    setDone(0)
    const fails: string[] = []
    let ok = 0
    try {
      for (const lc of BRAND_LANGS) {
        if (cancelRef.current) break // ★ 取消：已写完的保留（逐条即时生效，不做回滚），只停止后续语种
        let why = ''
        try {
          const r = await kbEntryAdd({ package_id: pkgId, layer: 1, source_text: brand, target_lang: lc, target_text: en, module: 'brand' })
          if (!r.success) why = String(r.message || t('common.fail'))
        } catch (e) { why = String((e as any)?.message || e) }
        if (why) fails.push(`${lc}：${why}`)
        else ok++
        setDone((n) => n + 1)
      }
      if (fails.length) {
        // 部分失败如实报「写成 k/总数 + 逐语种原因」，绝不发成功 toast（与 F-23/F-24 文案诚实同口径）
        toastError(gtpl('bt.addedPartial', { ok, total: BRAND_LANGS.length, why: fails.join('；') }))
      } else if (cancelRef.current) {
        toastWarn(gtpl('bt.addCancelled', { ok, total: BRAND_LANGS.length }))
      } else {
        toastSuccess(gtpl('bt.added', { en }))
      }
      // 中止/部分失败同样**不清空输入框**：客户可以直接改两个字段重跑一次，
      // 已写进去的语种走 kb-entries 幂等覆盖（同 package+source+target_lang 更新而非重复插行），重跑安全。
      if (!fails.length && !cancelRef.current) { setBrandInput(''); setBrandEn(''); setDlg(false) }
      await load() // 无论成/败/中止都重取列表：部分写入的行也要马上可见，别停在旧快照上
    } catch (e) {
      toastError(gtpl('bt.addFail', { err: String((e as any)?.message || e) }))
    } finally { setAdding(false) }
  }


  /** ★ F-25：打开「为既有品牌补单语言条目」弹窗（原 window.prompt 双连问的入口替代） */
  const openAddLang = (brand: string) => setEditDlg({ open: true, entryId: 0, brand, lang: 'en', text: '' })

  /** ★ F-25：打开「修改单语言译法」弹窗，三字段以条目现值预填（原 window.prompt 的入口替代） */
  const openEditLang = (brand: string, e: BrandEntry) =>
    setEditDlg({ open: true, entryId: e.id, brand, lang: e.target_lang, text: e.target_text })

  /** ★ F-25：编辑弹窗提交——entryId=0 走 kbEntryAdd 补条目，否则走 kbEntryUpdate 改条目；
   *  失败留在框内可重试（与「新增品牌名」弹窗同口径），成功才关窗回刷列表。 */
  const submitEdit = async () => {
    const brand = editDlg.brand.trim()
    const text = editDlg.text.trim()
    if (!brand) { toastWarn(t('bt.needBrand')); return }
    // 空译文复用现成 bt.needEn 文案提示（语义即「请输入品牌译法」），F-25 不为此再造新键
    if (!text) { toastWarn(t('bt.needEn')); return }
    try {
      if (editDlg.entryId > 0) {
        await kbEntryUpdate({ id: editDlg.entryId, layer: 1, source_text: brand, target_lang: editDlg.lang, target_text: text, module: 'brand' })
        toastSuccess(t('bt.updated'))
        setEditDlg((d) => ({ ...d, open: false }))
      } else {
        await kbEntryAdd({ package_id: pkgId, layer: 1, source_text: brand, target_lang: editDlg.lang, target_text: text, module: 'brand' })
        toastSuccess(gtpl('bt.langAdded', { lang: editDlg.lang }))
        setEditDlg((d) => ({ ...d, open: false }))
      }
      await load()
    } catch (e) {
      // 两分支各用现成失败文案键（写字面量而非三元拼接，保 missingKeyGate 静态可见）
      if (editDlg.entryId > 0) toastError(gtpl('bt.updateFail', { err: String((e as any)?.message || e) }))
      else toastError(gtpl('bt.langFail', { err: String((e as any)?.message || e) }))
    }
  }

  /** ★ F-25：删除前置站内 confirmDialog（danger 红框）二次确认——用户点「确认」才调 kbEntryDelete，
   *  取消/关闭一律不发删除请求。 */
  const removeEntry = async (brand: string, lang: string, id: number) => {
    const ok = await confirmDialog({
      header: t('bt.delConfirmTitle'),
      body: gtpl('bt.delConfirmBody', { brand, lang: langLabel(lang, uiLang) || lang }),
      danger: true,
    })
    if (!ok) return
    try {
      await kbEntryDelete(id)
      toastSuccess(t('bt.entryDeleted'))
      await load()
    } catch (e) { toastError(gtpl('bt.deleteFail', { err: String((e as any)?.message || e) })) }
  }

  const inputStyle = { minWidth: 180 } as const
  const rowStyle = { display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' as const }
  const rowTop = { display: 'flex', alignItems: 'center', gap: 10, margin: '6px 0' } as const

  return (
    <div style={{ marginTop: 4 }}>
      {/* ===== 包选择 + 顶部说明 + 新增品牌名 ===== */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12, marginBottom: 10, flexWrap: 'wrap' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <span style={{ fontSize: 15, color: 'var(--adm-hint)' }}>{t('bt.pkgLabel')}</span>
          <select className="lc-select" value={String(pkgId)} onChange={(e) => setPkgId(Number(e.target.value))} style={{ minWidth: 240 }}>
            {packages.map(p => <option key={p.id} value={String(p.id)}>{`${p.name || p.code}`}</option>)}
          </select>
        </div>
        <div style={{ fontSize: 15, color: 'var(--adm-hint)' }}>
          {t('bt.hint')}
        </div>
        <Button variant="primary" onClick={() => setDlg(true)}>{t('bt.new')}</Button>
      </div>

      {/* ★ F-58：提交中确认钮变进度文案并挡住重入，取消钮改为「中止」（置 cancelRef，循环下一条即停）。
          窗口在提交期间不给关（关窗≠中止，旧形态就是关窗后 21 条继续发完）——中止后由循环自己收尾关窗。 */}
      <Dialog title={t('bt.newTitle')} open={dlg}
              onCancel={() => { if (adding) { cancelRef.current = true } else { setDlg(false) } }}
              confirmText={adding ? gtpl('bt.adding', { ok: done, total: BRAND_LANGS.length }) : t('bt.save')}
              cancelText={adding ? t('bt.stop') : t('bt.cancel')}
              onConfirm={() => void addBrand()}>
        <div style={rowTop}>
          <span style={{ width: 110, fontSize: 15 }}>{t('bt.brandLabel')}</span>
          <input className="lc-input" value={brandInput} onChange={(e) => setBrandInput(String(e.target.value ?? ''))} placeholder={t('bt.brandPlaceholder')} style={inputStyle} />
        </div>
        <div style={rowTop}>
          <span style={{ width: 110, fontSize: 15 }}>{t('bt.enLabel')}</span>
          <input className="lc-input" value={brandEn} onChange={(e) => setBrandEn(String(e.target.value ?? ''))} placeholder="ROX" style={inputStyle} />
        </div>
      </Dialog>

      {/* ===== ★ F-25：单语言补充 / 编辑条目弹窗（站内 Dialog，替代原三处 window.prompt）。
             语言字段用现成 BRAND_LANGS 常量渲染 <select> + langLabel() 按界面语言显示语种名，
             禁止手输裸语种代码；原文/译文为普通输入框，提交走既有 kbEntryAdd / kbEntryUpdate。 ===== */}
      <Dialog title={t('bt.editLangTitle')} open={editDlg.open} onCancel={() => setEditDlg((d) => ({ ...d, open: false }))} confirmText={t('bt.save')} cancelText={t('bt.cancel')} onConfirm={() => void submitEdit()}>
        <div style={rowTop}>
          <span style={{ width: 110, fontSize: 15 }}>{t('bt.formLangLabel')}</span>
          <select className="lc-select" value={editDlg.lang} onChange={(e) => setEditDlg((d) => ({ ...d, lang: e.target.value }))} style={inputStyle}>
            {BRAND_LANGS.map(lc => <option key={lc} value={lc}>{langLabel(lc, uiLang) || lc}</option>)}
          </select>
        </div>
        <div style={rowTop}>
          <span style={{ width: 110, fontSize: 15 }}>{t('bt.brandLabel')}</span>
          <input className="lc-input" value={editDlg.brand} onChange={(e) => setEditDlg((d) => ({ ...d, brand: String(e.target.value ?? '') }))} placeholder={t('bt.brandPlaceholder')} style={inputStyle} />
        </div>
        <div style={rowTop}>
          <span style={{ width: 110, fontSize: 15 }}>{t('bt.formTextLabel')}</span>
          <input className="lc-input" value={editDlg.text} onChange={(e) => setEditDlg((d) => ({ ...d, text: String(e.target.value ?? '') }))} placeholder="ROX" style={inputStyle} />
        </div>
      </Dialog>

      {/* ===== 品牌名列表（按品牌分组，各语言译法一目了然） ===== */}
      {groups.length === 0 ? (
        <div style={{ color: 'var(--adm-faint)', fontSize: 15, padding: '16px 0' }}>{pkgId <= 0 ? t('bt.needPkg') : t('bt.empty')}</div>
      ) : (
        <DataTable<any> rowKey={(row) => String(row.brand)} rows={groups} columns={[
            { key: 'brand', title: t('bt.colBrand'), width: 160, render: (row) => <Badge>{row.brand}</Badge> },
            { key: 'langs', title: t('bt.colLangs'), width: '55%', render: (row) => (
              <div style={rowStyle}>
                {BRAND_LANGS.filter(lc => row.langs[lc] !== undefined).map(lc => {
                  const entry = terms.find(t => t.source_text === row.brand && t.target_lang === lc)
                  return (
                    /* 单语言条目芯片：语言缩写 + 规定译法 + 行内「编辑 / 删除」动作。
                       ★ F-25 收口：编辑/删除按钮文字与图形均走站内控件——删除钮内放现成
                       <CloseIcon size={14}/> 作可视入口（此前 emoji 清理后只剩 aria-label 不可见），
                       两个按钮的 type="button" 与 aria-label 粘连写法顺手补空格。 */
                    <span key={lc} style={{ display: 'inline-flex', alignItems: 'center', gap: 4, background: 'var(--adm-soft)', borderRadius: 5, padding: '2px 8px', fontSize: 14 }}>
                      <span style={{ color: 'var(--adm-faint)', width: 26 }}>{langLabel(lc, 'zh') || lc}</span>
                      <b style={{ color: 'var(--npz-text-1)' }}>{row.langs[lc]}</b>
                      {entry && <button type="button" aria-label={t('bt.editShort')} style={{ border:'none', background:'none', padding: 0, font:'inherit', cursor:'pointer', color:'#E7E9EA', marginInlineStart: 2 }} onClick={() => openEditLang(row.brand, entry)}>{t('bt.editShort')}</button>}
                      {entry && <button type="button" aria-label={t('bt.delEntry')} style={{ border:'none', background:'none', padding: 0, font:'inherit', cursor:'pointer', color:'var(--lc-danger)', marginInlineStart: 2, display: 'inline-flex', alignItems: 'center' }} onClick={() => void removeEntry(row.brand, lc, entry.id)}><CloseIcon size={14} /></button>}
                    </span>
                  )
                })}
                <button type="button" aria-label={t('bt.addLang')} style={{ border: 'none', background: 'none', padding: 0, font: 'inherit', cursor: 'pointer', color: 'var(--adm-faint)', fontSize: 14 }} onClick={() => openAddLang(row.brand)}>{t('bt.addLang')}</button>
              </div>
            ) },
          ]}  />
      )}
    </div>
  )
}
