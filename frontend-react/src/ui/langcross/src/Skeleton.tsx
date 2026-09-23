import type { CSSProperties, ReactNode } from "react";

// 骨架条入参：宽高/圆角（呼吸动画 1.4s）
export interface SkeletonProps {
  width?: number | string;
  height?: number;
  /** 圆角 6（骨架条规范值） */
  radius?: number;
  style?: CSSProperties;
  className?: string;
}

/** 骨架条：#16181C 圆角 6，呼吸 1.4s（prefers-reduced-motion 时静止为 .6 透明度） */
export function Skeleton({ width ="100%", height = 12, radius, style, className =""}: SkeletonProps) {
  return (
    <div
      className={`lc-skeleton ${className}`.trim()}
      style={{ width, height, borderRadius: radius ?? "var(--lc-r-bar)", ...style }}
    />
  );
}

// 骨架卡入参：行数与是否带头像位
export interface SkeletonCardProps {
  /** 行数，默认 3（标题 + 两行正文），末尾自动补一条按钮位 */
  rows?: number;
  width?: number | string;
}

/** 骨架卡：内嵌底 + 暗边 + 圆角 12，行宽递减模拟正文版式 */
export function SkeletonCard({ rows = 3, width = 320 }: SkeletonCardProps) {
  const widths = ["44%", "100%", "74%", "80%", "62%"];
  return (
    <div
      style={{
        width,
        padding: 16,
        display: "flex",
        flexDirection: "column",
        gap: 12,
        background: "var(--lc-inset)",
        border: "1px solid var(--lc-border-card-dim)",
        borderRadius: "var(--lc-r-card)",
      }}
    >
      {Array.from({ length: rows }).map((_, i) => (
        <Skeleton key={i} height={12} width={widths[i % widths.length]} />
      ))}
      <Skeleton height={24} width={88} />
    </div>
  );
}

// 空态入参：图标/标题/说明/动作按钮（面 #0A0B0D）
export interface EmptyStateProps {
  icon?: ReactNode;
  title: ReactNode;
  desc?: ReactNode;
  /** 主按钮文案；一屏一个主按钮，空状态卡里的动作默认次级描边 */
  actionText?: ReactNode;
  onAction?: () => void;
  className?: string;
}

/** 空状态：#0A0B0D 卡 + 暗边 + 圆角 12，图标 28 弱灰 */
export function EmptyState({ icon, title, desc, actionText, onAction, className =""}: EmptyStateProps) {
  return (
    <div className={`lc-empty ${className}`.trim()}>
      {icon ? <span style={{ color:"var(--lc-text-3)", display:"inline-flex"}}>{icon}</span> : null}
      <span className="lc-empty__title">{title}</span>
      {desc ? <span className="lc-empty__desc">{desc}</span> : null}
      {actionText ? (
        <button
          type="button"
          className="lc-btn lc-btn--primary lc-btn--sm"
          style={{ marginTop: 4 }}
          onClick={onAction}
        >
          {actionText}
        </button>
      ) : null}
    </div>
  );
}
