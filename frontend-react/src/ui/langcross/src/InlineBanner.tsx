import type { ReactNode } from "react";

export type BannerTone ="success"|"error"|"warn";

export interface InlineBannerProps {
  tone: BannerTone;
  children: ReactNode;
  className?: string;
}

/**
 * 内联提示条：高 40、圆角 10、无阴影、左侧 8px 状态点。
 * 出现在页面流里（表单上方 / 列表上方），不飘浮——飘浮的是 Toast。
 */
export function InlineBanner({ tone, children, className =""}: InlineBannerProps) {
  return (
    <div className={`lc-banner lc-banner--${tone} ${className}`.trim()} role="status">
      <span className="lc-banner__dot"/>
      {children}
    </div>
  );
}
