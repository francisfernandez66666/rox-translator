// ============ LoginFace.tsx · 职责说明 ============
// 登录面的**唯一调用点**（★ 2026-10-21 IAM 重做 P1-7「路由分诊合一」）。
//
// 背景：`App.tsx`（带 router 的主树）与 `PublicRoot.tsx`（未登录公开面树）都要出登录屏。
// 改造前两处各写一份 `<PublicShell auth><RouteBoundary><Suspense><Login …/><AiAssist />`，
// 于是同一件事有三份口径要记（mode 判定、onLogin 的 role 二次校验、挂件挂在哪一层）——
// 这正是「改一面留一面」的温床：改了 App 那一侧，访客走的却是 PublicRoot 那一侧，
// 表现为"刷新前一套、刷新后另一套"，而本地单测只看其中一棵树时全程绿。
// 现在两棵树都只调本组件，分支判定与登录后处理不再有第二份。
//
// 三条硬口径：
//  ① **必须留在公开面闭包允许的依赖里**：本文件只许引 `stores/auth`、`lib/nav`、
//     `components/publicshell`、`RouteBoundary`，以及懒加载的 `Login`／`AiAssist`。
//     引 `App.tsx`／`stores/admin`／`hooks/useChat` 一族会把 router 与后台面板拖回入口包
//     （体积回归只体现在产物里，源码/tsc/vitest 全绿，锁见 `lib/navShim.test.ts` B/C 段）。
//  ② **`applyLogin` 是登录成功后的唯一后处理**：它是"admin 档下非租管账号弹回首页"那条判定的
//     落点（`stores/auth.tsx`）。别在这里再手写一份 role 判断——两份迟早一把严一把松。
//  ③ **挂件挂在本组件里**：`AiAssist` 内部对 `/login`、`/register` 自隐藏；两棵树各挂一次即可，
//     调用方不要再补一份（重复挂载会在挂件可显示的路径上叠出两个入口）。
import { lazy, Suspense, type ReactNode } from 'react'
import { PublicShell } from './publicshell'
import RouteBoundary from './RouteBoundary'
import { applyLogin, useAuth } from '@/stores/auth'
import { useNav } from '@/lib/nav'

// 登录屏与挂件都按路径才需要：与两棵树原先的懒加载口径一致（共用同一份分片，不会多下一份）
const Login = lazy(() => import('./Login'))
// AiAssist 是官网常驻接待挂件，与登录屏同批懒加载，未登录路径均可挂载。
const AiAssist = lazy(() => import('./AiAssist'))

/**
 * 登录/注册屏（未登录兜底路径的唯一实现）。
 *
 * @param path     当前路径：只用来判 `admin` 形态（`/admin` 前缀 ⇒ 管理员登录面）
 * @param fallback 懒加载期间的占位：App 树给 `PageLoading`（换词动效），公开面树给 `BootLoading`
 *                 ——两棵树的加载视觉本来就不一样，这一项刻意由调用方给，不在共享件里二选一。
 */
export function LoginFace({ path, fallback }: { path: string; fallback: ReactNode }) {
  const { onLogin } = useAuth()
  const { navigate } = useNav()
  return (
    // 登录页不挂顶栏登录钮（用户就在这一页，再给一枚是自指重复；AGENTS「移动档语言钮+登录钮」）
    <PublicShell auth>
      <RouteBoundary>
        <Suspense fallback={fallback}>
          <Login
            mode={path.startsWith('/admin') ? 'admin' : 'home'}
            onLogin={(u) => applyLogin(u, onLogin, path, navigate)}
          />
          {/* 挂件内部对 /login、/register 自隐藏，其余未登录路径仍可接待 */}
          <AiAssist />
        </Suspense>
      </RouteBoundary>
    </PublicShell>
  )
}
