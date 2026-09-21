// ============ focusTrap.ts · 职责说明 ============
// 模态容器（Dialog / Drawer）的键盘焦点收口，解决评估报告 §四-a11y 指出的
// 「Dialog/Drawer 无 focus-trap」（全站 174 文件仅 111 处 aria-*，模态框只标了
// aria-modal 却没管住 Tab）。三件事：
//   1) **打开即入焦**：焦点从页面背景移进容器（否则键盘用户要 Tab 穿整页才够到对话框）；
//   2) **Tab 首尾环绕**：焦点在容器内循环，不泄漏到遮罩背后的背景内容——
//      背景在 aria-modal 语义下对读屏已隐藏，却能被 Tab 到，就是 WCAG 2.4.3 的硬伤
//      （用户会「掉进黑洞」：焦点在一个念不出来的按钮上）；
//   3) **关闭还焦**：焦点还给打开前的元素，否则掉回 <body>，键盘用户从头再 Tab。
//
// 与嵌套模态的关系（刻意设计，别简化成「document 上无条件拦 Tab」）：
//   本仓确有「抽屉里再开确认框」（TicketsPage / uiDialogs.confirmDialog 叠在 Drawer 上）。
//   若每个模态都在 document 上拦 Tab，外层会把内层刚拿到的焦点**抢回去**，
//   表现为「弹层的按钮按 Tab 没反应」。故维护一个模块级激活栈：
//   只有栈顶（最内层）的 trap 处理按键，其余让路。
//
// 不做的事：不给背景加 inert / aria-hidden。浏览器与 React 版本兼容性代价高，
// 且环绕 + 栈顶判定已经阻断泄漏路径；背景遮罩本身不可聚焦。
// =============================================
import { useEffect, type RefObject } from "react";

// 容器内可聚焦元素：显式排除 disabled 与 tabindex="-1"（后者是「程序化聚焦专用」，
// 不该进 Tab 环，如被隐藏的文件 input、只做 ref 目标的容器）。
export const FOCUSABLE_SELECTOR = [
  "a[href]",
  "button:not([disabled])",
  "input:not([disabled])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  '[tabindex]:not([tabindex="-1"])',
].join(",");

// trapStack 当前激活的 trap 盒（后进者为栈顶 = 最内层模态）。
// 用数组而非 Set：需要「最后打开者获得键盘权」这一栈语义。
const trapStack: HTMLElement[] = [];

/**
 * useFocusTrap 把一个已渲染的模态容器变成焦点闭环。
 * @param containerRef 容器元素 ref（role="dialog"/"alertdialog" 的那个节点）
 * @param open 容器是否处于打开态（false 时不注册任何监听，也不抢焦点）
 * 说明：不接管 Esc（各组件已有 onCancel/onClose 口径），只管 Tab 与入焦/还焦。
 */
export function useFocusTrap(containerRef: RefObject<HTMLElement | null>, open: boolean) {
  useEffect(() => {
    if (!open) return;
    const box = containerRef.current;
    if (!box) return;

    const opener = document.activeElement as HTMLElement | null;
    trapStack.push(box);

    const focusables = (): HTMLElement[] =>
      Array.from(box.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR));

    // 打开即入焦：焦点已在容器内（组件自己 ref.focus() 的场景，如 Dialog 落「取消」）就不动它
    if (!box.contains(document.activeElement as Node | null)) {
      (focusables()[0] ?? box).focus();
    }

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== "Tab" || trapStack[trapStack.length - 1] !== box) return; // 让位给更内层的模态
      const list = focusables();
      if (list.length === 0) {
        e.preventDefault();
        box.focus(); // 空容器：焦点钉在对话框本身，不让它顺着背景跑掉
        return;
      }
      const first = list[0];
      const last = list[list.length - 1];
      const active = document.activeElement as HTMLElement | null;
      const outside = !active || !box.contains(active);
      // 焦点已在背景（点击遮罩后方内容、或程序移动过）⇒ 按方向拉回容器边界
      if (e.shiftKey && (outside || active === first)) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && (outside || active === last)) {
        e.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKeyDown, true);

    return () => {
      document.removeEventListener("keydown", onKeyDown, true);
      const i = trapStack.indexOf(box); // 按盒摘除（不用 pop：多个模态可能非严格逆序卸载）
      if (i >= 0) trapStack.splice(i, 1);
      if (opener && typeof opener.focus === "function" && document.contains(opener)) {
        opener.focus(); // 还焦：关闭后键盘用户回到「刚才那个按钮」，而不是从头 Tab
      }
    };
  }, [open, containerRef]);
}
