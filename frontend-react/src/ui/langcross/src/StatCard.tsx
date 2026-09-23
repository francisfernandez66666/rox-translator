import type { ReactNode } from "react";

// 统计卡语气档：决定数值描边与状态点色
export type StatTone ="default"|"success"|"danger"|"warn";

// 统计卡入参：标题/数值/单位/趋势与语气档
export interface StatCardProps {
  /** 主数值，如「7046」「10/10」「0.0%」 */
  value: ReactNode;
  /** 标签，如「知识条目」「LLM 错误率」 */
  label: ReactNode;
  /** 数值着色：success 白 / danger 语义红 / warn 琥珀（默认白） */
  tone?: StatTone;
  /** 头部右侧小件（如「● 正常」状态胶囊） */
  extra?: ReactNode;
}

/** 统计卡：raised 底 + 顶缘高光，20/700 数值 + 12 灰标签。外层用 <div className="lc-stat-row"> 排网格 */
export function StatCard({ value, label, tone = "default", extra }: StatCardProps) {
  const toneCls = tone ==="default"?"": ` lc-stat__value--${tone}`;
  return (
    <div className="lc-stat">
      <div className="lc-stat__head">
        <span className={`lc-stat__value${toneCls}`}>{value}</span>
        {extra}
      </div>
      <span className="lc-stat__label">{label}</span>
    </div>
  );
}

/** 统计卡网格容器：auto-fit、最小 150、间距 12 */
export function StatRow({ children }: { children: ReactNode }) {
  return <div className="lc-stat-row">{children}</div>;
}
