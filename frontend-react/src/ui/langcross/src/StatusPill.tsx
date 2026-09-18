import type { ReactNode } from "react";

export type StatusTone ="success"|"danger"|"warn"|"idle";

const TONE_COLOR: Record<StatusTone, string> = {
  success: "var(--lc-success)",
  danger: "var(--lc-red)",
  warn: "var(--lc-warn)",
  idle: "var(--lc-text-3)",
};

export interface StatusPillProps {
  tone: StatusTone;
  children: ReactNode;
  className?: string;
}

/** 状态胶囊：6px 状态点 + 12px 文案，描边 --lc-border-pill 胶囊 */
export function StatusPill({ tone, children, className =""}: StatusPillProps) {
  return (
    <span className={`lc-pill ${className}`.trim()}>
      <span className="lc-pill__dot"style={{ background: TONE_COLOR[tone] }} />
      {children}
    </span>
  );
}

export interface BadgeProps {
  children: ReactNode;
  /** mono = JetBrains Mono（版本号 / 数值） */
  mono?: boolean;
  className?: string;
}

/** 徽章：浮面小底 + 10px，表格内联计数用 */
export function Badge({ children, mono = false, className =""}: BadgeProps) {
  return <span className={`lc-badge ${mono ?"lc-badge--mono":""} ${className}`.trim()}>{children}</span>;
}
