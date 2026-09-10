// ============================================================================
// components/admin/BrandTermsP.tsx — 品牌名设置面板（2026-09-10 需求）
// 对知识库中的品牌术语（module=brand AND layer=1，如 极石→ROX）做独立可见与配置：
//  - 按品牌名分组展示各目标语言规定译法（外语统一为品牌名，如 ROX）
//  - 新增品牌名（自动为该包全部品牌对象补各语言条目；也可手动补单个语言）
//  - 单个条目编辑 / 删除
// 复用了既有 /api/admin/brand-terms 查询与 kb-entries add/update/delete 写接口，
// 保证「云端知识库单独可配」。
// ============================================================================
import { useEffect, useMemo, useState } from 'react'
import { Button, Table, Input, Select, Dialog, MessagePlugin, Popconfirm, Space, Tag } from 'tdesign-react'
import { brandTerms, kbPackages, kbEntryAdd, kbEntryUpdate, kbEntryDelete } from '@/api/kb'
import { langLabel } from '@/lib/langNames'

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

type Props = Record<string, never>

// 品牌术语支持的目标语言（与「极石→ROX」方言表一致；空值后端兜底 en）
const BRAND_LANGS = ['en', 'ar', 'de', 'es', 'fa', 'fr', 'hi', 'id_lang', 'it', 'ja', 'kk', 'ko', 'ms', 'my', 'pt', 'ru', 'th', 'tr', 'uk', 'vi', 'zh_hant']

