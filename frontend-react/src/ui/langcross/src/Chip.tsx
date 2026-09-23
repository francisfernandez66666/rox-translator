import type { ReactNode } from "react";

// 胶囊组入参：可选项列表 + 当前值 + 单选/多选回调
export interface ChipGroupProps {
  items: { key: string; label: ReactNode }[];
  activeKey: string;
  onChange?: (key: string) => void;
  className?: string;
}

/**
 * 选择 chip 组（增长漏斗「近7天 / 近30天 / 近90天」提炼）：
 * raised 底 + faint 边，活跃项白字 + pill 档描边。
 */
export function ChipGroup({ items, activeKey, onChange, className =""}: ChipGroupProps) {
  return (
    <div className={`lc-toolbar ${className}`.trim()} style={{ marginBottom: 0 }}>
      {items.map((item) => (
        <button
          key={item.key}
          type="button"
          className={`lc-chip${item.key === activeKey ? " lc-chip--active":""}`}
          onClick={() => onChange?.(item.key)}
        >
          {item.label}
        </button>
      ))}
    </div>
  );
}
