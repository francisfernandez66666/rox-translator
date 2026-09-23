import { cloneElement, isValidElement, useId } from "react";
import type {
  InputHTMLAttributes,
  ReactElement,
  ReactNode,
  SelectHTMLAttributes,
  TextareaHTMLAttributes,
} from "react";
import { CaretDownIcon } from "./icons";

// 表单字段壳入参：标签 + 提示/错误文案（标签 12 #9AA0AA）
export interface FieldProps {
  label?: ReactNode;
  error?: ReactNode;
  children: ReactNode;
  /** 传给 .lc-field 的额外类名 */
  className?: string;
}

/**
 * 字段容器：12px 标签 + 控件 + 12px 错误文案（错误态描边由控件自己的 error 类负责）
 *
 * 标签与控件通过 useId 建立 htmlFor/id 关联——点击标签可聚焦输入框、读屏能正确播报字段名。
 * 交付包原始实现 import 了 useId 却未接线（label 是裸 span），此处按原意补齐。
 * 若子元素自带 id 则尊重调用方取值，不覆盖。
 */
export function Field({ label, error, className = "", children }: FieldProps) {
  const autoId = useId();
  const control = isValidElement(children)
    ? cloneElement(children as ReactElement<{ id?: string }>, {
        id: (children.props as { id?: string } | undefined)?.id ?? autoId,
      })
    : children;

  return (
    <div className={`lc-field ${className}`.trim()}>
      {label ? (
        <label className="lc-field__label"htmlFor={autoId}>
          {label}
        </label>
      ) : null}
      {control}
      {error ? (
        <span className="lc-field__error"role="alert">
          {error}
        </span>
      ) : null}
    </div>
  );
}

// 输入框入参：错误态标记与尾部插槽（控件高 37/移动 44）
export interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  /** 传入即进入错误态（描边 #E5484D） */
  error?: boolean;
  /**
   * 控件右端浮件（密码显隐按钮等）。
   * 传入时外层包一个 .lc-input-wrap 供绝对定位；`id` / `className` / `style` 仍落到 <input> 本身，
   * 所以 Field 的 label→input 关联不受影响。
   */
  trailing?: ReactNode;
}

// 单行输入框
export function Input({ error = false, className = "", trailing, children, ...rest }: InputProps) {
  // children 必须显式摘掉：input 是 void 元素，若随 {...rest} 摊进去，
  // 运行期会抛「input is a void element tag and must neither have children」
  // （同 Checkbox 的坑，`InputHTMLAttributes` 自带 children 类型所以 TS 不拦）
  void children;
  const cls = ["lc-input", error ?"lc-input--error":"", className].filter(Boolean).join(" ");
  const input = <input className={cls} {...rest} />;
  if (!trailing) return input;
  return (
    <span className="lc-input-wrap">
      {input}
      <span className="lc-input-wrap__trailing">{trailing}</span>
    </span>
  );
}

// 多行文本域
export function Textarea({ className = "", ...rest }: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea className={`lc-textarea ${className}`.trim()} {...rest} />;
}

// 下拉框入参：选项列表 + 错误态（高度与 Input 同档）
export interface SelectProps extends SelectHTMLAttributes<HTMLSelectElement> {
  /** 展示值（胶囊文案）；原生 select 负责交互，样式走 .lc-select */
  display?: ReactNode;
}

/** 语种胶囊样式选择器（ZH → EN 之类）；选项用原生 select 保证可访问性 */
export function Select({ display, className = "", children, ...rest }: SelectProps) {
  return (
    <span className={`lc-select ${className}`.trim()} style={{ position:"relative"}}>
      <select
        {...rest}
        style={{
          appearance: "none",
          position: "absolute",
          inset: 0,
          width: "100%",
          height: "100%",
          opacity: 0,
          cursor: "pointer",
          border: 0,
        }}
      >
        {children}
      </select>
      {display}
      <CaretDownIcon size={12} />
    </span>
  );
}
