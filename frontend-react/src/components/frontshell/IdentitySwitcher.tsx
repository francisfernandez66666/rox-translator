// ============================================================================
// components/frontshell/IdentitySwitcher.tsx — 左栏身份切换器（★ 多身份方案 B4）
// 职责：一人多身份（个人版 ＋ 若干企业成员）时，在当前身份徽标右侧露出切换入口，
//       点开是本账户全部身份的下拉；选中即调 /api/auth/switch-identity 换会话，
//       不整页刷新（方案 §十 R8 拍板）。
//
// 四条硬口径（改本件前必读）：
//   ① **单身份账户整颗不渲染**。身份清单只有一条时，这里没有可切的东西，
//      摆一颗点开只有一个选项的按钮＝噪声（方案 §十「单身份账户不渲染 icon」）。
//      判据用 identities.length，不用 user 上有没有企业字段——后者对「只有个人版」和
//      「还没取到清单」两种形态同形，会把切换器闪成时有时无。
//   ② **状态不可用的身份置灰且不可点**（tenant_status 非 active）。后端守卫照旧必判 403，
//      这里只是少一次注定失败的请求；灰档判据只读出栈字段，不在前端猜租户状态。
//   ③ **面板必须 portal 到 body ＋ position:fixed**（AGENTS §一·5 浮层那条）：
//      左栏宽仅 237（桌面）／295（移动抽屉）且 overflow-y:auto，栏内绝对定位的浮层
//      会把左栏撑出横向滚动条（2026-10-17 用户报障的同族形态）。落点现算、四边夹进视口。
//   ④ **切换动作只有一个前端落点**：stores/auth 的 switchTo()。本件不再拼第二份
//      setAuthToken ＋ setState（两处各写一遍迟早漂移成两种状态）。
//
// 切换后为什么还要重挂页面：数据是按身份（uid,tid）读的，路由组件多在挂载时拉一次。
// 只换 state 不重挂，用户会在「企业 B 的界面」上看到「企业 A 的会话/工单」。
// 重挂由外壳负责（App.tsx 的 RouteBoundary key），本件只负责把新会话落进 store。
// ============================================================================
import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Icon, useToast } from '@/ui/langcross/src'
import { useAuth } from '@/stores/auth'
import { useT } from '@/i18n'
import type { Identity } from '@/api'

/** 面板宽度上限（窄屏下按视口收，见 place()） */
const PANEL_WIDTH = 260

/**
 * 左栏身份切换器。挂在品牌行右侧（.lc-sidebar__tools 内、.fs-tenant 徽标之后）。
 */
