// ============================================================================
// components/LangMultiSelect.tsx — 目标语言多选（自绘 CSS 下拉 + 自定义语言）
//  2026-09-15 重构（任务⑤）：触发框内不再渲染已选标签（与外部 chip 行重复展示、
//   换行挤占输入区）。触发器只显示占位文案（永远单行），
//   选中结果唯一展示位 = 外部 <LangChips/> 行；面板内保留勾选态供增删操作。
//  2026-09-18 迁移：TDesign Popup/Input/Button → 页面级 CSS 浮层 + 原生控件
//   （langcross 令牌着色，浮层 inset 底 + 1.2px 灰阶 card 框（★ 〇-P 交付档）+ r10）。
// 功能保留：KB 九语分组 / 其他常用语分组 / 后端语言动态覆盖 / 搜索过滤 / 手输自定义语言。
// ============================================================================
import { useEffect, useMemo, useRef, useState } from 'react'
import { API_BASE } from '@/api'
import { CloseIcon } from '@/ui/langcross/src'
import { t, useLang } from '@/i18n'
// ★ #23（2026-09-19）：语言名展示改走 langLabel（zh* 取中文名，其余界面语言取英文名），
//   目标语言列表不再对国外用户恒显中文
import { langLabel } from '@/lib/langNames'

// ============ 本文件职责中文说明 ============
// 目标语言多选组件（CSS 自绘下拉）+ 共用已选语言 chip 行 LangChips。
// ========================================

// KB 九语（与后端 /api/translation/langs 对齐的本地兜底；挂载后由父组件动态覆盖可选）
// 知识库支持的高质量目标语言（本地兜底，后端返回后覆盖名称与国旗）
const KB_LANGS: Array<{ code: string; label: string; flag?: string }> = [
  { code:'en', label:'英语', flag:''},
  { code:'ru', label:'俄语', flag:''},
  { code:'ar', label:'阿拉伯语', flag:''},
  { code:'es', label:'西班牙语', flag:''},
  { code:'pt', label:'葡萄牙语', flag:''},
  { code:'fr', label:'法语', flag:''},
  { code:'kk', label:'哈萨克语（哈萨克斯坦）', flag:''},
  { code:'de', label:'德语', flag:''},
  { code:'zh_hant', label:'繁体中文', flag:''},
]

// 其他常用语言（非 KB，走 AI 翻译）分组选项
const OTHER_LANGS: Array<{ code: string; label: string; flag?: string }> = [
  { code:'ja', label:'日语', flag:''},
  { code:'ko', label:'韩语', flag:''},
  { code:'th', label:'泰语', flag:''},
  { code:'vi', label:'越南语', flag:''},
  { code:'ms', label:'马来语', flag:''},
  { code:'id_lang', label:'印尼语', flag:''},
  { code:'it', label:'意大利语', flag:''},
  { code:'pl', label:'波兰语', flag:''},
  { code:'tr', label:'土耳其语', flag:''},
]

// LangMultiSelect 入参：value 当前选中语言代码数组；onChange 变更回调；kbLangs 覆盖 KB 分组；
// compact 触发器改「胶囊」档（★ 〇-M：宽度随内容、高 28、透明底描边胶囊，供输入区内的一行工具条用；
//   默认 false 保持工单页那种整宽单行触发框）
interface Props {
  value: string[]
  onChange: (v: string[]) => void
  /** 覆盖 KB 分组选项（默认本地九语） */
  kbLangs?: string[]
  /** 触发器走胶囊档（一行工具条内用） */
  compact?: boolean
}

// langDisplay 语言代码 → 展示元信息（国旗 + 本地化名）；未知代码给  兜底
export function langDisplay(code: string): { flag: string; label: string } {
  const hit = [...KB_LANGS, ...OTHER_LANGS].find((x) => x.code === code)
  return hit ? { flag: hit.flag || '', label: hit.label } : { flag: '', label: code }
}

