// ============================================================================
// components/LangSelect.tsx — 界面语言下拉切换器（★ #23，2026-09-19）
// 取代旧的 zh/en 二元 toggleLang 按钮：顶栏 / 登录卡 / 管理后台三处共用。
// 选项永远用各语言自称（native），当前语种打勾；选完即 setLang 持久化。
// ★ 用户拍板⑦（2026-10）：触发钮改显固定词（app.langBtn），自称名降级到
//   title 悬停 + 菜单选中项两条腿——旧 app.langSwitch 是 zh/en 二元时代的键，禁止再当 aria-label。
// ★ 用户后令（2026-10-13，覆盖⑦的「随界面语言本地化」这一半）：**触发钮可见文字不分前端语言，
//   统一显示固定英文词**。原形态在中文界面出「语言」、法语界面出「Langue」，
//   用户判定是「外国人看不懂那一串本地字、中国人又用不上这扇入口」——
//   可见文字是品宣与可识别性的一部分，固定成一个全球都认的形状。
//   本地化没有丢：`aria-label` 仍取该语种词典的 app.langBtn（读屏按用户母语念），
//   当前语种信息仍在 title 与菜单选中项两条腿里。**禁止**把可见文字再改回 t('app.langBtn')
//   或自称名（两条锁各拦一种回潮）。
// ★ 用户再令（2026-10-17）：那个固定词由 `Language` 收短为 **`Lang`**（配地球图标），
//   理由是移动档顶栏要把它和登录钮一起收进右上角，长词会把顶栏挤窄。词面仍**不分语种**、仍固定一处。
// 词表与语种代码唯一来源是 @/i18n 的 LANG_OPTIONS——新语种进表即进菜单。
// ============================================================================
import { useEffect, useRef, useState } from 'react'
import { LANG_OPTIONS, setLang, useT } from '@/i18n'
import { GlobeIcon } from '@/ui/langcross/src'

/** 触发钮可见文字：全站固定英文，不随界面语言本地化（见文件头 2026-10-13 后令 ＋ 2026-10-17 收短为 `Lang`） */
export const LANG_BTN_FIXED_TEXT = 'Lang'

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
        {/* ★ 2026-10-13 用户后令 ＋ 2026-10-17 收短：可见文字**固定**英文 `Lang`（配地球图标），
            不随界面语言本地化（不再显示当前语种自称、也不再取 t('app.langBtn') 的本地字）；
            当前语种信息保留在 title 悬停与菜单选中项（✓/is-cur）两条腿里 */}
        <span className="lang-sel-cur">{LANG_BTN_FIXED_TEXT}</span>
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
