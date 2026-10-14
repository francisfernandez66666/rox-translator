// ============================================================================
// components/Bell.tsx — 站内通知铃铛（langcross Badge + CSS 绝对定位浮层）
// 行为对齐：30s 未读轮询、下拉列表、单条已读/全部已读、feedback 类跳转后台反馈面板。
// 呈现层替换：TDesign Badge→Badge、Popup→页面级 CSS 浮层、Empty→EmptyState。
// ============================================================================
import { useCallback, useEffect, useRef, useState } from 'react'
import { Badge, BellIcon, EmptyState } from '@/ui/langcross/src'
import { notificationsUnread, notifications, notificationRead, notificationsReadAll } from '@/api'
import { fmtTime } from '@/lib/ui'
import { noticeLandingFor } from '@/lib/noticeRouting'
import { t } from '@/i18n'
import { useAdmin } from '@/stores/admin'

// ============ 本文件职责中文说明 ============
// 站内通知铃铛：未读轮询、下拉列表与已读操作。
// 挂载点有两处：前台顶栏（App.tsx 的 FrontShell，懒加载）与后台顶栏（admin/AdminDashboard，静态 import），
// 所以浮层不依赖任何外壳 CSS，样式与定位全在本文件自带。
// ========================================

// 站内通知条目数据结构（字段口径同后端 /api/notifications 列表项，不做前端改名）
interface NoticeItem {
  id: number
  title: string
  body: string
  ref_type?: string // 关联对象类型：feedback / ticket / quota / kb_scrape，决定点击跳去哪
  ref_id?: number // 关联对象 ID，含义随 ref_type 变（可为 0/缺省）
  created_at: string
}

// 浮层一页的条数：与后端默认档（store.NotificationPageDefault＝20）同值，也是浮层高度放得下的上限。
// ★ 2026-10-11 跨境链路瓶颈批：这个数以前只在**渲染侧**起作用（后端吐 100 条、这里 slice(0,20)），
// 传输照旧付整包；现在它同时是**请求参数**，链路上真的只走一页。
const BELL_PAGE = 20

