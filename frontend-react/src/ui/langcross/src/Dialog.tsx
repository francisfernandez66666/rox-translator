// ============ Dialog.tsx · 职责说明 ============
// langcross 设计系统的模态对话框基件：全站确认框、条目表单弹窗（含后台 AI 助手面板）
// 都由它渲染，统一 role="alertdialog" + aria-modal，故 Playwright 用 getByRole('alertdialog')
// 定位、用 Label 取字段——新增调用点不要再自造遮罩层，否则焦点陷阱/Esc 语义会分叉。
// 关闭语义固定走 onCancel（Esc 与遮罩点击同一路径），确认按钮不自动关框（由调用方控制），
// 这是「确认失败留在框内可重试」的产品口径，改动会让用户丢输入。
// =============================================
import { useEffect, useRef } from "react";
import type { ReactNode } from "react";
import { createPortal } from "react-dom";
import { Button } from "./Button";
import { useFocusTrap } from "./focusTrap";

// 弹窗入参：标题/内容/动作按钮（默认宽 440，见交付真值 §4）
export interface DialogProps {
  open: boolean;
  title: ReactNode;
  children?: ReactNode;
  /** 危险操作：整框描边 #402323，确认按钮自动变红底 */
  danger?: boolean;
  confirmText?: ReactNode;
  cancelText?: ReactNode;
  onConfirm?: () => void;
  onCancel?: () => void;
  /** 点遮罩是否关闭，默认 true */
  dismissOnOverlay?: boolean;
}

/**
 * 对话框：440 宽 / 圆角 14 / 遮罩 72% 黑 / 顶缘高光。
 * Esc 与遮罩点击走 onCancel；确认不自动关——由调用方在 onConfirm 里控制。
 * ★ a11y（#43）：Tab 焦点由 useFocusTrap 收在框内循环，关闭后还给打开前的元素。
 */
export function Dialog({
  open,
  title,
  children,
  danger = false,
  confirmText = "确定",
  cancelText = "取消",
  onConfirm,
  onCancel,
  dismissOnOverlay = true,
}: DialogProps) {
  const cancelRef = useRef<HTMLButtonElement>(null);
  const boxRef = useRef<HTMLDivElement>(null);
  useFocusTrap(boxRef, open);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCancel?.();
    };
    document.addEventListener("keydown", onKey);
    cancelRef.current?.focus();
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onCancel]);

  if (!open) return null;

  return createPortal(
    <div
      className="lc-overlay"
      onMouseDown={(e) => {
        if (dismissOnOverlay && e.target === e.currentTarget) onCancel?.();
      }}
    >
      <div
        ref={boxRef}
        className={`lc-dialog ${danger ? "lc-dialog--danger" : ""}`.trim()}
        role="alertdialog"
        aria-modal="true"
      >
        <div className="lc-dialog__title">{title}</div>
        {children ? <div className="lc-dialog__body">{children}</div> : null}
        <div className="lc-dialog__actions">
          <button
            ref={cancelRef}
            type="button"
            className="lc-btn lc-btn--secondary"
            style={{ padding: "8px 16px", fontSize: 15 }}
            onClick={onCancel}
          >
            {cancelText}
          </button>
          <Button variant={danger ? "danger" : "primary"} onClick={onConfirm}>
            {confirmText}
          </Button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
