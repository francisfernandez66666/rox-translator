// ============================================================================
// components/SkillBadge.tsx — 技能徽章（等价 Vue SkillBadge.vue）
// 职责：在 AI 回复气泡顶部显示当前技能标识（固定展示"🌐 翻译"）
// ============================================================================
import { t } from '@/i18n'

export interface SkillBadgeProps {
  skill?: string
}

export function SkillBadge(_props: SkillBadgeProps) {
  // 固定展示"🌐 翻译"技能标识（当前版本仅翻译一种技能，参数预留扩展）
  return <span className="skill-badge">🌐 {t('chat.skillLabel')}</span>
}

export default SkillBadge