// 默认导出组件：通知铃铛，负责未读轮询、下拉列表与已读操作（等价 Vue Bell.vue）
export default function Bell() {
  const [unread, setUnread] = useState(0)
  const [items, setItems] = useState<NoticeItem[]>([])
  const [open, setOpen] = useState(false)
  // 分页状态：hasMore 决定「更多」按钮在不在，loadingMore 只用来把按钮按成一发即锁（防连点打两页）
  const [hasMore, setHasMore] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  // ★ F-11 顺手项（批G 2026-09-25）：open 的同步镜像——refresh 经它现读展开态，
  // 于是 refresh 引用不再随 open 翻转而更换（旧版靠 open 进 deps 重建 refresh，
  // 才连带产生「toggle 里调的是旧闭包」的 stale-refresh，见 toggle 注释）
  const openRef = useRef(false)
  const { gotoPanel, openFeedback, myLevel } = useAdmin() // 后台跳转与等级取自 admin store，铃铛自己不管路由（落点见 lib/noticeRouting.ts）
  const timerRef = useRef<number | null>(null) // 30s 轮询句柄：用 ref 而非 state，改它不该触发重渲染
  const wrapRef = useRef<HTMLDivElement>(null) // 「点击外部」判定基准：铃铛钮 + 浮层都在这层里

  // ★ F-11 顺手项（批G 2026-09-25）：拉取最近 20 条通知列表——从 refresh 里抽出来的独立动作。
  // 抽出来的意义：toggle 展开时可以「直接」await 它，不必再绕 refresh 那层对 open 的闭包依赖
  //（见下方 toggle 的 stale-refresh 说明）。deps 为空 ⇒ 引用终生稳定，不连带重启 30s 轮询。
  const loadList = useCallback(async () => {
    try {
      const r = await notifications(BELL_PAGE, 0)
      // ★ 2026-10-11 分页批：旧形态在这里 `slice(0, 20)` 截渲染——那是"后端吐 100、前端只看 20"的补丁，
      // 传输整包照付。现在请求本身就只取一页，`slice` 失去存在理由，一并撤掉（留着会让人以为后端还在整档吐）。
      if (r.success) {
        const box = r as unknown as { notifications?: NoticeItem[]; has_more?: boolean }
        setItems(box.notifications || [])
        setHasMore(box.has_more === true)
      }
    } catch { /* 忽略 */ } // 同 refresh：拉不到就保持上一次的列表，不打断操作
  }, [])

  // ★ 2026-10-11 分页批新增：追加下一页。offset 取"当前已到手条数"——后端列表按 id DESC 稳定排序，
  // 顺序追加不会重复或漏条；中途若有新通知落库，最坏是这一页边界挪一位，下一次展开或 30s 轮询重拉第一页即修正
  //（铃铛是旁路信息，不为这一格新鲜度多打一次 COUNT 查询——那正是本批要省的那类往返）。
  const loadMore = useCallback(async () => {
    setLoadingMore(true)
    try {
      const r = await notifications(BELL_PAGE, items.length)
      if (r.success) {
        const box = r as unknown as { notifications?: NoticeItem[]; has_more?: boolean }
        const more = box.notifications || []
        // 用函数式更新而不是 ` [...items, ...more]`：本回调的 items 是**这一页请求发出前**的快照，
        // 期间若有别的写回（如 markRead 触发的 refresh）已经改过列表，直接拼会把那份改动抹掉
        setItems((prev) => [...prev, ...more])
        // 空页＝到底了：后端 has_more 用「本页取满」近似判断，取到 0 条时必须收口，否则按钮永远在
        setHasMore(more.length > 0 && box.has_more === true)
      }
    } catch { /* 忽略 */ } // 拉不到就维持"还能翻"这一乐观档：下一次点击重试，不收按钮
    finally { setLoadingMore(false) }
  }, [items.length])

  // 刷新未读数；下拉展开时（openRef 现读）同步拉取最近 20 条通知列表
  // 列表只在展开时才请求：收起状态下 30s 轮询只多打一个轻量的 unread 接口，省掉一次列表查询。
  // ★ 展开态经 openRef 读取而非闭包 open ⇒ deps 稳定 ⇒ 定时器不再随收起/展开重启（旧版副作用一并消除）。
  const refresh = useCallback(async () => {
    try {
      const u = await notificationsUnread()
      // `|| 0` 把缺省字段与 0 统一成数字 0，避免把 undefined 塞进 state 后徽标处比不出大小
      if (u.success) setUnread(Number((u as unknown as { unread?: number }).unread || 0))
      if (openRef.current) await loadList()
    } catch { /* 忽略 */ } // 顶栏铃铛是旁路信息：拉不到就保持上一次的读数，不 toast 不打断操作
  }, [loadList])

  // 挂载即刷新并启动 30s 未读轮询；卸载时清除定时器
  // deps 挂 refresh（引用稳定，仅随 loadList）：展开/收起不再重绑定时器，30s 节拍跨开合连续；
  // 展开瞬间的列表拉取由 toggle 直接调 loadList 负责（见下），不靠这里兜底。
  // 定时器本身不 await 上一轮：慢响应按到达先后互相覆盖（后写赢），未读数这种幂等值无所谓。
  useEffect(() => {
    void refresh() // void 显式丢弃 promise：这里不关心结果，只触发
    timerRef.current = window.setInterval(refresh, 30000)
    // 与工单列表轮询不同，这里没有 document.hidden 判断：收起时也只打一个轻量 unread 接口
    return () => { if (timerRef.current) window.clearInterval(timerRef.current) }
  }, [refresh])

  // 点击浮层外部关闭（等价 TDesign Popup 的 trigger="click" 点击外部收起）
  // 监听随 open 挂/卸，收起态不在 document 上留全局 mousedown；
  // 铃铛钮与浮层都在 wrapRef 子树内，「点内部」由 contains 一次判掉，无需各元素 stopPropagation。
  // 用 mousedown 而不是 click：按下那一瞬就收起，浮层不会在该次点击落到别处时还杵着。
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  // 标记单条已读并刷新列表
  // 未套 runGuarded：请求失败只留一个未处理 rejection、不弹提示，未读数靠下一次 30s 轮询自愈
  // （这里也不弹：onItemClick 不 await markRead，toast 会漂到跳转后的新页面上）
  async function markRead(id: number) {
    await notificationRead(id)
    void refresh()
  }
  // 标记全部已读并刷新列表（不本地 setUnread(0)：以服务端回读为准，避免并发新通知被抹掉读数）
  async function markAll() {
    await notificationsReadAll()
    void refresh()
  }

  // 点击通知：先标记已读，再按 ref_type 落点表跳转（★ #46，2026-10-14：落点从 Bell 里那一条
  // 硬编码分支收成 lib/noticeRouting.ts 的唯一表，与后端 store.NotifyRefTypes 跨语言等值）
  function onItemClick(n: NoticeItem) {
    void markRead(n.id) // 不 await：跳转要立刻发生，已读失败不该把用户卡在铃铛里
    // ref_id 的含义随 ref_type 变（feedback=反馈单 ID、ticket=工单 ID、quota=组织/租户 ID、kb_scrape=0），
    // 只有 feedback 落点会真的读它，所以强转写在那一分支里。
    // 注：gotoPanel / openFeedback 都来自 admin store，落点是后台 /admin 的面板
    //     （store 内 set panel + navigate('/admin')），不是前台 /tickets；跳转不带 ref_id，
    //     所以除 feedback 外的落点只做到「把你送到那个面板」，不定位到具体那条记录。
    const landing = noticeLandingFor(n.ref_type, myLevel)
    if (landing.kind === 'feedback') openFeedback(n.ref_id as number)
    else gotoPanel(landing.panel)
    setOpen(false) // 收起浮层：已在后台时点通知不会触发路由跳转，浮层得自己收，否则一直盖在内容上
  }

  // 切换浮层；展开时刷新列表
  // ★ F-11 顺手项（批G 2026-09-25）：消掉旧版 stale-refresh——旧实现这里也调 refresh()，
  // 但闭包里的 open 还是翻转前的 false，那一次 refresh 只刷未读数**不拉列表**；
  // 列表实际全靠 open 翻转 → refresh 换身份 → useEffect 重跑那次兜底才到手（异步双跳，
  // 慢网下浮层先闪 EmptyState）。现在展开同步置 openRef 并直接 loadList()，
  // 点开的同时在途的就是列表；随后 30s 轮询因 openRef 已翻，也会带着刷列表。
  function toggle() {
    const next = !open
    openRef.current = next // 同步翻转镜像：本轮事件循环内 refresh/轮询读到的就是新态
    setOpen(next)
    if (next) void loadList()
  }

  return (
    <div className="bell-wrap" ref={wrapRef}>
      {/* 样式随组件注入（与 TicketsPage 的 tk- 同口径：bell- 前缀只此一处在用）。
          字阶按 UI 真值 T 系（标题 13 / 正文 12 / 时间 11）；历史上 theme.css §十一 曾用
          `html .bell-trigger{padding:9px}` 提权覆写并放大本浮层字阶（#67/#68），
          2026-09-22 全站还原时该覆写层已删除，本文件即唯一来源。 */}
      <style>{BELL_CSS}</style>
      {/* aria-expanded 让读屏报出「已展开/已折叠」；未读变化不做 aria-live 播报，
          顶栏每 30s 刷一次，实时播报会持续打断用户 */}
      <button type="button" className="bell-trigger" onClick={toggle} aria-label={t('bell.title')} aria-expanded={open}>
        <BellIcon size={18} />
        {/* 未读为 0 时整个徽标不渲染（而非显示 0），铃铛回到纯图标 */}
        {unread > 0 && <Badge className="bell-count">{unread}</Badge>}
      </button>
      {/* 浮层自绘：绝对定位挂在 .bell-wrap(position:relative) 之下，替代原 Popup 的 popper 定位。
          ⚠ 这里只标了 role="menu"，条目仍是可点 div、没有 menuitem/方向键导航，键盘用户只能 Tab 到关闭 */}
      {open && (
        <div className="bell-panel" role="menu">
          <div className="bell-head">
            <b style={{ fontSize: 15 }}>{t('bell.title')}</b>
            <button type="button" className="bell-readall" onClick={markAll}>{t('bell.readAll')}</button>
          </div>
          {/* 空态与列表并存：无通知时只出 EmptyState（items 为空，map 自然产不出节点） */}
          {items.length === 0 && <EmptyState title={t('bell.empty')} />}
          {items.map((n) => (
            <div key={n.id} className="bell-item" onClick={() => onItemClick(n)}>
              <div style={{ fontWeight: 600, fontSize: 15 }}>{n.title}</div>
              {/* pre-wrap 保住后端正文里的换行（工单/反馈摘要常带 \n），否则整段塌成一行 */}
              <div style={{ fontSize: 14, color: 'var(--lc-text-3)', marginTop: 2, whiteSpace: 'pre-wrap' }}>{n.body}</div>
              <div style={{ fontSize: 13, color: 'var(--lc-text-4)', marginTop: 2 }}>{fmtTime(n.created_at)}</div>
            </div>
          ))}
          {/* ★ 2026-10-11 分页批：仍有下一页才出这颗「更多」。文案复用既有键 app.more（更多／More），
              不为一个词新开键——新键必须 12 份全量词典同步（AGENTS §一·5 的 i18n 口径），代价与收益不成比。 */}
          {hasMore && (
            <button type="button" className="bell-more" disabled={loadingMore} onClick={() => void loadMore()}>
              {t('app.more')}
            </button>
          )}
        </div>
      )}
    </div>
  )
}