// 知识库包精简结构（前端包选择器用）
type PkgItem = { id: number; name: string; code: string }

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
  const [packages, setPackages] = useState<PkgItem[]>([])
  const [pkgId, setPkgId] = useState(0)           // 当前选中的知识库包 ID
  const [terms, setTerms] = useState<BrandEntry[]>([])
  const [loading, setLoading] = useState(false)
  const [brandInput, setBrandInput] = useState('')   // 新增品牌名（中文，如「极石」）
  const [brandEn, setBrandEn] = useState('')          // 该品牌名统一外语译法（如 ROX）
  const [dlg, setDlg] = useState(false)               // 新增弹窗

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

  /** 新增品牌名：为该品牌在全部目标语言写入规定译法（源文无译法的语言自动补充） */
  const addBrand = async () => {
    const brand = brandInput.trim()
    const en = brandEn.trim()
    if (!brand) { MessagePlugin.warning('请输入品牌中文名'); return }
    if (!en) { MessagePlugin.warning('请输入品牌外语统一译法（如 ROX）'); return }
    try {
      for (const lc of BRAND_LANGS) {
        await kbEntryAdd({ package_id: pkgId, layer: 1, source_text: brand, target_lang: lc, target_text: en, module: 'brand' })
      }
      MessagePlugin.success('品牌名已新增（全语言译为 ' + en + '）')
      setBrandInput(''); setBrandEn(''); setDlg(false)
      await load()
    } catch (e) {
      MessagePlugin.error('新增失败：' + String((e as any)?.message || e))
    }
  }

  /** 新增单个语言的品牌条译法（品牌已存在但缺某语言时） */
  const addSingleLang = async (brand: string) => {
    const lang = window.prompt('输入目标语言代码（如 ar），为「' + brand + '」补该语言译法')
    if (!lang) return
    const text = window.prompt('输入该语言品牌译法（如 ROX）')
    if (!text) return
    try {
      await kbEntryAdd({ package_id: pkgId, layer: 1, source_text: brand, target_lang: lang.trim(), target_text: text.trim(), module: 'brand' })
      MessagePlugin.success('已补充 ' + lang.trim() + ' 译法')
      await load()
    } catch (e) { MessagePlugin.error('补充失败：' + String((e as any)?.message || e)) }
  }

  /** 修改单语言译法 */
  const editLang = async (g: { brand: string }, e: BrandEntry | null, lang: string) => {
    if (!e) return
    const text = window.prompt('修改「' + g.brand + '」的 ' + lang + ' 译法', e.target_text)
    if (!text || text.trim() === e.target_text) return
    try {
      await kbEntryUpdate({ id: e.id, layer: 1, source_text: g.brand, target_lang: lang, target_text: text.trim(), module: 'brand' })
      MessagePlugin.success('已更新')
      await load()
    } catch (err) { MessagePlugin.error('更新失败：' + String((err as any)?.message || err)) }
  }

  const removeEntry = async (id: number) => {
    try {
      await kbEntryDelete(id)
      MessagePlugin.success('已删除该语言条目')
      await load()
    } catch (e) { MessagePlugin.error('删除失败：' + String((e as any)?.message || e)) }
  }

  const inputStyle = { minWidth: 180 } as const
  const rowStyle = { display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' as const }
  const rowTop = { display: 'flex', alignItems: 'center', gap: 10, margin: '6px 0' } as const

  return (
    <div style={{ marginTop: 4 }}>
      {/* ===== 包选择 + 顶部说明 + 新增品牌名 ===== */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12, marginBottom: 10, flexWrap: 'wrap' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <span style={{ fontSize: 13, color: '#556' }}>知识库包</span>
          <Select value={pkgId} onChange={(v: any) => setPkgId(Number(v ?? 0))} style={{ minWidth: 240 }}
            options={packages.map(p => ({ label: `${p.name || p.code}`, value: p.id }))} />
        </div>
        <div style={{ fontSize: 13, color: '#667' }}>
          品牌名（如 极石 / 极石汽车）在外语统一译为规定写法（如 ROX）。翻译管线会强约束并剥离「ROX vehicles / ROX motor」等自创后缀。
        </div>
        <Button theme="primary" onClick={() => setDlg(true)}>＋ 新增品牌名</Button>
      </div>

      <Dialog header="新增品牌名" visible={dlg} onClose={() => setDlg(false)} footer={null} width={420}>
        <div style={rowTop}>
          <span style={{ width: 110, fontSize: 13 }}>品牌中文名</span>
          <Input value={brandInput} onChange={(v: any) => setBrandInput(String(v ?? ''))} placeholder="极石 或 极石汽车" style={inputStyle} />
        </div>
        <div style={rowTop}>
          <span style={{ width: 110, fontSize: 13 }}>外语统一译法</span>
          <Input value={brandEn} onChange={(v: any) => setBrandEn(String(v ?? ''))} placeholder="ROX" style={inputStyle} />
        </div>
        <div style={{ ...rowTop, marginTop: 16, justifyContent: 'flex-end' }}>
          <Button onClick={() => setDlg(false)}>取消</Button>
          <Button theme="primary" onClick={() => void addBrand()}>保存</Button>
        </div>
      </Dialog>

      {/* ===== 品牌名列表（按品牌分组，各语言译法一目了然） ===== */}
      {groups.length === 0 ? (
        <div style={{ color: '#889', fontSize: 13, padding: '16px 0' }}>{pkgId <= 0 ? '请先选择知识库包' : '暂无品牌名术语，点击「新增品牌名」添加。'}</div>
      ) : (
        <Table size="small" rowKey="brand" loading={loading} data={groups} maxHeight={520}
          columns={[
            { colKey: 'brand', title: '品牌名', width: 160, cell: ({ row }: any) => <Tag theme="primary" variant="light">{row.brand}</Tag> },
            { colKey: 'langs', title: '各语言译法', minWidth: 480, cell: ({ row }: any) => (
              <div style={rowStyle}>
                {BRAND_LANGS.filter(lc => row.langs[lc] !== undefined).map(lc => {
                  const entry = terms.find(t => t.source_text === row.brand && t.target_lang === lc)
                  return (
                    <span key={lc} style={{ display: 'inline-flex', alignItems: 'center', gap: 4, background: '#f3f5f9', borderRadius: 5, padding: '2px 8px', fontSize: 12 }}>
                      <span style={{ color: '#889', width: 26 }}>{langLabel(lc, 'zh') || lc}</span>
                      <b style={{ color: '#2f3542' }}>{row.langs[lc]}</b>
                      {entry && <a style={{ color: '#4a7dff', marginLeft: 2 }} onClick={() => void editLang(row, entry, lc)}>改</a>}
                      {entry && <a style={{ color: '#d45656', marginLeft: 2 }} onClick={() => void removeEntry(entry.id)}>✕</a>}
                    </span>
                  )
                })}
                <a style={{ color: '#889', fontSize: 12 }} onClick={() => void addSingleLang(row.brand)}>+补语言</a>
              </div>
            ) },
          ] as never} />
      )}
    </div>
  )
}