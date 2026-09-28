// ============================================================================
// components/TrialPanel.tsx — 官网首页「免登录试翻一句」面板（★ 2026-09-28 〇-Z）
//
// 为什么要有这张卡：用户反馈「首页点什么都要先注册」是劝退形态——已经有账号的人
// 被要求再注册一次，访客也无法在掏邮箱之前判断译文质量。这张卡把判断权还给一次
// 真实翻译：给 5 句、强制专业校对档（第一句就拿到糙活，额度再宽也是白送），用完出注册引导。
//
// 形态口径（用户 2026-09-28 原话「hero页的动效不用动，点击动效以后再切换成翻译体验」）：
//   · 本卡**替换** hero 右侧演示卡的位置（Landing.tsx 里 .lc-hero-demo 内部条件渲染），
//     HeroDemo 自身一行未改，「返回演示」即把位置原样交还；
//   · 卡内不写死域名、不依赖登录态：设备号取 lib/trialDevice.ts，语种取 /api/translation/langs。
//
// 三条必须留神的账：
//   1. 分支一律按**稳定码**判（TRIAL_EXHAUSTED / RATE_LIMITED / VALIDATION_ERROR…），
//      不拿 message 文案判分支——文案会随语种与措辞改，码是契约（错误体经 bizResp 摊平，
//      reason 与 retry_after 都在顶层）；
//   2. 只有成功交付才计数，所以"剩 N 句"只能在**响应回来之后**更新，不能在发送时预扣；
//      首发时 left 未知，界面显示额度规则而不是一个假的数字；
//   3. 试用一句要跑两趟模型（初翻＋校对），慢是正常态：请求进行中禁用重复提交，
//      并用 reqRef 代际号丢弃过期响应（切语种/连点时旧响应不能覆盖新结果）。
//
// 样式在 Landing.tsx 的 LANDING_CSS 里（.lc-trial-*）：官网样式只此一处来源，组件内联 style
// 会绕开交付稿的令牌口径（AGENTS.md §一·5 五类渲染面第①类）。
// ============================================================================
import { useEffect, useRef, useState } from 'react'
import { useT } from '@/i18n' // [lang, t, tpl]：文案 100% 走 land.trial.*，本文件不出现裸中文
import { langLabel } from '@/lib/langNames' // 语种名与全站同一份事实（只有 zh* 界面取中文名，其余英文名）
import { trialDeviceID } from '@/lib/trialDevice' // 设备号：localStorage 持久化，与服务端字符集同口径
import { trialTranslate, trialLangs, TRIAL_MAX_CHARS, type TrialLang } from '@/api/trial'

/** 名单接口不可用时的兜底语种：只取一定在后端试用白名单里的常用档。
 *  为什么要有这一份：试用入口不能因为一次拉表失败就退化成"只能翻成中文"——中文界面的默认目标
 *  本来是 en，框里却只剩 zh，界面显示与实际提交就会各说各话。
 *  ⚠️ name/name_en 这里只是占位（显示一律走 langLabel 那份全站语种名表），别拿它们当文案真值。 */
const FALLBACK_LANGS: TrialLang[] = [
  { code: 'en', name: 'en', name_en: 'en' },
  { code: 'ja', name: 'ja', name_en: 'ja' },
  { code: 'de', name: 'de', name_en: 'de' },
  { code: 'fr', name: 'fr', name_en: 'fr' },
  { code: 'ru', name: 'ru', name_en: 'ru' },
]

