import type { ReactNode } from "react";

export interface KeyValueProps {
  label: ReactNode;
  value: ReactNode;
  /** 标签列宽：默认 72，传 wide 为 88（抽屉等更宽场景） */
  size?:"sm"|"wide";
  /** 值用等宽字体（时间 / 数值） */
  mono?: boolean;
  className?: string;
}

/**
 * 键值行：标签定宽 + 值 fill 左对齐。
 * 规范：不要 SPACE_BETWEEN 右对齐——值从固定列起点起排，多行键值才竖得齐。
 */
export function KeyValue({ label, value, size ="sm", mono = false, className =""}: KeyValueProps) {
  return (
    <div className={`lc-kv ${size ==="wide"?"lc-kv--wide":""} ${className}`.trim()}>
      <span className="lc-kv__key">{label}</span>
      <span
        className="lc-kv__val"
        style={mono ? { fontFamily:"var(--lc-font-mono)"} : undefined}
      >
        {value}
      </span>
    </div>
  );
}
