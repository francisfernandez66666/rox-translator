// ============================================================================
// components/LangSelect.tsx — 界面语言下拉切换器（★ #23，2026-09-19）
// 取代旧的 zh/en 二元 toggleLang 按钮：顶栏 / 登录卡 / 管理后台三处共用。
// 选项永远用各语言自称（native），当前语种打勾；选完即 setLang 持久化。
// ★ 用户拍板⑦（2026-10）：触发钮改显固定词「语言/Language…」（app.langBtn），自称名降级到
//   title 悬停 + 菜单选中项两条腿——旧 app.langSwitch 是 zh/en 二元时代的键，禁止再当 aria-label。
// 词表与语种代码唯一来源是 @/i18n 的 LANG_OPTIONS——新语种进表即进菜单。
// ============================================================================
import { useEffect, useRef, useState } from 'react'
import { LANG_OPTIONS, setLang, useT } from '@/i18n'
import { GlobeIcon } from '@/ui/langcross/src'

/** props.align='right'：菜单向触发钮左侧收拢，顶栏右端用，防窄屏溢出视口 */
export function LangSelect({ align = 'right' }: { align?: 'left' | 'right' }) {
  const [lang, t] = useT()
  const [open, setOpen] = useState(false)
  const wrapRef = useRef<HTMLDivElement>(null)
  const current = LANG_OPTIONS.find((o) => o.code === lang) ?? LANG_OPTIONS[0]

  // 外点/Esc 收起：菜单是小部件不做焦点陷阱，但点哪儿都能关是底线体验
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (!wrapRef.current?.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false) }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  return (
    <div className="lang-sel" ref={wrapRef}>
      <button
        type="button"
        className={`lang-sel-btn${open ? ' is-open' : ''}`}
        aria-label={t('app.langBtn')}
        aria-haspopup="listbox"
        aria-expanded={open}
        title={current.native} /* ★ 用户拍板⑦：自称名降级到 title 悬停腿，不占按钮宽度 */
        onClick={() => setOpen((v) => !v)}
      >
        <GlobeIcon size={14} />
        {/* ★ 用户拍板⑦（2026-10）：触发钮固定显示「语言/Language…」固定词（app.langBtn，随界面语言本地化），
            不再显示当前语种自称——选错语种后外国人读不懂自称名会被钉死；
            当前语种信息保留在 title 悬停与菜单选中项（✓/is-cur）两条腿里 */}
        <span className="lang-sel-cur">{t('app.langBtn')}</span>
      </button>
      {open && (
        <div className={`lang-sel-menu${align === 'right' ? ' lang-sel-menu--right' : ''}`} role="listbox" aria-label={t('app.langBtn')}>
          {LANG_OPTIONS.map((o) => (
            <button
              key={o.code}
              type="button"
              role="option"
              aria-selected={o.code === lang}
              className={`lang-sel-item${o.code === lang ? ' is-cur' : ''}`}
              onClick={() => { setLang(o.code); setOpen(false) }}
            >
              <span className="lang-sel-native">{o.native}</span>
              {o.code === lang && <span className="lang-sel-check" aria-hidden="true">✓</span>}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
