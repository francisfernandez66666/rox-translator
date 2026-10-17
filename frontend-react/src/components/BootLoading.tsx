// ============ components/BootLoading.tsx · 职责说明 ============
// 启动/页面加载占位：**与 index.html 里那段首屏骨架长得完全一样**（纯黑 + 同一枚转圈）。
//
// ★ 2026-10-17 首屏轻量化批 B：让「骨架 → React 占位 → 真界面」三段视觉连续。
//   为什么必须有它，而不是让 Suspense 的 fallback 给 null：
//   `index.html` 的启动哨兵判"界面起没起来"的判据是 **`#root` 里有没有子节点**
//   （`src/bootStorageGate.test.ts` 的启动链锁盯着这条），fallback=null 时懒加载的
//   App/PublicRoot 分片下载期间 `#root` 一直是空的 ⇒ 哨兵在截止时刻会把它判成
//   「页面没能加载起来」并**盖全屏错误面板**——而那一档其实是"正在正常下载"
//   （跨境慢链路要十几秒，正是本批要服务的场景）。所以挂载后第一帧就往 `#root` 里放这个占位。
//
// 三条口径：
//  ① **几何只有一个出处**：容器/转圈的尺寸、环宽、颜色、关键帧都在 `index.html` 那段 `<style>` 里
//     （`#__boot_skeleton, .lc-boot-cover` 与 `.lc-boot-spin`），本组件**只用类名、零内联几何**——
//     内联第二份的形态是"两处慢慢漂开，切换时闪一下"（同 AGENTS §一·5「界面规格＝交付真值，改一处漏一处」）。
//  ② **不许复用 `id="__boot_skeleton"`**：那是 index.html 骨架节点的 id，哨兵靠它撤节点；
//     React 再渲染一个同名 id，哨兵的 `removeChild` 会直接把 React 树里的节点撕掉
//     （React 不知情，后续更新可能抛 not a child）。这里只借类名。
//  ③ 纯展示、无文案（同首屏骨架：`aria-hidden`，加载语义由转圈承担；也**不订阅语言**——
//     无文案的组件没有随语种重渲染的需求）。
export default function BootLoading() {
  return (
    <div className="lc-boot-cover" aria-hidden="true">
      <span className="lc-boot-spin" />
    </div>
  )
}