// —— 浮层（bell- 前缀，避免与组件库类名重名）——
const BELL_CSS = `
.bell-wrap{position:relative;display:inline-flex}
.bell-trigger{position:relative;display:inline-flex;align-items:center;justify-content:center;padding:6px;
  background:transparent;border:0;color:var(--lc-text-2);cursor:pointer;font-family:var(--lc-font);border-radius:var(--lc-r-bar)}
.bell-trigger:hover{color:var(--lc-text)}
/* 焦点环用 :focus-visible 而非 :focus：鼠标点击铃铛不画环，键盘 Tab 过来才出（顶栏不需要每次都糊一圈） */
.bell-trigger:focus-visible{outline: 2px solid var(--lc-border-input);outline-offset:2px}
/* 未读数徽标挂在铃铛右上角（★ 2026-09-22 还原：#67 放大顶栏时曾外移到 -7/-9
   避免压住 18px 铃铛，现随字阶回档回到贴角 -1/-1）。 */
.bell-count{position:absolute;top:-1px;inset-inline-end:-1px}
/* 浮层 z-index:60 只在顶栏这个层叠上下文（.app-header 为 sticky + z-index:20，见 theme.css）
   内部比大小——够盖住下方页面内容，但对外盖不过页面级模态遮罩（如工单页 .tk-overlay 的 1200）。
   宽 340 + max-height 420 的固定盒：一页就是 BELL_PAGE（20）条、由后端分页给到，靠自身 overflow-y 滚动，
   不做虚拟列表；要看第 21 条以后点底部「更多」追加下一页。 */
.bell-panel{position:absolute;top:calc(100% + 8px);inset-inline-end:0;z-index:60;width:340px;max-height:420px;overflow-y:auto;
  padding:8px;background:var(--lc-panel);border:1.2px solid var(--lc-border-card);border-radius:var(--lc-r-modal);
  box-shadow:var(--lc-panel-highlight)}
.bell-head{display:flex;justify-content:space-between;align-items:center;margin-bottom:6px}
.bell-readall{background:none;border:0;color:var(--lc-text-3);font-size:14px;cursor:pointer;font-family:var(--lc-font);padding:0}
.bell-readall:hover{color:var(--lc-text);text-decoration:underline}
.bell-item{padding:8px 6px;border-bottom:1px solid var(--lc-border-faint);cursor:pointer}
.bell-item:last-child{border-bottom:0}
.bell-item:hover{background:var(--lc-raised)}
/* 「更多」按钮（★ 2026-10-11 分页批）：样式沿用 .bell-readall 那一族弱文字档，独占一行居中；
   disabled 只降透明度不改字号——按下去要看得见"这一下生效了"，不能整颗消失让人以为点空 */
.bell-more{display:block;width:100%;margin-top:6px;padding:6px 0;background:none;border:0;
  color:var(--lc-text-3);font-size:14px;cursor:pointer;font-family:var(--lc-font);border-radius:var(--lc-r-bar)}
.bell-more:hover{color:var(--lc-text);text-decoration:underline}
.bell-more:disabled{opacity:.5;cursor:default;text-decoration:none}
`
