// ============================================================================
// main.tsx — 应用入口
// 职责：创建 React 18 根节点，引入全局样式，渲染 <App />。
//       并在最外层挂 LangCross <ToastProvider> + ToastBridge + DialogHost，
//       使全站提示/弹窗脱离 TDesign 的 MessagePlugin / Dialog 调用方式。
// ============================================================================

/**
 * main.tsx · 职责说明
 * 应用入口文件，负责：
 * - 创建 React 18 根节点
 * - 引入全局样式（项目主题 → LangCross 令牌/组件/动效 → 移动端）
 * - 挂载 LangCross 全局提示容器 ToastProvider，并挂上 ToastBridge / DialogHost
 * - 渲染 App 组件到 DOM 节点
 *
 * 样式引入次序（2026-09-17 换肤后确定的口径）：
 * 1) theme.css：只改「变量值」不改结构——把 --td-* / --npz-* / --adm-* 三套语义变量
 *    整体重定义到纯黑色值，从而一次覆盖全站约 130 处 tsx 内联样式；
 * 2) tokens.css：--lc-* 令牌的唯一来源（theme.css 里的动效类会引用它，CSS 变量取值
 *    与声明先后无关，故两者不存在顺序冲突）；
 * 3) components.css：.lc-* 组件结构样式；
 * 4) motion.css：.lc-mo-* 动效工具类——必须晚于 components.css，同权重下才能
 *    「只做叠加、不改外观」；
 * 5) mobile.css 最上层：窄屏断点覆写。
 *
 * ToastBridge / DialogHost 挂在 ToastProvider 内部：前者把 lib 层 toastBus 接回
 * useToast（供非组件环境 import { toastError } 使用），后者承接 confirmDialog 等
 * 命令式弹窗出口——两者都依赖 Provider 上下文，故不能放到 App 之外。
 */

import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
// LangCross 纯黑组件库（@langcross/ui）
import { ToastProvider } from './ui/langcross/src'
// ToastBridge：把 lib/toastBus 的命令式 toast 转接到 ToastProvider（非组件环境也能提示）
import ToastBridge from './components/ToastBridge'
// DialogHost：confirmDialog / promptText 等命令式弹窗的挂载点（取代 TDesign Dialog 调用）
import { DialogHost } from './components/uiDialogs'

// 项目主题覆盖样式（变量已重映射到 --lc-* 纯黑单色令牌）
import './styles/theme.css'
// LangCross 设计令牌（唯一来源，必须先于组件样式引入）
import './ui/langcross/css/tokens.css'
// LangCross 组件样式（.lc-* 前缀，与画布「00 · 设计令牌」一一对应）
import './ui/langcross/css/components.css'
// LangCross 动效层（.lc-mo-* 工具类 + 关键帧，对应「纯黑 UI 动效十原则」）
// 必须晚于 components.css：同权重下后者可见，动效层只做叠加不改外观
import './ui/langcross/css/motion.css'
// 移动端 / 窄屏自适应样式（叠加在最上层）
import './styles/mobile.css'

// 挂载应用到 DOM 节点，并开启严格模式
// ToastProvider：@langcross/ui 全局提示容器，等价替换 TDesign 的 MessagePlugin
ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ToastProvider>
      <ToastBridge />
      <DialogHost />
    <App />
    </ToastProvider>
  </React.StrictMode>,
)