export default function IdentitySwitcher() {
  const { user, identities, switchTo, loadIdentities } = useAuth()
  const { toast } = useToast()
  const [, t, tpl] = useT()
  const triggerRef = useRef<HTMLButtonElement>(null)
  const panelRef = useRef<HTMLDivElement>(null)
  const [open, setOpen] = useState(false)
  // 落点由现算得出（top 与 bottom 只会有一侧有值：向下开还是向上开由剩余空间决定）
  const [pos, setPos] = useState<{ left: number; top?: number; bottom?: number; width: number; maxH: number } | null>(null)
  const [busy, setBusy] = useState(false)

  // 身份清单为空时补拉一次：登录/注册两条出口都带 identities，但历史形态（老包、
  // 直接落到工作台的其他登录路径）可能没塞进来，这里让切换器自己把账补齐。
  useEffect(() => {
    if (user && identities.length === 0) void loadIdentities()
  }, [user, identities.length, loadIdentities])

  // 展开期间：点面板外任意处／Esc 关闭；窗口尺寸或任意层滚动都重锚
  // （scroll 用 capture，左栏自身 overflow-y:auto，冒泡阶段收不到它内部的滚动）。
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      const n = e.target as Node
      if (triggerRef.current?.contains(n) || panelRef.current?.contains(n)) return
      setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false) }
    const onMove = () => place()
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    window.addEventListener('resize', onMove)
    window.addEventListener('scroll', onMove, true)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
      window.removeEventListener('resize', onMove)
      window.removeEventListener('scroll', onMove, true)
    }
  }, [open, identities.length])

  // 现算落点：左边界夹进视口、优先向下开，下方放不下且上方更宽裕时翻到上方。
  // 估算高度只用于「该往哪边开」的判定，真实高度由 maxH ＋ 内滚兜住。
  function place() {
    const el = triggerRef.current
    if (!el) return
    const r = el.getBoundingClientRect()
    const width = Math.min(PANEL_WIDTH, window.innerWidth - 24)
    const left = Math.max(8, Math.min(r.left, window.innerWidth - width - 8))
    const estH = Math.min(360, 40 + identities.length * 46)
    const below = window.innerHeight - r.bottom - 16
    const above = r.top - 16
    if (below < estH && above > below) {
      setPos({ left, bottom: Math.max(8, window.innerHeight - r.top + 6), width, maxH: Math.max(160, above) })
    } else {
      setPos({ left, top: Math.max(8, r.bottom + 6), width, maxH: Math.max(160, below) })
    }
  }

  // 当前身份：以会话里的 tenant_id 比对（切换成功后 user 换了，勾自动跟着走）
  const currentTid = user ? Number((user as { tenant_id?: number }).tenant_id ?? 0) : 0

  async function pick(i: Identity) {
    if (busy || i.tenant_status !== 'active' || i.tid === currentTid) return
    setBusy(true)
    const r = await switchTo(i.tid)
    setBusy(false)
    setOpen(false)
    if (!r.ok) {
      // 后端对「目标不属于本账户／该行已停用／租户已停用」一律回同一句 403（防枚举口径），
      // 所以这里照实展示那一句话，不拆成三种猜测文案。
      toast({ title: r.message || t('auth.id.failed'), tone: 'error' })
      return
    }
    toast({ title: tpl('auth.id.done', { name: identityLabel(i, t) }), tone: 'success' })
  }

  if (!user || identities.length <= 1) return null

  return (
    <>
      <style>{ISW_CSS}</style>
      <button
        type="button"
        ref={triggerRef}
        className="idsw-trigger"
        data-testid="identity-switcher"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={t('auth.id.switch')}
        title={t('auth.id.switch')}
        onClick={() => {
          if (open) { setOpen(false); return }
          place()
          setOpen(true)
          // 打开即后台补拉：管理员在我登录期间把我移出/加进某企业，这一份清单要当场跟得上
          void loadIdentities()
        }}
      >
        <Icon n="swap" style={{ fontSize: 14, flex: 'none' }} />
      </button>

      {open && pos && createPortal(
        <div
          ref={panelRef}
          className="idsw-panel"
          role="menu"
          aria-label={t('auth.id.list')}
          style={{ left: pos.left, top: pos.top, bottom: pos.bottom, width: pos.width, maxHeight: pos.maxH }}
        >
          <div className="idsw-head">{t('auth.id.list')}</div>
          {identities.map((i) => {
            const disabled = i.tenant_status !== 'active'
            const current = i.tid === currentTid
            return (
              <button
                type="button"
                key={`${i.uid}-${i.tid}`}
                role="menuitem"
                data-testid="identity-option"
                className={['idsw-item', current ? 'idsw-item--current' : '', disabled ? 'idsw-item--disabled' : ''].filter(Boolean).join(' ')}
                aria-disabled={disabled || undefined}
                title={disabled ? t('auth.id.unavailable') : undefined}
                onClick={() => { void pick(i) }}
              >
                <Icon n={i.is_personal ? 'user' : 'building'} style={{ fontSize: 14, flex: 'none', verticalAlign: '-3px', marginInlineEnd: 8 }} />
                <span className="idsw-name">{identityLabel(i, t)}</span>
                {!!i.org_name && <span className="idsw-org">{i.org_name}</span>}
                {current && <Icon n="check" className="idsw-check" style={{ fontSize: 14 }} />}
              </button>
            )
          })}
        </div>,
        document.body,
      )}
    </>
  )
}

/** 一个身份的展示名：个人版走既有 `app.personalPlan`（与左栏徽标同词，不另立第二套叫法）；
 *  企业回租户名，租户读不到时（后端出栈 tenant_name 为空）退化成企业编号，
 *  宁可得难看的读数也不给一个空白行——空白行会被读成「这个身份没了」。 */
function identityLabel(i: Identity, t: (k: string) => string): string {
  if (i.is_personal) return t('app.personalPlan')
  return i.tenant_name || `${t('auth.id.tenant')} ${i.tid}`
}

/* 面板：portal 到 body 的固定定位（落点由 place() 现算）。
   ⚠ box-sizing:border-box 必写：自带内垫与描边，content-box 下 width 会再溢出约 18px。
   描边 1.2px＝交付档（AGENTS §一·5 〇-P），最小字阶 14px＝排版地板之上。 */
const ISW_CSS = `
.idsw-trigger{display:inline-flex;align-items:center;justify-content:center;width:26px;height:26px;flex:none;
  padding:0;border:1.2px solid var(--lc-border-card-dim);border-radius:8px;background:transparent;
  color:var(--lc-text-2);cursor:pointer}
.idsw-trigger:hover{color:var(--lc-text-1);border-color:var(--lc-border-strong)}
.idsw-panel{position:fixed;z-index:70;box-sizing:border-box;overflow-y:auto;padding:6px;
  border:1.2px solid var(--lc-border-card);border-radius:10px;background:var(--lc-raised);
  box-shadow:0 8px 24px rgba(0,0,0,.45)}
.idsw-head{padding:4px 8px 6px;font-size:12px;color:var(--lc-text-3);border-bottom:1px solid var(--lc-border-card-dim);margin-bottom:4px}
.idsw-item{display:flex;align-items:center;gap:6px;width:100%;box-sizing:border-box;padding:8px;border:0;
  border-radius:8px;background:transparent;color:var(--lc-text-1);font-size:14px;text-align:start;cursor:pointer}
.idsw-item:hover{background:var(--lc-inset)}
.idsw-item--current{font-weight:600}
.idsw-item--disabled{color:var(--lc-text-3);cursor:not-allowed}
.idsw-name{min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.idsw-org{margin-inline-start:auto;max-width:80px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;
  font-size:12px;color:var(--lc-text-3)}
.idsw-check{margin-inline-start:auto;flex:none;color:var(--lc-text-1)}
`