/** 已选语言 chip 行（选中结果的唯一展示位）：每项可移除
 *  ★ #23：chip 名称走 langLabel，随界面语言取中/英
 *  ★ 〇-M（2026-09-23，即时翻译输入区压成一行工具条）新增两个可选入参，默认值保持工单页老形态：
 *    - `max`：内联最多展示几颗，其余折成「+n」；点「+n」就地展开全部（不是只读计数——
 *      折起来的语种仍要能一键 × 掉，否则就得开面板滚动找勾，功能尺寸不能因为压缩而缩水）。
 *    - `dense`：行内模式（工具条里用），去掉独立成行时的 8px 下外边距。 */
export function LangChips({ langs, onRemove, max, dense }: {
  langs: string[]; onRemove: (next: string[]) => void; max?: number; dense?: boolean
}) {
  const lang = useLang()
  const [expanded, setExpanded] = useState(false)
  if (!langs.length) return null
  const limit = !expanded && max && max > 0 ? max : langs.length
  const shown = langs.slice(0, limit)
  const rest = langs.slice(limit)
  const nameOf = (l: string) => (langLabel(l, lang) === l ? langDisplay(l).label : langLabel(l, lang))
  return (
    <div data-testid="lang-chips" style={{ display: 'flex', flexWrap: 'wrap', gap: 6, alignItems: 'center', paddingBottom: dense ? 0 : 8 }}>
      {shown.map((l) => {
        const d = langDisplay(l)
        const label = langLabel(l, lang) === l ? d.label : langLabel(l, lang)
        return (
          <span key={l} className="tag tag-lang" style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
            {d.flag} {label}
            <button type="button" className="lms-chip-close"
 aria-label={`remove-${l}`} onClick={() => onRemove(langs.filter((x) => x !== l))}>
              <CloseIcon size={10} />
      </button>
          </span>
        )
      })}
      {/* 折叠位：语言名列表进 title/aria-label，读屏与悬浮都能看到被折走的是哪几个 */}
      {!!rest.length && (
        <button type="button" className="lms-chips-more" data-testid="lang-chips-more"
                title={rest.map(nameOf).join('、')} aria-label={rest.map(nameOf).join('、')}
                onClick={() => setExpanded(true)}>+{rest.length}</button>
      )}
    </div>
  )
}

/** 分组选项结构（供列表渲染与过滤） */
interface Opt { group: string; code: string; flag: string; label: string }

