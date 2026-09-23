// ============================================================================
// components/ModeToggle.tsx — 快速/专业校对 双模式分段切换（即时翻译 & 翻译工单共用）
// 等价 Vue 版 ChatWindow 的模式切换按钮；fastFirst 控制排列顺序与默认视觉，value 受控。
// 视觉：langcross 分段控件——inset 轨道 + 活跃项 raised 白字（X/Grok 单色，无蓝）。
// ============================================================================
import { t } from '@/i18n'

// ============ 本文件职责中文说明 ============
// 快速/专业校对双模式分段切换控件，供即时翻译与翻译工单共用。
// ========================================

interface Props {
  value: 'fast' | 'pro'
  onChange: (m: 'fast' | 'pro') => void
  /** 为 true 时左为快速、右为专业（翻译工单用）；默认左专业、右快速（即时翻译用） */
  fastFirst?: boolean
}

// 默认导出组件：双模式分段按钮
export default function ModeToggle({ value, onChange, fastFirst = false }: Props) {
  const pro = { key: 'pro' as const, label: t('chat.modePro') }
  const fast = { key: 'fast' as const, label: t('chat.modeFast') }
  // 只换排列顺序、不改 value 语义：即时翻译用默认顺序（专业在左），工单传 fastFirst 让快速在左
  const items = fastFirst ? [fast, pro] : [pro, fast]
  return (
    // 用原生 button 拼分段控件后必须自己给组语义：role=group + aria-label，
    // 否则读屏只会念出两个孤立按钮，听不出它们是一对互斥选项
    <div className="mt-seg" role="group" aria-label={t('chat.modeTip')}>
      {items.map((it) => (
        <button
          key={it.key}
          // 原生 button 的 type 默认就是 submit；分段控件永远不该提交表单，显式写死 button，
          // 免得将来被放进某个 <form> 时点击顺带触发整页刷新
          type="button"
          className={'mt-seg__item' + (value === it.key ? ' mt-seg__item--on' : '')}
          title={t('chat.modeTip')}
          onClick={() => onChange(it.key)}
        >
          {it.label}
        </button>
      ))}
      {/* 样式随组件一起注入（本项目不引全局组件样式表）；同屏多个实例会重复插同一份 CSS，体积可忽略 */}
      <style>{CSS_MT}</style>
    </div>
  )
}

// 页面级样式：mt- 前缀（防与组件库/其他页面类名重名）
// 只过渡颜色与背景、时长统一取 --lc-mo-* 动效令牌，方便在 prefers-reduced-motion 下一并收敛
const CSS_MT = `
.mt-seg{display:inline-flex;background:var(--lc-inset);border:2px solid var(--lc-border-card);border-radius:8px;padding:2px;gap:2px}
.mt-seg__item{height:28px;padding:0 14px;border:0;border-radius:6px;background:transparent;color:var(--lc-text-3);font-size:15px;font-family:var(--lc-font);cursor:pointer;transition:color var(--lc-mo-release) var(--lc-mo-out),background var(--lc-mo-release) var(--lc-mo-out)}
.mt-seg__item:hover{color:var(--lc-text)}
.mt-seg__item--on{background:var(--lc-raised);color:var(--lc-text)}
`
