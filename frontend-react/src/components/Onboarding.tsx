// ============================================================================
// components/Onboarding.tsx — 「备第一份产品」引导流（O 组；H1 改挂对话式外壳；H5 去掉横幅）
//
//   · 默认导出 `Onboarding` ＝ 引导页本体：**复用注册面那台 AI 引导外壳**（components/guidedchat/），
//     步骤与写口全部由 lib/onboardingSchema 驱动，本页不写死任何一步。
//   （登录后自动打开这件事已交给 components/OnboardingGate.tsx —— 横幅形态按用户令退役）
//
// ★ H1（2026-11-06 用户当场质疑后改形）：仓里原本只有一处"AI 引导填写"＝注册面那台
//   气泡＋打字机＋chips＋进度＋上一步（AiRegisterFlow），而我给引导建产品另起了一页朴素表单
//   ⇒ 同一个产品里两种"引导"，用户先碰到哪个就按哪个理解。这不是排版问题，是**同一动作两套语义**
//   （AGENTS §一·5「三口径打架」同族）。⇒ 硬口径：打字机／步进栈／代际**只有 guidedChatMachine 那一份**，
//   本文件只投喂"这一步说什么、收哪些字段、交哪个写口"；**禁止**在这里再造一套动效。
//
// ★ 四条口径（改这个文件前必读）：
//  ① 判据只认「确实没有」：`productList` 必须 success:true 且数组长度为 0 才提醒。
//     接口 500／超时／返回非数组都不算——把一次抖动读成"这人没产品"，会把老用户一次次推进向导
//     （AGENTS §一·5「空态不许伪装成检查过」在引导流上的同一形态）。
//  ② 横幅用 `position:fixed` 的底部条，**不进主布局流**：塞进 Routes 上方会推动整页内容，
//     后台像素锁（工具条/框脚几何档）会因为一条"只有新用户才看得见"的横幅而随机翻红。
//  ③ 不与 ProfileSetupGate 抢屏：那个闸门管"补邮箱/改密码"，它开着时闸门不出（判据在 OnboardingGate）。
//  ④ 完成判据不落库：有没有产品这件事本身就是判据，加一列"引导完成标记"只会多一个漏挂钩的写点。
//
// ★ H2（模型整理腿）三条口径：
//  ① 只出候选、**不落库**：用户点「下一步」时才由原有写口真写；
//  ② 任何失败（上游没配／超时／坏形态）⇒ 一句"这次没整理出来，自己填也一样"＋候选全空，
//     **不许**把这一枪写成挡住向导的失败态（fail-open 只朝"照常手填"）；
//  ③ 成本归**平台承担**（用户拍板），界面上因此不许出现"消耗积分"这类话。
//
// 本文件由 App.tsx 的 React.lazy 加载（引导页），Prompt 同样走懒加载分片。
// ============================================================================
import { useCallback, useEffect, useRef, useState } from 'react'
import { useT, getLang, tpl } from '@/i18n'
import { useNav } from '@/lib/nav'
import { langLabel } from '@/lib/langNames'
import { productSave, seoSet, onboardDraft } from '@/api/product'
import {
  SCENE_STEPS, LANG_OPTIONS, WRITE_TARGETS, parseOnboardScene, stepIsCompletable,
  type OnboardField, type OnboardStep,
} from '@/lib/onboardingSchema'
import { GuidedChatFrame } from './guidedchat/GuidedChatFrame'
import { useGuidedChat, type GuidedMsgBase } from './guidedchat/guidedChatMachine'

/** 本场景的消息：外壳只认 id/side/text/typing，其余字段由这里自己投喂 */
interface OnboardMsg extends GuidedMsgBase {
  /** 这条气泡下面挂哪一块：输入块 / 收尾摘要 */
  block?: 'fields' | 'summary'
  /** block='fields' 时对应的步骤（含字段与写口名） */
  step?: OnboardStep
  /** 收尾摘要：这一份的名字、备好的语种、以及"去哪继续改" */
  summary?: { name: string; langs: string }
}

/** 引导页入参：intro＝闸门自动打开时先说的那一句；onDismiss＝关掉时该做什么（路由进来的人没有 onDismiss，× 就回工作台） */
interface Props { intro?: string; onDismiss?: () => void }

