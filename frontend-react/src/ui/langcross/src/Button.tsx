import type { ButtonHTMLAttributes, ReactNode } from "react";

export type ButtonVariant ="primary"|"secondary"|"danger";
export type ButtonSize ="md"|"sm";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  /** primary = 白底黑字（一屏一个）；secondary = 描边无底；danger = 红底白字 */
  variant?: ButtonVariant;
  size?: ButtonSize;
  /** 胶囊形（radius 999），营销页 CTA 用 */
  pill?: boolean;
  icon?: ReactNode;
  children?: ReactNode;
}

/**
 * 按钮。disabled 样式由 CSS 统一接管（.lc-btn:disabled），
 * 无需单独 variant —— 这是规范：禁用永远长一个样。
 */
export function Button({
  variant = "primary",
  size = "md",
  pill = false,
  icon,
  className = "",
  type = "button",
  children,
  ...rest
}: ButtonProps) {
  const cls = [
    "lc-btn",
    `lc-btn--${variant}`,
    size ==="sm"?"lc-btn--sm":"",
    pill ?"lc-btn--pill":"",
    className,
  ]
    .filter(Boolean)
    .join(" ");
  return (
    <button type={type} className={cls} {...rest}>
      {icon}
      {children}
    </button>
  );
}
