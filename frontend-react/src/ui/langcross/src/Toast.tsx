import { createContext, useCallback, useContext, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { createPortal } from "react-dom";
import { ToastErrorIcon, ToastSuccessIcon, ToastWarnIcon } from "./icons";

// 轻提示语气档
export type ToastTone ="success"|"error"|"warn";

// 轻提示入参：标题/描述/语气/自动消失时长
export interface ToastOptions {
  title: ReactNode;
  desc?: ReactNode;
  tone?: ToastTone;
  /** 停留毫秒数，默认 4200 */
  duration?: number;
}

// 队列内一条提示（带 id 供撤销与去重）
interface ToastItem extends ToastOptions {
  id: number;
}

// Toast 上下文：push/dismiss 两个动作
interface ToastContextValue {
  toast: (options: ToastOptions) => void;
}

// Toast 上下文实例（组件必须在 ToastProvider 下渲染）
const ToastContext = createContext<ToastContextValue | null>(null);

/** 堆叠上限：规范「最多 3 条」，超出时挤掉最旧的 */
const MAX_VISIBLE = 3;

// 语气档 → 图标映射
const TONE_ICON: Record<ToastTone, (props: { size?: number }) => ReactNode> = {
  success: (p) => <ToastSuccessIcon {...p} style={{ color:"var(--lc-success)", flex:"none"}} />,
  error: (p) => <ToastErrorIcon {...p} style={{ color:"var(--lc-danger)", flex:"none"}} />,
  warn: (p) => <ToastWarnIcon {...p} style={{ color:"var(--lc-warn)", flex:"none"}} />,
};

// ToastProvider 入参：children（右上角最多 3 条）
export interface ToastProviderProps {
  children: ReactNode;
}

/** 包在应用根部一次；之后任意组件里 useToast().toast({...}) */
export function ToastProvider({ children }: ToastProviderProps) {
  const [items, setItems] = useState<ToastItem[]>([]);
  const seq = useRef(0);

  const toast = useCallback((options: ToastOptions) => {
    const id = ++seq.current;
    setItems((prev) => [...prev.slice(-(MAX_VISIBLE - 1)), { ...options, id }]);
    const duration = options.duration ?? 4200;
    window.setTimeout(() => {
      setItems((prev) => prev.filter((t) => t.id !== id));
    }, duration);
  }, []);

  const value = useMemo(() => ({ toast }), [toast]);

  return (
    <ToastContext.Provider value={value}>
      {children}
      {createPortal(
        <div className="lc-toast-viewport"aria-live="polite">
          {items.map((t) => {
            const Icon = TONE_ICON[t.tone ?? "success"];
            return (
              <div key={t.id} className="lc-toast"role="status">
                <Icon />
                <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: 3 }}>
                  <span className="lc-toast__title">{t.title}</span>
                  {t.desc ? <span className="lc-toast__desc">{t.desc}</span> : null}
                </div>
              </div>
            );
          })}
        </div>,
        document.body,
      )}
    </ToastContext.Provider>
  );
}

// 取 Toast 句柄；脱离 Provider 时直接抛错，避免静默丢提示
export function useToast(): ToastContextValue {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error("useToast 必须在 <ToastProvider> 内使用");
  return ctx;
}