// 默认导出组件：目标语言多选（分组勾选列表 + 搜索过滤 + 自定义语言输入）
export default function LangMultiSelect({ value, onChange, kbLangs, compact }: Props) {
  const [open, setOpen] = useState(false)
  const [custom, setCustom] = useState('')
  const [query, setQuery] = useState('')
  const [extraCodes, setExtraCodes] = useState<string[]>([])
  const lang = useLang() // ★ #23：语言名按界面语言取中/英
  // 从后端加载 KB 支持语言（新语言升级进 KB 区，名称/国旗覆盖本地兜底）
  const [apiKb, setApiKb] = useState<Array<{ code: string; label: string; flag?: string }>>([])
  const rootRef = useRef<HTMLDivElement>(null)

  // 挂载时从后端拉取 KB 语言列表，覆盖/补充本地兜底选项
  // ★ §4.2-2 正当豁免（公开字典端点）：/api/translation/langs 在路由鉴权白名单内
  //   （见 backend-go internal/api/route_auth_gate_test.go：「落地页/翻译页下拉数据源，公开」），
  //   不带 Authorization 也不会 401，因此不经 core.request()：套上去等于把登录凭证发给一个匿名接口。
  //   失败口径同样是刻意的——拉不到就保留本文件顶部的 KB_LANGS 内置兜底语言表，
  //   选择器照常可用，不打断用户当前的勾选动作，故不弹提示。
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

  // 点组件外部收起下拉
  useEffect(() => {
    if (!open) return
    const onDoc = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDoc)
    return () => document.removeEventListener('mousedown', onDoc)
  }, [open])

  // 构造分组选项：KB 分组 + 其他语言分组 + 自定义语言分组（如有）
  // ★ #23：label 一律经 langLabel 本地化（zh*→中文名；其余界面语言→英文名），
  //   KB 区含后端追加的 zh（简体中文，外语→中文方向），未知代码原样显示
  const options = useMemo<Opt[]>(() => {
    // ★ #23：默认 KB 名单跟随后端返回（/langs 现含 zh 纯模型条目——外语→简体中文方向）；
    //   后端未就绪时回落本地九语。显式传入的 kbLangs 仍最优先。
    const kbSet = kbLangs?.length ? kbLangs
      : (apiKb.length ? apiKb.map((x) => x.code) : KB_LANGS.map((x) => x.code))
    // 合并顺序：先本地兜底、再用后端条目覆盖同名键——后端只回 code+name 时不会把本地 flag 冲没，
    // 反之新升级进 KB 的语言也能立刻出现
    const kbMap = new Map<string, { code: string; label: string; flag?: string }>()
    for (const x of [...KB_LANGS, ...apiKb]) kbMap.set(x.code, { ...(kbMap.get(x.code) || {}), ...x })
    const disp = (code: string, fallback: string) => {
      const l = langLabel(code, lang)
      return l === code ? fallback || code : l // 表内无此码（自定义语言）→ 用兜底名
    }
    const out: Opt[] = []
    for (const x of [...kbMap.values()].filter((x) => kbSet.includes(x.code)))
      out.push({ group: t('chat.kbGroup'), code: x.code, flag: x.flag || '', label: disp(x.code, x.label) })
    // ★ #23 修复（e2e T3 红灯）：后端 /langs 扩容后 kbSet 覆盖 ja/ko/th 等「其他常用语」，
    //   本地 OTHER_LANGS 若原样再推一遍会在面板出现重复 option（勾选态计数翻车）。
    //   规则：已进 KB 组的代码从其他语言组剔除，其余保持非 KB 展示位。
    for (const x of OTHER_LANGS) {
      if (kbSet.includes(x.code)) continue
      out.push({ group: t('chat.otherGroup'), code: x.code, flag: x.flag || '', label: disp(x.code, x.label) })
    }
    for (const c of extraCodes)
      out.push({ group: t('chat.customGroup'), code: c, flag: '', label: langLabel(c, lang) })
    return out
  }, [kbLangs, extraCodes, apiKb, lang])

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
  // ★ 〇-M：`compact`（输入区内的一行工具条）时面板改为**向上弹**——触发器已贴在屏幕底部，
  //   向下弹会顶出视口，而宿主 `.cw-dialog` 是 `overflow:hidden`，越界部分直接被裁掉打不开。
  const panel = (
    <div className={'lms-panel' + (compact ? ' lms-panel--up' : '')} style={{ width: 320 }} data-testid="lang-multi-panel">
      <div style={{ padding: 8, borderBottom: '1px solid var(--lc-border-faint)' }}>
        <input
          className="lc-input lms-search" autoFocus value={query}
               placeholder={t('chat.langPlaceholder')}
               aria-label={t('chat.langPlaceholder')}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter') onEnterKey() }}
        />
      </div>
      <div style={{ maxHeight: 280, overflowY: 'auto', padding: '4px 0' }}>
        {grouped.map(([g, opts]) => (
          <div key={g}>
            <div style={{ fontSize: 14, color: 'var(--lc-text-3)', padding: '4px 12px' }}>{g}</div>
            {opts.map((o) => {
              const on = value.includes(o.code)
              return (
                // 自绘浮层脱离了组件库的 listbox 语义，role/aria-selected 需自己补：
                // 读屏才能播报「已选/未选」，data-lang 供 e2e 按语种定位断言
                <div key={o.code} role="option" aria-selected={on} data-lang={o.code}
                     onClick={() => toggle(o.code)}
                     style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '6px 12px', cursor: 'pointer',
                              background: on ? 'rgba(231,233,234,0.10)' : undefined, fontSize: 15 }}>
                  <span style={{ width: 18 }}>{o.flag}</span>
                  <span style={{ flex: 1 }}>{o.label}</span>
                  {on && <span style={{ color: 'var(--lc-text)', fontWeight: 700 }}></span>}
                </div>
              )
            })}
          </div>
        ))}
        {!shown.length && <div style={{ padding: '10px 12px', fontSize: 14, color: 'var(--lc-text-3)' }}>{t('chat.noLangHit')}</div>}
      </div>
      <div style={{ padding: '8px 12px', borderTop: '1px solid var(--lc-border-faint)', display: 'flex', gap: 6, alignItems: 'center' }}
           onClick={(e) => e.stopPropagation()}>
        <input
          className="lc-input lms-search" style={{ flex: 1 }} value={custom}
               placeholder={t('chat.customLangPlaceholder')}
          onChange={(e) => setCustom(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter') addCustom() }}
        />
        <button type="button" className="lc-btn lc-btn--secondary lc-btn--sm" disabled={!custom.trim()} onClick={addCustom}>+</button>
      </div>
    </div>
  )

  return (
    <div ref={rootRef} style={{ position: 'relative' }}>
      {/* 触发器：单行占位按钮，永不渲染已选标签（选中结果只在外部 LangChips 展示） */}
      <button type="button" data-testid="lang-multi-trigger"
              className={'lms-trigger' + (compact ? ' lms-trigger--pill' : '')}
              onClick={() => setOpen((v) => !v)}
              style={{ color: value.length ? 'var(--lc-text)' : 'var(--lc-text-3)' }}>
        {/* 胶囊档只占一行工具条里的一小格，文案取短档「目标语言」（长档「选择目标语言」是整宽
            触发框用的，塞进 28px 高的胶囊会把 ▾ 挤掉） */}
        <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {compact ? t('chat.targetLangLabel') : t('chat.langPlaceholder')}
        </span>
        <span aria-hidden style={{ fontSize: 12 }}>▾</span>
      </button>
      {open && panel}
      <style>{CSS_LMS}</style>
    </div>
  )
}

