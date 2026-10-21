import type { ReactNode } from "react";

// 认证卡外壳入参：标题/副标题/正文与底部链接（登录·注册·找回密码共用）
export interface AuthCardProps {
  title: ReactNode;
  /** 标题下的说明行；不传不渲染 */
  desc?: ReactNode;
  /** 表单区（Field / Input / Checkbox…） */
  children?: ReactNode;
  /** 提交按钮文案，默认「登录」；样式为 primary 通栏 */
  submitText?: ReactNode;
  onSubmit?: () => void;
  /** 底部链接行，如「还没有账号？注册」 */
  foot?: ReactNode;
  /** 卡片右上角浮件（语言切换胶囊等）；绝对定位在角上，不参与纵向节奏 */
  corner?: ReactNode;
  /**
   * busy=true ⇒ 提交钮禁用并标 aria-busy（★ 2026-10-21 IAM 重做 P1-2）。
   * 旧形态这里**没有**禁用档：登录/注册/找回三张面的提交钮在请求飞行中照样可点，
   * 于是"连点两下"会真的发两次请求——注册那一腿第二次必吃「该邮箱已被其他账号绑定」，
   * 用户看到的是"我点了注册它报错"。
   * ⚠️ 刻意**不加新视觉**：禁用态直接复用交付档 `.lc-btn:disabled`
   *   （background=--lc-raised / color=--lc-disabled / border=--lc-border-faint，
   *   components.css:59 现值），不新造 loading 样式、不动色值与字阶（AGENTS §一·5 界面规格＝UI 交付真值）。
   */
  busy?: boolean;
  className?: string;
}

/**
 * 认证卡（登录 / 注册 / 忘记密码提炼）：400 宽 panel 卡 + 圆角 14，
 * 通栏 primary 提交。外层配 <div className="lc-auth-bg"> 全屏黑底居中。
 */
export function AuthCard({
  title,
  desc,
  children,
  submitText = "登录",
  onSubmit,
  foot,
  corner,
  busy = false,
  className = "",
}: AuthCardProps) {
  return (
    <div className={`lc-auth-card ${className}`.trim()}>
      {corner ? <div className="lc-auth-card__corner">{corner}</div> : null}
      <h1 className="lc-auth-card__title">{title}</h1>
      {desc ? <p className="lc-auth-card__desc">{desc}</p> : null}
      {children}
      <button
        type="button"
        className="lc-btn lc-btn--primary lc-auth-card__submit"
        onClick={onSubmit}
        disabled={busy}
        aria-busy={busy || undefined}
      >
        {submitText}
      </button>
      {foot ? <p className="lc-auth-card__foot">{foot}</p> : null}
    </div>
  );
}
