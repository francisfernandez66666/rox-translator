// ============================================================================
// lib/siteFlags.ts — 站点门面标记（★ 〇-Z：演示站不进官网主页）
//
// 数据来源：后端 `spa.go serveIndexHTML` 在**主页对访客关闭**时，往入口 HTML 的
//   </head> 前注入 `<script id="__site_flags__">window.__SITE_FLAGS__={"landing":false};</script>`；
//   主页开放时**一段都不注入**（主站首页 2,591 B 是钉死的发版判据，见《部署指南》§十）。
//
// 为什么不是前端写死域名：演示站与主站共用同一份 dist，按 hostname 判定等于把
//   「哪个站是体验机」编译进构建产物——换域名要重构建，且本地任何闸门都看不见这条分支。
//   走后端直出后，差异只落在演示单元的一个环境变量（LANDING_DISABLED）上，
//   且这一层是**可 curl 验证**的（发版冒烟脚本判据 H）。
//
// 缺省口径：读不到标记一律按「主页开放」处理。这与后端同向——
//   误把主站首页关掉（对外营业的门面）比演示站多显示一页严重得多。
//   同理，标记存在但 landing 不是布尔时按缺省走，不做「非 false 即 true」之外的猜测。
// ============================================================================

/** 后端直出的站点门面标记形态（新增因子时在此扩字段，别另开全局变量） */
export interface SiteFlags {
  /** 官网主页对未登录访客是否开放；false＝打开 `/` 直接进登录注册页 */
  landing?: boolean
}

declare global {
  interface Window {
    /** 站点门面标记；仅当有因子偏离默认档时由后端注入（缺省＝未注入＝全默认） */
    __SITE_FLAGS__?: SiteFlags
  }
}

/**
 * 读取当前部署的站点门面标记。
 * SSR／测试环境无 window 时返回空对象（＝全默认档），不抛错。
 */
export function readSiteFlags(): SiteFlags {
  if (typeof window === 'undefined') return {}
  const raw = window.__SITE_FLAGS__
  // 只接受朴素对象；null／数组／字符串形态（被浏览器插件或半截脚本污染）一律按未表态
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return {}
  return raw as SiteFlags
}

/**
 * 官网主页对未登录访客是否开放（★ 〇-Z 的唯一消费点判据）。
 * 返回 false 时，App.tsx 的访客分流把 `/` 重定向到 `/login`（地址栏跟着变）。
 */
export function landingEnabledForGuest(): boolean {
  const f = readSiteFlags()
  return f.landing !== false
}