// 页面级样式：lms- 前缀（防与组件库/其他页面类名重名）
const CSS_LMS = `
.lms-trigger{width:100%;height:var(--lc-ctl-h);padding:0 12px;text-align:start;display:flex;align-items:center;justify-content:space-between;gap:6px;background:var(--lc-inset);border:1.2px solid var(--lc-border-input);border-radius:var(--lc-r-ctl);font-size:15px;font-family:var(--lc-font);cursor:pointer;transition:border-color var(--lc-mo-release) var(--lc-mo-out)}
.lms-trigger:hover{border-color:var(--lc-border-pill)}
.lms-panel{position:absolute;top:calc(100% + 6px);inset-inline-start:0;z-index:40;background:var(--lc-inset);border:1.2px solid var(--lc-border-card);border-radius:10px;box-shadow:0 12px 32px rgba(0,0,0,.5),var(--lc-panel-highlight)}
.lms-search{height:30px;font-size:15px}
.lms-chip-close{display:inline-flex;align-items:center;justify-content:center;width:16px;height:16px;padding:0;border:0;border-radius:4px;background:transparent;color:var(--lc-text-3);cursor:pointer}
.lms-chip-close:hover{color:var(--lc-text)}
/* ★ 〇-M 胶囊档触发器：一行工具条内用（高 28 与相邻分段控件/主按钮同档，宽度随内容不撑满） */
.lms-trigger--pill{width:auto;max-width:220px;height:28px;padding:0 10px;border-radius:999px;background:transparent;border:1.2px solid var(--lc-border-pill);font-size:14px}
.lms-trigger--pill:hover{border-color:var(--lc-border-strong)}
/* 贴底触发时面板向上长（宿主卡片 overflow:hidden，向下弹会被裁掉） */
.lms-panel--up{top:auto;bottom:calc(100% + 6px)}
/* 「+n」折叠位：外观与 chip 同档但可点，点下去就地展开全部已选语种 */
.lms-chips-more{height:24px;padding:0 8px;border:1.2px solid var(--lc-border-pill);border-radius:999px;background:transparent;
  color:var(--lc-text-2);font-size:14px;font-family:var(--lc-font);cursor:pointer;line-height:1}
.lms-chips-more:hover{color:var(--lc-text);border-color:var(--lc-border-strong)}
.lms-chips-more:focus-visible{outline:2px solid var(--lc-border-strong);outline-offset:2px}
`
