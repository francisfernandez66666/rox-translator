// ============================================================================
// components/LangMultiSelect.tsx — 目标语言多选（自绘 Popup 下拉 + 自定义语言）
// ★ 2026-09-15 重构（任务⑤）：原 TDesign 多选 Select 在触发框内渲染已选标签，
//   与聊天/工单输入框上方的语言 chip 行重复展示、且标签换行挤占输入区。
//   换为 Popup + 自绘分组勾选列表：触发器只显示占位文案（永远单行、不沉标签），
//   选中结果唯一展示位 = 外部 <LangChips/> 行；面板内保留勾选态供增删操作。
// 功能保留：KB 九语分组 / 其他常用语分组 / 后端语言动态覆盖 / 搜索过滤 / 手输自定义语言。
// ============================================================================
import { useEffect, useMemo, useRef, useState } from 'react'
import { API_BASE } from '@/api'
import { Popup, Input, Button } from 'tdesign-react'
import { t } from '@/i18n'

// ============ 本文件职责中文说明 ============
// 目标语言多选组件（Popup 自绘下拉）+ 共用已选语言 chip 行 LangChips。
// ========================================

// KB 九语（与后端 /api/translation/langs 对齐的本地兜底；挂载后由父组件动态覆盖可选）
// 知识库支持的高质量目标语言（本地兜底，后端返回后覆盖名称与国旗）
const KB_LANGS: Array<{ code: string; label: string; flag?: string }> = [
  { code: 'en', label: '英语', flag: '🇬🇧' },
  { code: 'ru', label: '俄语', flag: '🇷🇺' },
  { code: 'ar', label: '阿拉伯语', flag: '🇸🇦' },
  { code: 'es', label: '西班牙语', flag: '🇪🇸' },
  { code: 'pt', label: '葡萄牙语', flag: '🇵🇹' },
  { code: 'fr', label: '法语', flag: '🇫🇷' },
  { code: 'kk', label: '哈萨克语（哈萨克斯坦）', flag: '🇰🇿' },
  { code: 'de', label: '德语', flag: '🇩🇪' },
  { code: 'zh_hant', label: '繁体中文', flag: '🇹🇼' },
]

// 其他常用语言（非 KB，走 AI 翻译）分组选项
const OTHER_LANGS: Array<{ code: string; label: string; flag?: string }> = [
  { code: 'ja', label: '日语', flag: '🇯🇵' },
  { code: 'ko', label: '韩语', flag: '🇰🇷' },
  { code: 'th', label: '泰语', flag: '🇹🇭' },
  { code: 'vi', label: '越南语', flag: '🇻🇳' },
  { code: 'ms', label: '马来语', flag: '🇲🇾' },
  { code: 'id_lang', label: '印尼语', flag: '🇮🇩' },
  { code: 'it', label: '意大利语', flag: '🇮🇹' },
  { code: 'pl', label: '波兰语', flag: '🇵🇱' },
  { code: 'tr', label: '土耳其语', flag: '🇹🇷' },
]

// LangMultiSelect 入参：value 当前选中语言代码数组；onChange 变更回调；kbLangs 覆盖 KB 分组
interface Props {
  value: string[]
  onChange: (v: string[]) => void
  /** 覆盖 KB 分组选项（默认本地九语） */
  kbLangs?: string[]
}

// langDisplay 语言代码 → 展示元信息（国旗 + 本地化名）；未知代码给 🤖 兜底
export function langDisplay(code: string): { flag: string; label: string } {
  const hit = [...KB_LANGS, ...OTHER_LANGS].find((x) => x.code === code)
  return hit ? { flag: hit.flag || '🌐', label: hit.label } : { flag: '🤖', label: code }
}

/** 已选语言 chip 行（选中结果的唯一展示位）：每项可 ✕ 移除 */
export function LangChips({ langs, onRemove }: { langs: string[]; onRemove: (next: string[]) => void }) {
  if (!langs.length) return null
  return (
    <div data-testid="lang-chips" style={{ display: 'flex', flexWrap: 'wrap', gap: 6, paddingBottom: 8 }}>
      {langs.map((l) => {
        const d = langDisplay(l)
        return (
          <span key={l} className="tag tag-lang" style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
            {d.flag} {d.label}
            <Button size="small" variant="text" theme="default" className="tag-close"
                    aria-label={`remove-${l}`} onClick={() => onRemove(langs.filter((x) => x !== l))}>✕</Button>
          </span>
        )
      })}
    </div>
  )
}

/** 分组选项结构（供列表渲染与过滤） */
interface Opt { group: string; code: string; flag: string; label: string }

