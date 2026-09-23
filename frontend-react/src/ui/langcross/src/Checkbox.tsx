import type { InputHTMLAttributes, ReactNode } from "react";

// 复选框入参：选中态走白底黑勾（交付真值，禁用彩色）
export interface CheckboxProps extends InputHTMLAttributes<HTMLInputElement> {
  label?: ReactNode;
}

/**
 * 复选框。选中态白底黑勾（X/Grok 单色，--lc-success）。
 * label 13px #E7E9EA；disabled 时文字交给调用方用 --lc-disabled。
 *
 * ★ children 是 label 的等价写法，必须**显式摘出来**（2026-09-18 修复）：
 *   `InputHTMLAttributes` 自带 `children?: ReactNode`（继承自 DOMAttributes），
 *   所以 `<Checkbox>文字</Checkbox>` 能通过类型检查 —— 但 `children` 会随 `{...rest}`
 *   一起摊到 `<input>` 上，运行期直接抛
 *   「input is a void element tag and must neither have children nor use
 *   dangerouslySetInnerHTML」，整屏被 ErrorBoundary 兜掉（注册流程曾因此全崩）。
 */
export function Checkbox({ label, children, className = "", style, ...rest }: CheckboxProps) {
  const content = label ?? children;
  return (
    <label className={`lc-check-row ${className}`.trim()} style={{ display: "inline-flex", alignItems: "center", gap: 10, ...style }}>
      <input type="checkbox"className="lc-checkbox"{...rest} />
      {content ? <span style={{ fontSize: 13, color: rest.disabled ?"var(--lc-disabled)":"var(--lc-text)"}}>{content}</span> : null}
    </label>
  );
}

// 开关入参：活跃态小控件，配色走文字档 #E7E9EA
export interface SwitchProps extends InputHTMLAttributes<HTMLInputElement> {
  label?: ReactNode;
  /** label 放在开关右侧（规范布局） */
}

/** 开关：36×20 轨道，选中白底黑球右移；label 在右。 */
export function Switch({ label, children, className = "", style, ...rest }: SwitchProps) {
  // 同 Checkbox：children 与 label 等价，必须摘出来，不能摊进 <input>
  const content = label ?? children;
  return (
    <label className={`lc-switch-row ${className}`.trim()} style={{ display: "inline-flex", alignItems: "center", gap: 10, ...style }}>
      <input type="checkbox"role="switch"className="lc-switch"{...rest} />
      {content ? <span style={{ fontSize: 13, color: rest.disabled ?"var(--lc-disabled)": rest.checked ?"var(--lc-text)":"var(--lc-text-2)"}}>{content}</span> : null}
    </label>
  );
}