/** 引导页：按场景取步骤，逐步写口，走完即落到产品详情页 */
export default function Onboarding({ intro, onDismiss }: Props) {
  const [, t] = useT()
  const nav = useNav()
  const scene = parseOnboardScene(new URLSearchParams(nav.search || '').get('scene'))
  const steps = SCENE_STEPS[scene]
  const total = steps.length
  // 默认值：SEO 收录出厂开（一期裁定 3／§5.8：默认开 ≠ 默认告知，最后一屏必须明说一次）
  const [values, setValues] = useState<Record<string, unknown>>({ indexable: true, target_langs: [] })
  const [productId, setProductId] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [draftText, setDraftText] = useState('')
  const [draftBusy, setDraftBusy] = useState(false)
  const [draftNote, setDraftNote] = useState('')
  // 当前第几步：步进栈由外壳持有，这里只需要一个"现在该收哪一步的字段"的读数
  const idxRef = useRef(1)

  /** 语种显示名：取词只走 lib/langNames 那一份实现；它回的就是码本身时改用语种本名（与 LangMultiSelect 同一条兜底） */
  const nameOf = (code: string) => (langLabel(code, getLang()) === code ? code : langLabel(code, getLang()))

  const flow = useGuidedChat<OnboardMsg>((idx, tot, name) => (
    tot ? tpl('auth.aiStepOf', { idx, total: tot, name }) : tpl('auth.aiStepOnly', { idx, name })
  ))
  const {
    msgs, addMsg, patchMsg, typeText, stepRender, goBack, reduced, stepLabel, trackPct,
    backHidden, setFinished, genRef, chatRef, resetFlow, scrollBottom,
  } = flow

  /** 执行一步的写口：口名全部来自 WRITE_TARGETS 那张真值表，配置里写了不存在的口就当场出声（不静默） */
  const commit = useCallback(async (s: OnboardStep, v: Record<string, unknown>) => {
    const target = WRITE_TARGETS[s.write]
    if (s.write === 'skip' || target === '') return true
    if (s.write === 'product.profile') {
      const r = await productSave({ name: String(v.name || ''), tagline: String(v.tagline || ''), use_case: String(v.use_case || '') }).catch(() => null)
      if (!r || !r.success) { setError((r && (r as { message?: string }).message) || t('onboard.saveFailed')); return false }
      const id = String((r as { id?: unknown }).id || '')
      if (id) setProductId(id)
      return true
    }
    if (s.write === 'product.langs') {
      // 没勾就是**空数组**（＝显式"暂未设定"），这与"省略＝不表态"是两种话，界面要能分开显示
      const langs = Array.isArray(v.target_langs) ? (v.target_langs as string[]) : []
      if (!productId) return true // 还没建档就跳到这步（不该发生，但也不许凭空写库）
      // ★ 带 id 的 save 是"把这份档案存回去"，**不是 PATCH**（AGENTS §30）：只交 `{id, target_langs}`
      //   会被后端判成"名字空了"（400，向导卡死在第 3 步）；而就算后端放过空串，第 2 步收到的
      //   tagline/use_case 也会被这一枪静默洗成空。⇒ 每一次写都交完整表单。
      const r = await productSave({
        id: productId,
        name: String(v.name || ''),
        tagline: String(v.tagline || ''),
        use_case: String(v.use_case || ''),
        target_langs: langs,
      }).catch(() => null)
      if (!r || !r.success) { setError((r && (r as { message?: string }).message) || t('onboard.saveFailed')); return false }
      return true
    }
    if (s.write === 'product.seo') {
      if (!productId) return true
      const r = await seoSet(productId, v.indexable !== false).catch(() => null)
      if (!r || !r.success) { setError((r && (r as { message?: string }).message) || t('onboard.saveFailed')); return false }
      return true
    }
    setError(t('onboard.unknownTarget'))
    return false
  }, [productId, t])

  /** 当前这一步那条气泡的 id：答完要按它把输入块收起来 */
  const curMsgRef = useRef(0)

  /** 渲染某一步：一条气泡说这一步的事，下面挂这一步的输入块 */
  const showStep = useCallback((i: number, my: number) => {
    const s = steps[i - 1]
    if (!s) return
    idxRef.current = i
    setError('')
    const body = t(s.descKey)
    const id = addMsg({ side: 'ai', block: 'fields', step: s, full: body, text: reduced ? body : '' })
    curMsgRef.current = id
    void typeText(id, body, my)
  }, [steps, addMsg, typeText, reduced, t])

  /** 这一步的答案（回显成一条用户气泡，并把上一条的输入块收起）
   *  ★ 外壳的消息流是**保留历史**的（聊天本来就该看得见前面说过什么），
   *    所以"已答的那一步还在 DOM 里留一份可点的表单"必须由这里收掉——
   *    否则同一屏会长出 N 份「下一步」，测试与用户都点不准（H1 首跑真踩）。 */
  const echoAnswer = useCallback((s: OnboardStep) => {
    if (curMsgRef.current) patchMsg(curMsgRef.current, { block: undefined, step: undefined })
    let ans = ''
    if (s.write === 'skip') ans = t('onboard.answer.confirmed')
    else if (s.write === 'product.profile' || s.write === 'product.langs') ans = String(values.name || '')
    else if (s.write === 'product.seo') ans = values.indexable !== false ? t('onboard.answer.on') : t('onboard.answer.off')
    if (!ans) return
    const id = addMsg({ side: 'user', full: ans, text: ans })
    void typeText(id, ans, genRef.current)
  }, [patchMsg, addMsg, typeText, genRef, values, t])

  /** 收尾：把这一份的名字与备好的语种读回给用户（链接由详情页给，不在这里凭空造） */
  const finish = useCallback((my: number) => {
    const name = String(values.name || '')
    const list = Array.isArray(values.target_langs) ? (values.target_langs as string[]) : []
    const langs = list.length ? list.map(nameOf).join('、') : t('onboard.langsUnset')
    const body = t('onboard.ai.done')
    const id = addMsg({ side: 'ai', block: 'summary', full: body, text: reduced ? body : '', summary: { name, langs } })
    void typeText(id, body, my).then(() => setFinished(true))
  }, [values, addMsg, typeText, reduced, t, setFinished])

  /** 下一步：先落库再前进（写失败必须留在本步并出声，不许把"没存上"读成"引导已走完"） */
  const next = useCallback(async () => {
    const s = steps[idxRef.current - 1]
    if (!s || !stepIsCompletable(s, values)) return
    setBusy(true); setError('')
    const ok = await commit(s, values)
    setBusy(false)
    if (!ok) return
    const at = idxRef.current
    echoAnswer(s)
    if (at >= total) { finish(genRef.current); return }
    const ns = steps[at]
    stepRender(at + 1, total, t(ns.titleKey), (my) => showStep(at + 1, my))
  }, [steps, values, commit, total, finish, genRef, t, stepRender, showStep, echoAnswer])

  // 挂载：面板每次挂上都是从第一步重来（不跨挂载续答），先熄灭上一条异步链再清消息流与栈
  useEffect(() => {
    resetFlow()
    // 由闸门自动打开时，第一句先说"为什么这一屏出现了"（onboard.prompt 那句话从横幅搬进对话流），
    // 再说第一步的事——用户不必先读一条横幅再点按钮，也不用记住两个地方各说什么。
    if (intro) {
      const pid = addMsg({ side: 'ai', full: intro, text: reduced ? intro : '' })
      void typeText(pid, intro, genRef.current)
    }
    const s = steps[0]
    stepRender(1, total, t(s.titleKey), (my) => showStep(1, my))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  /** H2 模型整理腿：只出候选填进表单，**不落库**；任何失败都收成"自己填也一样"一句 */
  const runDraft = useCallback(async () => {
    if (!draftText.trim() || draftBusy) return
    setDraftBusy(true); setDraftNote('')
    try {
      const r = await onboardDraft(draftText)
      const d = (r && r.success ? r.draft : undefined) as Record<string, unknown> | undefined
      const patch: Record<string, unknown> = {}
      if (d) {
        for (const k of ['name', 'tagline', 'use_case'] as const) {
          const v = d[k]
          if (typeof v === 'string' && v.trim()) patch[k] = v.trim()
        }
        // 语种候选只接受站点支持的那几个码；模型凭空造出来的码一律丢（界面不替客户决定对外说法）
        if (Array.isArray(d.target_langs)) {
          const ok = (d.target_langs as unknown[]).filter((x) => typeof x === 'string' && (LANG_OPTIONS as readonly string[]).includes(x as string))
          if (ok.length) patch.target_langs = ok
        }
      }
      // 空候选是**合法结果**（上游没配／超时／回了坏形态）：说清"这次没整理出来"，但不挡住向导
      if (Object.keys(patch).length === 0) setDraftNote(t('onboard.ai.draftEmpty'))
      else { setValues((v) => ({ ...v, ...patch })); setDraftNote(t('onboard.ai.draftDone')) }
    } catch {
      setDraftNote(t('onboard.ai.draftEmpty'))
    } finally {
      setDraftBusy(false)
      scrollBottom()
    }
  }, [draftText, draftBusy, t, scrollBottom])

  /** 单个字段的输入控件：沿用外壳那套 .ar-* 表单档，不新造第二套样式 */
  const fieldInput = (f: OnboardField) => {
    if (f.kind === 'langs') {
      const cur = Array.isArray(values[f.key]) ? (values[f.key] as string[]) : []
      return (
        <div className="ar-chips" data-testid="onboard-langs">
          {LANG_OPTIONS.map((code) => {
            const picked = cur.includes(code)
            return (
              <button key={code} type="button"
                      className={'ar-chip' + (picked ? ' ar-opt--picked' : '')}
                      data-testid={`onboard-lang-${code}`}
                      onClick={() => {
                        const list = Array.isArray(values[f.key]) ? [...(values[f.key] as string[])] : []
                        if (picked) list.splice(list.indexOf(code), 1)
                        else if (!f.maxPick || list.length < f.maxPick) list.push(code)
                        setValues({ ...values, [f.key]: list })
                      }}>
                <span className="ar-chip-v">{nameOf(code)}</span>
              </button>
            )
          })}
        </div>
      )
    }
    if (f.kind === 'toggle') {
      return (
        <label className="ar-ckpt">
          <input type="checkbox" data-testid={`onboard-field-${f.key}`}
                 checked={values[f.key] !== false}
                 onChange={(e) => setValues({ ...values, [f.key]: e.target.checked })} />
          <span>{f.hintKey ? t(f.hintKey) : t(f.labelKey)}</span>
        </label>
      )
    }
    if (f.kind === 'textarea') {
      return (
        <textarea className="lc-input" rows={3} data-testid={`onboard-field-${f.key}`}
                  value={String(values[f.key] ?? '')}
                  onChange={(e) => setValues({ ...values, [f.key]: e.target.value })} />
      )
    }
    return (
      <input className="lc-input" data-testid={`onboard-field-${f.key}`}
             value={String(values[f.key] ?? '')}
             onChange={(e) => setValues({ ...values, [f.key]: e.target.value })} />
    )
  }

  const renderBody = useCallback((m: OnboardMsg) => {
    if (m.block === 'summary') {
      return (
        <div className="ar-done lc-mo-pop">
          <div className="ar-done-t">{t('onboard.title')}</div>
          <div className="ar-kv"><span className="ar-kv-k">{t('onboard.field.productName')}</span><span className="ar-kv-v">{m.summary?.name || '—'}</span></div>
          <div className="ar-kv"><span className="ar-kv-k">{t('onboard.field.targetLangs')}</span><span className="ar-kv-v">{m.summary?.langs || '—'}</span></div>
          <button className="lc-btn lc-btn--secondary lc-btn--block" type="button" data-testid="onboard-goto-detail"
                  onClick={() => nav.navigate(productId ? `/products/detail?id=${encodeURIComponent(productId)}` : '/products/detail')}>
            {t('product.detail.title')}
          </button>
        </div>
      )
    }
    const s = m.step
    if (!s) return null
    const canNext = stepIsCompletable(s, values)
    return (
      <div className="ar-form" data-testid={`onboard-step-${s.id}`}>
        {/* 建档那一步多给一条"贴资料 → 我整理成候选"的腿：这是本场景唯一真调模型的地方（H2） */}
        {s.write === 'product.profile' && (
          <div className="ar-fgroup">
            <textarea className="lc-input" rows={3} data-testid="onboard-draft-text"
                      placeholder={t('onboard.ai.draftPlaceholder')}
                      value={draftText} onChange={(e) => setDraftText(e.target.value)} />
            <button className="lc-btn lc-btn--secondary lc-btn--sm" type="button" data-testid="onboard-draft"
                    disabled={draftBusy || !draftText.trim()} onClick={() => void runDraft()}>
              {draftBusy ? t('auth.aiSubmitting') : t('onboard.ai.draft')}
            </button>
            {draftNote && <p className="ar-done-note" data-testid="onboard-draft-note">{draftNote}</p>}
          </div>
        )}
        {s.fields.map((f) => (
          <div className="ar-fgroup" key={f.key} data-testid={`onboard-field-wrap-${f.key}`}>
            <span className="ar-flabel">{t(f.labelKey)}{f.required ? ' *' : ''}</span>
            {fieldInput(f)}
          </div>
        ))}
        {/* 错误位与注册面同一形态：卡内必须落一条（toast 一闪就没了，用户看到的是"点了没反应"） */}
        {error && <p className="auth-err" role="alert" data-testid="onboard-error">{error}</p>}
        <button className="lc-btn lc-btn--primary lc-btn--block" type="button" data-testid="onboard-next"
                disabled={busy || !canNext} onClick={() => void next()}>
          {busy && <span className="ar-spin" />}{busy ? t('auth.aiSubmitting') : t('onboard.next')}
        </button>
        <button className="ar-link" type="button" data-testid="onboard-later"
                onClick={() => (onDismiss ? onDismiss() : nav.navigate('/', { replace: true }))}>{t('onboard.later')}</button>
      </div>
    )
  }, [busy, draftBusy, draftNote, draftText, error, next, productId, runDraft, t, values])

  return (
    <GuidedChatFrame<OnboardMsg>
      title={t('onboard.title')}
      onlineLabel={t('auth.aiOnline')}
      closeLabel={t('auth.skipForNow')}
      onClose={() => (onDismiss ? onDismiss() : nav.navigate('/', { replace: true }))}
      stepLabel={stepLabel}
      trackPct={trackPct}
      backLabel={t('auth.aiBack')}
      backHidden={backHidden}
      backDisabled={busy}
      onBack={goBack}
      badgeLabel="AI"
      whoLabel={t('onboard.title')}
      msgs={msgs}
      renderBody={renderBody}
      chatRef={chatRef}
    />
  )
}