// 默认导出组件：目标语言多选（Popup 分组勾选列表 + 搜索过滤 + 自定义语言输入）
export default function LangMultiSelect({ value, onChange, kbLangs }: Props) {
  const [open, setOpen] = useState(false)
  const [custom, setCustom] = useState('')
  const [query, setQuery] = useState('')
  const [extraCodes, setExtraCodes] = useState<string[]>([])
  // 从后端加载 KB 支持语言（新语言升级进 KB 区，名称/国旗覆盖本地兜底）
  const [apiKb, setApiKb] = useState<Array<{ code: string; label: string; flag?: string }>>([])
  const searchRef = useRef<HTMLDivElement>(null)

  // 挂载时从后端拉取 KB 语言列表，覆盖/补充本地兜底选项
  useEffect(() => {
    ;(async () => {
      try {
        const resp = await fetch(`${API_BASE}/api/translation/langs`)
        if (!resp.ok) return
        const data = await resp.json()
        if (data.kb_langs?.length) {
          setApiKb(data.kb_langs.map((l: any) => ({ code: l.code, label: l.name, flag: l.flag })))
        }
      } catch { /* 静默保留内置选项 */ }
    })()
  }, [])

  // 构造分组选项：KB 分组 + 其他语言分组 + 自定义语言分组（如有）
  const options = useMemo<Opt[]>(() => {
    const kbSet = kbLangs?.length ? kbLangs : KB_LANGS.map((x) => x.code)
    const kbMap = new Map<string, { code: string; label: string; flag?: string }>()
    for (const x of [...KB_LANGS, ...apiKb]) kbMap.set(x.code, { ...(kbMap.get(x.code) || {}), ...x })
    const out: Opt[] = []
    for (const x of [...kbMap.values()].filter((x) => kbSet.includes(x.code)))
      out.push({ group: t('chat.kbGroup'), code: x.code, flag: x.flag || '🌐', label: x.label })
    for (const x of OTHER_LANGS)
      out.push({ group: t('chat.otherGroup'), code: x.code, flag: x.flag || '🌐', label: x.label })
    for (const c of extraCodes)
      out.push({ group: t('chat.customGroup'), code: c, flag: '🤖', label: langDisplay(c).label })
    return out
  }, [kbLangs, extraCodes, apiKb])

  // 面板内可见选项（按搜索词过滤 名称/代码）
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return options
    return options.filter((o) => o.label.toLowerCase().includes(q) || o.code.toLowerCase().includes(q))
  }, [options, query])

  // 分组切块（保持 KB→其他→自定义 顺序渲染）
  const grouped = useMemo(() => {
    const m = new Map<string, Opt[]>()
    for (const o of shown) { if (!m.has(o.group)) m.set(o.group, []); m.get(o.group)!.push(o) }
    return [...m.entries()]
  }, [shown])

  /** toggle 增删一个语言（选中态即开关） */
  function toggle(code: string) {
    onChange(value.includes(code) ? value.filter((x) => x !== code) : [...value, code])
  }

  /** onEnterKey 搜索框回车：勾选首个过滤命中（无命中且为纯代码输入 → 走自定义添加） */
  function onEnterKey() {
    const first = shown[0]
    if (first) { toggle(first.code); setQuery(''); return }
    if (custom.trim()) addCustom()
  }

  // 将手输语言代码加入自定义分组与选中值
  function addCustom() {
    const code = custom.trim()
    if (!code || value.includes(code)) { setCustom(''); return }
    setExtraCodes((prev) => (prev.includes(code) ? prev : [...prev, code]))
    onChange([...value, code])
    setCustom('')
  }

  // 下拉面板内容：搜索过滤 + 分组勾选列表 + 自定义语言添加
  const panel = (
    <div style={{ width: 320 }} data-testid="lang-multi-panel">
      <div ref={searchRef} style={{ padding: 8, borderBottom: '1px solid #eee' }}>
        <Input size="small" autofocus value={query}
               placeholder={t('chat.langPlaceholder')}
               aria-label={t('chat.langPlaceholder')}
               onChange={(v) => setQuery(v as string)}
               onEnter={onEnterKey} />
      </div>
      <div style={{ maxHeight: 280, overflowY: 'auto', padding: '4px 0' }}>
        {grouped.map(([g, opts]) => (
          <div key={g}>
            <div style={{ fontSize: 12, color: '#999', padding: '4px 12px' }}>{g}</div>
            {opts.map((o) => {
              const on = value.includes(o.code)
              return (
                <div key={o.code} role="option" aria-selected={on} data-lang={o.code}
                     onClick={() => toggle(o.code)}
                     style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '6px 12px', cursor: 'pointer',
                              background: on ? 'rgba(0,82,217,.06)' : undefined, fontSize: 13 }}>
                  <span style={{ width: 18 }}>{o.flag}</span>
                  <span style={{ flex: 1 }}>{o.label}</span>
                  {on && <span style={{ color: 'var(--td-brand-color, #0052d9)', fontWeight: 700 }}>✓</span>}
                </div>
              )
            })}
          </div>
        ))}
        {!shown.length && <div style={{ padding: '10px 12px', fontSize: 12, color: '#999' }}>{t('chat.noLangHit')}</div>}
      </div>
      <div style={{ padding: '8px 12px', borderTop: '1px solid #e7e7e7', display: 'flex', gap: 6, alignItems: 'center' }}
           onClick={(e) => e.stopPropagation()}>
        <Input size="small" style={{ flex: 1 }} value={custom}
               placeholder={t('chat.customLangPlaceholder')}
               onChange={(v) => setCustom(v as string)}
               onEnter={() => addCustom()} />
        <Button size="small" variant="outline" disabled={!custom.trim()} onClick={addCustom}>＋</Button>
      </div>
    </div>
  )

  return (
    <Popup visible={open} onVisibleChange={setOpen} trigger="click" placement="bottom-left"
           overlayClassName="lang-multi-popup" destroyOnClose={false} content={panel}>
      {/* 触发器：单行占位按钮，永不渲染已选标签（选中结果只在外部 LangChips 展示） */}
      <button type="button" data-testid="lang-multi-trigger"
              className="t-button t-button--variant-outline t-button--theme-default"
              style={{ width: '100%', textAlign: 'left', display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 6, color: value.length ? 'inherit' : '#999' }}>
        <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>🌐 {t('chat.langPlaceholder')}{value.length ? '' : ''}</span>
        <span aria-hidden style={{ fontSize: 10 }}>▾</span>
      </button>
    </Popup>
  )
}
