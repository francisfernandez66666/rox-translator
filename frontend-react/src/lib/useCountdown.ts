// ============================================================================
// lib/useCountdown.ts — 冷却倒计时钩子（★ #42 前端技术债：定时器清理收口）
//
// 为什么要收这一个口子（改动前的真实缺陷，勿当成「洁癖重构」）：
//   「发送验证码后 60s 内禁止重发」在 Login.tsx、modals.tsx（两处：改密弹窗 + 换邮箱弹窗）
//   各自手写了一份 `window.setInterval` + 回调里 self-clear 的倒计时。那种写法只解决了
//   「跑到 0 自己停」，却漏掉两件事：
//     1) **组件卸载时不清**：弹窗关掉/页面切走后定时器仍在跳，最长再打 59 次 setState，
//        对已卸载组件写状态（React 18 不再告警，所以这种泄漏极容易长期潜伏）；
//     2) **重复启动不互斥**：`setInterval` 的句柄只存在于回调闭包里，任何人二次触发
//        （按钮 disabled 有延迟、回车与点击并发、接口慢响应期间连点）就会并存两个定时器，
//        于是 60s 冷却实际 30s 就见底——用户看到「还能再发一次」，验证码接口被真实打到限流。
//   两处缺陷都不是靠「记得 clearInterval」能长期保证的，故统一到这里：
//   句柄放 ref（可互斥）、卸载即清（effect 返回 stop）、启动前先 stop（幂等）。
//
// 口径：只保证「同一时刻至多一个定时器 + 卸载后不再触发」，不接管业务侧的禁用条件
// （按钮还要自己判 left > 0），与各调用点改动前的可观察行为完全一致。
// ============================================================================
import { useCallback, useEffect, useRef, useState } from "react";

// 倒计时句柄：剩余秒数与格式化文案（验证码/订单共用）
export interface Countdown {
  /** 剩余秒数：0 表示冷却结束（按钮可再次点击） */
  left: number;
  /** 启动（或重新）倒计时；重复调用不会并存两个定时器 */
  start: () => void;
  /** 立即停止并清零（例如提交成功后不必等冷却走完） */
  stop: () => void;
}

/**
 * useCountdown 冷却倒计时。
 * 参数 seconds: 冷却总秒数（默认 60，与各调用点原有硬编码值一致）。
 * 返回 { left, start, stop }。
 */
export function useCountdown(seconds = 60): Countdown {
  const [left, setLeft] = useState(0);
  // 定时器句柄放 ref：闭包里留句柄是旧写法翻车的根因（外部无从清理，也就无法互斥）
  const iv = useRef<number | null>(null);

  const stop = useCallback(() => {
    if (iv.current !== null) {
      window.clearInterval(iv.current);
      iv.current = null;
    }
    setLeft(0);
  }, []);

  const start = useCallback(() => {
    // 幂等启动：先停旧的再开新的，杜绝「两个定时器同时递减同一份状态」
    if (iv.current !== null) window.clearInterval(iv.current);
    setLeft(seconds);
    iv.current = window.setInterval(() => {
      setLeft((c) => {
        if (c <= 1) {
          // 归零即停：句柄在 ref 里，这里能真正清掉（旧写法清的是闭包变量）
          if (iv.current !== null) {
            window.clearInterval(iv.current);
            iv.current = null;
          }
          return 0;
        }
        return c - 1;
      });
    }, 1000);
  }, [seconds]);

  // 卸载清理：弹窗关闭 / 路由切换后不再有任何 setState 打进来
  useEffect(() => stop, [stop]);

  return { left, start, stop };
}
