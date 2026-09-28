// ============================================================================
// lib/trialDevice.ts — 免登录试用的「浏览器设备号」（★ 〇-Z，2026-09-28）
//
// 为什么需要它：试用面（POST /api/trial/translate）是匿名的，后端没有 user_id 可记账，
// 于是「每台浏览器 5 句」这条额度必须有一个**客户端生成、服务端只消费**的标识。
// 口径：
//   1. 只做设备标识，不做身份：里面不含 IP、UA、时间戳，也不与任何账号关联，
//      所以清 Cookie / 隐私模式都追不到人，符合「试用」这一场景的最小收集原则；
//   2. 落 localStorage 而非 sessionStorage：刷新、关标签页再回来仍是同一台设备，
//      否则「按 F5 就重置 5 句」等于没有额度；
//   3. 字符集必须与服务端 trialDeviceIDRe（^[A-Za-z0-9_-]{8,64}$）一致：
//      这个串会直接当限流表的 key 片段，格式不合就是 400，前端拿不到译文还说不清原因；
//   4. localStorage 不可用（无痕模式禁用存储、配额满）时**回落到会话内存值**：
//      宁可刷新后换一个新号（少送几次额度），也不能因为取不到设备号就把试用入口点死。
//
// ★ F-81（2026-09-28）：同一个设备号现在也随**自助注册**一起上报（api/auth.ts 的 authRegister 出口兜上），
//   后端用它记「同一浏览器每日新建免费账号」那一档防薅账（rate_limits 的 reg_dev）。
//   刻意复用同一份标识而不是再造一个 reg_device 键：「先刷 5 句试用、再批量注册领免费额度」这类连号行为
//   只有在一本账上才看得见；键名改动等于给所有历史浏览器重置额度，所以这里不再新增第二个标识。
// ============================================================================

/** storage 键名：改名等于给老访客重置额度，发布后不要再动 */
const KEY = 'lc_trial_device'

/** 随机设备号长度（字节）：16 字节 → base64url 22 字符，落在服务端 8~64 位区间正中 */
const BYTES = 16

// 存储不可用时的进程内回落值：同一次会话内稳定，刷新即换（见文件头第 4 条取舍）
let memoryFallback = ''

/** base64url 编码：去掉 +/= 这三个 URL 与正则都不友好的字符，与服务端白名单字符集对齐 */
function base64url(bytes: Uint8Array): string {
  let bin = ''
  bytes.forEach((b) => { bin += String.fromCharCode(b) })
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

/** 生成一份新的设备号（crypto 不可用的老浏览器回落到 Math.random，格式仍合规） */
function randomID(): string {
  try {
    const buf = new Uint8Array(BYTES)
    crypto.getRandomValues(buf)
    return base64url(buf)
  } catch {
    // 极端环境（无 crypto）：拼两段 Math.random 的 36 进制，长度与字符集同样合规
    return (Math.random().toString(36).slice(2) + Math.random().toString(36).slice(2)).slice(0, 22)
  }
}

/**
 * trialDeviceID 取本机试用设备号：有则复用，无则生成并持久化。
 * 返回值恒满足服务端格式校验（8~64 位 [A-Za-z0-9_-]）。
 */
export function trialDeviceID(): string {
  try {
    const cur = window.localStorage.getItem(KEY)
    // 复用时再验一次格式：手改存储、历史版本遗留的非法值都可能把请求顶成 400，
    // 与其让访客看到「试用标识不合法」，不如就地换一个新号
    if (cur && /^[A-Za-z0-9_-]{8,64}$/.test(cur)) return cur
    const next = randomID()
    window.localStorage.setItem(KEY, next)
    return next
  } catch {
    // 存储不可用：会话内复用同一个回落值，保证本页面生命周期内额度是连续的
    if (!memoryFallback) memoryFallback = randomID()
    return memoryFallback
  }
}
