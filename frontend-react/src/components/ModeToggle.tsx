// ============================================================================
// components/ModeToggle.tsx — 快速/专业校对 双模式分段切换（即时翻译 & 翻译工单共用）
// 等价 Vue 版 ChatWindow 的模式切换按钮；fastFirst 控制排列顺序与默认视觉，value 受控。
// ============================================================================
import { t } from '@/i18n'

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
  const items = fastFirst ? [fast, pro] : [pro, fast]
  return (
    <div style={{ display: 'inline-flex', background: 'rgba(26,115,232,.06)', borderRadius: 14, padding: 2, gap: 2 }}>
      {items.map((it) => (
        <button key={it.key} title={t('chat.modeTip')} onClick={() => onChange(it.key)}
          style={{
            border: '1px solid #d8dee6',
            background: value === it.key ? '#1a73e8' : '#fff',
            color: value === it.key ? '#fff' : '#777',
            fontSize: 14, padding: '6px 9px', borderRadius: 12, cursor: 'pointer',
          }}>
          {it.label}
        </button>
      ))}
    </div>
  )
}
