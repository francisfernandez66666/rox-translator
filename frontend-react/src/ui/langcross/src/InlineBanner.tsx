import type { ReactNode } from "react";

// 提示条三档语义色（success 白档、warn #D29922、error #E5484D，全站无绿）
export type BannerTone ="success"|"error"|"warn";

// 页面提示条入参：语气档 + 文案（高 40、左侧 8px 状态点）
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
