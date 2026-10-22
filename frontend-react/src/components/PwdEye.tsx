// ============================================================================
// components/PwdEye.tsx — 密码可见切换的**唯一实现**（★ 2026-10-21 IAM 重做 P1-3/P1-4）
//
// 为什么抽成独立文件：这段 JSX 原先写在 Login.tsx 里，注释写着"登录/注册/找回/强制改密
// 四处共用"，而 `ForcePasswordGate`（强制改密窗）是独立组件、根本 import 不到它——
// 于是那个"必须过"的弹窗里三个密码框全是盲输（旧口令 + 新口令 + 确认口令），
// 而它是批量导入账号的必经之路，初始口令还是随机串。注释承诺四处、实际三处。
// 2026-10-22 一致性核查把实现挪到本文件，Login.tsx 与 ForcePasswordGate 都从这里取。
//
// ⚠️ 样式：`.auth-pwd-eye` 定义在 Login.tsx 的 CSS_AUTH 里，只有登录面加载。
//   强制改密窗走 Dialog，不加载那段 CSS ⇒ 这里带 inline 兜底（同 ProfileSetupGate 的 .auth-err 手法）。
//   inline 值与 CSS_AUTH 里那条**逐项同值**，不引入第二种视觉。
//
// aria-label 走现成的 showPwd/hidePwd 两键——历史上一处写成了词典里不存在的键，
// 屏幕阅读器会把键名字符串念出来；共用这一个组件就不再有"某处忘了改"的形态。
// ============================================================================
import { EyeIcon, EyeOffIcon } from '@/ui/langcross/src'
import { t } from '@/i18n'

// PwdEye 密码可见切换钮（Login / ForcePasswordGate / ProfileSetupGate 共用这一个实现）。
//   shown=当前是否明文；onToggle=点击回调（由各调用方持有显隐状态，本组件无内部状态——
//   三处密码框的显隐必须同进同退，状态外提才不会出现"第一个框亮了后两个还盲着"）。
//   样式走 className＋inline 双轨：`.auth-pwd-eye` 在登录面 CSS 里，强制改密窗（Dialog）
//   不加载那段 CSS，inline 兜底值与 CSS_AUTH 逐项同值（理由见文件头）。
export default function PwdEye({ shown, onToggle }: { shown: boolean; onToggle: () => void }) {
  return (
    <button
      type="button"
      className="lc-input-affix auth-pwd-eye"
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        background: 'none',
        border: 0,
        color: 'var(--lc-text-3)',
        cursor: 'pointer',
        padding: 4,
        lineHeight: 0,
      }}
      aria-pressed={shown}
      aria-label={shown ? t('auth.hidePwd') : t('auth.showPwd')}
      onClick={onToggle}
    >
      {shown ? <EyeOffIcon /> : <EyeIcon />}
    </button>
  )
}