/** 试用面板：hero 右侧演示卡的「就地体验」形态 */
export default function TrialPanel(props: {
  onBack: () => void // 交还演示卡（HeroDemo 重新挂载，动效从头演）
}) {
  const [lang, t, tpl] = useT()
  // 默认目标语种：中文界面演示 ZH→EN，其余界面翻成中文（与 HeroDemo 的 EN→ZH 演示方向对齐，
  // 访客点开的第一眼就能看到"和刚才那张卡相反"的真实跨语向结果）
  const defaultTarget = lang.startsWith('zh') ? 'en' : 'zh'
  const [text, setText] = useState('')
  const [target, setTarget] = useState(defaultTarget)
  const [langs, setLangs] = useState<TrialLang[]>([]) // 语种盘（含 zh 兜底，见下方 opts）
  const [langFallback, setLangFallback] = useState(false) // 名单没拉到：给一句小字，别让访客以为语种盘就这么大
  const [busy, setBusy] = useState(false)
  const [out, setOut] = useState<{ tr: string; src: string } | null>(null)
  const [left, setLeft] = useState<number | null>(null) // null=本次会话还没拿到过成功响应
  const [done, setDone] = useState<'' | 'device' | 'ip' | 'global'>('') // 非空即出注册引导卡
  const [err, setErr] = useState('')
  const [copied, setCopied] = useState(false)
  const reqRef = useRef(0) // 代际号：只有最后一次请求的响应可以落盘到界面
  const areaRef = useRef<HTMLTextAreaElement>(null)

  // 挂载即拉语种盘：这份名单与后端试用白名单同源，访客选得到一个后端肯收的语种
  useEffect(() => {
    const g = ++reqRef.current
    trialLangs().then((r) => {
      if (g !== reqRef.current) return // 卸载后回写到已销毁组件
      setLangs(r.langs)
      setLangFallback(r.langs.length === 0)
    })
  }, [])

  // 下拉选项 = zh ∪ 语种盘（按 code 去重）。两处刻意的兜底：
  //   1. 后端允许"外语翻回中文"，而 /langs 名单本身不含 zh（它是目标语种盘），所以显式补 zh；
  //   2. 名单接口挂了不能把语种框收成只剩中文——默认目标语种（英文界面是 zh、中文界面是 en）
  //      会落进空选项，界面显示首个语种、实际提交另一个，那是"读写不同源"的前端版。
  const opts: TrialLang[] = (() => {
    const map = new Map<string, TrialLang>()
    map.set('zh', { code: 'zh', name: '中文', name_en: 'Chinese' })
    for (const l of (langs.length ? langs : FALLBACK_LANGS)) map.set(l.code, l)
    return Array.from(map.values())
  })()

  const runes = Array.from(text).length // 按码点数：与后端 []rune 同口径，中文一个字算 1 而不是 3 字节

  const submit = async () => {
    if (busy || done) return // 试用中／已出引导卡：不接受并发提交（引导卡下没有输入框，防的是键盘回车残留焦点）
    setErr('')
    const src = text.trim()
    if (!src) { setErr(t('land.trial.errEmpty')); return }
    if (Array.from(src).length > TRIAL_MAX_CHARS) { setErr(t('land.trial.errTooLong')); return }
    const g = ++reqRef.current
    setBusy(true)
    let r: Awaited<ReturnType<typeof trialTranslate>>
    try {
      r = await trialTranslate({ text: src, target_lang: target, device_id: trialDeviceID() })
    } catch {
      // 只有 401/403 会从这里抛（试用面不需要凭证，抛出即说明接线异常）：给一句可重试的话，
      // 不把异常文本丢给访客——那是把开发者信息当用户文案。
      if (g === reqRef.current) { setErr(t('land.trial.errFail')); setBusy(false) }
      return
    }
    if (g !== reqRef.current) return // 过期响应：连点/换语种时旧回包不得覆盖新状态，busy 由新一轮负责
    setBusy(false)
    const code = typeof r.code === 'string' ? r.code : ''
    if (r.success) {
      setOut({ tr: String(r.translation || ''), src: String(r.source_lang || '') })
      const l = typeof r.left === 'number' ? r.left : null
      setLeft(l)
      if (r.exhausted === true || (l !== null && l <= 0)) setDone('device') // 剩余归零即切引导卡（下一句必被拒，别让访客白点）
      return
    }
    if (code === 'TRIAL_EXHAUSTED') {
      const reason = (typeof r.reason === 'string' ? r.reason : 'device') as 'device' | 'ip' | 'global'
      setDone(reason)
      setLeft(0)
      return
    }
    if (code === 'RATE_LIMITED') {
      // 最小间隔拦的（默认 3 秒）：这不是额度见底，给秒数而不是注册引导，否则访客以为没额度就走了
      const sec = typeof r.retry_after === 'number' && r.retry_after > 0 ? r.retry_after : 3
      setErr(tpl('land.trial.errFast', { n: sec }))
      return
    }
    if (code === 'VALIDATION_ERROR' && typeof r.message === 'string' && r.message) {
      // 校验类（内容过长／语种不在范围）：后端直出的 message 已过 i18n 词条表，按访客界面语种给话；
      // 拿它而不是本地键，是为了不维护第二份"服务端判据的前端猜测文案"（两边数字迟早漂移）
      setErr(r.message)
      return
    }
    setErr(t('land.trial.errFail')) // 503（服务启动中）／翻译失败：可重试语义，不扣额度
  }

  const copyOut = () => {
    if (!out) return
    navigator.clipboard?.writeText(out.tr).then(() => {
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1600)
    }).catch(() => { /* 非安全上下文没有剪贴板：静默失败，译文照样能选中复制 */ })
  }

  // 额度用完：三种 reason 给三种话（设备用完了／这个网络今天用得多／平台当日名额满），
  // 因为访客的下一步动作不同——换设备、等明天、还是直接注册。
  const doneMsg = done === 'global' ? t('land.trial.doneGlobal')
    : done === 'ip' ? t('land.trial.doneIP')
      : t('land.trial.doneDevice')

  return (
    <div className="lc-trial" data-trial="1">
      <div className="lc-trial-head">
        <div className="lc-trial-head-txt">
          <span className="lc-trial-mode">{t('land.trial.mode')}</span>
          <h3 className="lc-trial-title">{t('land.trial.title')}</h3>
          <p className="lc-trial-sub">{t('land.trial.sub')}</p>
        </div>
        <button type="button" className="lc-trial-back" onClick={props.onBack}>{t('land.trial.back')}</button>
      </div>

      {done ? (
        <div className="lc-trial-done">
          <p className="lc-trial-done-t">{t('land.trial.doneTitle')}</p>
          <p className="lc-trial-done-d">{doneMsg}</p>
          {/* 引导去注册（试用是给"要不要注册"提供依据的，这里就是它唯一该出主投的地方）；
              同时给登录入口：已有账号的访客不该被"再注册一次"挡住 */}
          <div className="lc-trial-done-cta">
            <a className="lc-trial-btn lc-trial-btn--pri" href="/register">{t('land.trial.register')}</a>
            <a className="lc-trial-btn lc-trial-btn--sec" href="/login">{t('land.navLogin')}</a>
          </div>
          <button type="button" className="lc-trial-link" onClick={props.onBack}>{t('land.trial.back')}</button>
        </div>
      ) : (
        <>
          <textarea
            ref={areaRef}
            className="lc-trial-input"
            value={text}
            maxLength={TRIAL_MAX_CHARS} // 服务端还会按同样上限判 400（前端只是提前拦，判据在后端）
            placeholder={t('land.trial.placeholder')}
            rows={3}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) void submit() }}
          />
          <div className="lc-trial-bar">
            <span className="lc-trial-count">{tpl('land.trial.chars', { n: runes })}</span>
            <label className="lc-trial-to">
              <span className="lc-trial-to-label">{t('land.trial.to')}</span>
              <select className="lc-trial-select" value={target} onChange={(e) => setTarget(e.target.value)}>
                {opts.map((l) => <option key={l.code} value={l.code}>{langLabel(l.code, lang)}</option>)}
              </select>
            </label>
            <button type="button" className="lc-trial-btn lc-trial-btn--pri" onClick={() => void submit()} disabled={busy}>
              {busy ? t('land.trial.busy') : t('land.trial.go')}
            </button>
          </div>
          {/* 名单没拉到：给一句小字说明当前是"常用语种"而不是全部语种，
              放在 err 之外（这不是错误态，试用照常能翻，不该抢红色） */}
          {langFallback && <p className="lc-trial-note">{t('land.trial.langFail')}</p>}
          <div className="lc-trial-out">
            <div className="lc-trial-out-head">
              <span className="lc-trial-out-label">{t('land.trial.out')}</span>
              {out && (
                <button type="button" className="lc-trial-copy" onClick={copyOut}>
                  {copied ? t('land.demoCopied') : t('land.demoCopy')} {/* 复用演示卡的复制词条：同一动作同一份文案 */}
                </button>
              )}
            </div>
            <p className={out ? 'lc-trial-out-text' : 'lc-trial-out-empty'}>
              {out ? out.tr : t('land.trial.outEmpty')}
            </p>
          </div>
          {err && <p className="lc-trial-err" role="alert">{err}</p>}
          {/* 剩余句数只在拿到过成功响应后才显示：首发时显示它只能是个假数字（计数按"成功才计"） */}
          {left !== null && <p className="lc-trial-foot">{tpl('land.trial.left', { n: left })}</p>}
        </>
      )}
    </div>
  )
}
