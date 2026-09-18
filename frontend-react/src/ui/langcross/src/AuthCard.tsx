import type { ReactNode } from "react";

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
  className = "",
}: AuthCardProps) {
  return (
    <div className={`lc-auth-card ${className}`.trim()}>
      {corner ? <div className="lc-auth-card__corner">{corner}</div> : null}
      <h1 className="lc-auth-card__title">{title}</h1>
      {desc ? <p className="lc-auth-card__desc">{desc}</p> : null}
      {children}
      <button type="button"className="lc-btn lc-btn--primary lc-auth-card__submit"onClick={onSubmit}>
        {submitText}
      </button>
      {foot ? <p className="lc-auth-card__foot">{foot}</p> : null}
    </div>
  );
}
