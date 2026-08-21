// ============================================================================
// main.ts — Vue 应用入口
// ============================================================================
// 【作用】创建 Vue 应用实例，挂载 Pinia 状态管理和路由
// 这是整个前端最先执行的文件

// Vue 框架核心（创建应用实例）
import { createApp } from 'vue'
// Pinia 状态管理（创建 store 实例）
import { createPinia } from 'pinia'
// 根组件
import App from './App.vue'

// 创建 Vue 应用
const app = createApp(App)

// 全局错误显形：任何组件崩溃/未捕获异常直接显示在页面底部（便于线上定位白板问题）
function showErrorBox(title: string, detail: unknown) {
  try {
    const box = document.createElement('pre')
    box.style.cssText = 'position:fixed;bottom:0;left:0;right:0;max-height:45%;overflow:auto;background:#fff0f0;color:#b00020;padding:12px;font-size:12px;line-height:1.5;z-index:99999;white-space:pre-wrap;border-top:2px solid #b00020;'
    box.textContent = `[${title}]\n${String(detail)}`
    document.body.appendChild(box)
  } catch {}
}
app.config.errorHandler = (err, _instance, info) => {
  console.error('[Vue Error]', err, info)
  showErrorBox('Vue Error', `${(err as Error)?.stack || err}\n\nhook: ${info}`)
}
window.addEventListener('error', (e) => {
  if (e.error) showErrorBox('Uncaught Error', e.error?.stack || e.message)
})
window.addEventListener('unhandledrejection', (e) => {
  showErrorBox('Unhandled Rejection', e.reason?.stack || e.reason)
})

// 挂载 Pinia 状态管理（全局状态，所有组件共享）
app.use(createPinia())

// 挂载到 HTML 的 #app 元素
app.mount('#app')
