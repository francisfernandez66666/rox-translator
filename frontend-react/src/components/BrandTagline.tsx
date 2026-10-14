// ============================================================================
// components/BrandTagline.tsx — ★ 页脚品宣尾注「Powered by LangCross AI」唯一事实源
// （2026-10-13 用户令②：「在所有有页脚的页面，页脚位置写一条：Powered by LangCross AI，
//   来加重品宣和 ai 赋能价值，重点提醒。」）
//
// 四条口径，改这一腿前必读：
// ① **不分语种、不本地化**：这一行是**品牌标识**（与「LangCross」这个名字本身同级），
//    不是界面文案。因此它**刻意不进 i18n 词典**——十二份 locale 全量词典
//    （`locales.core.test.ts` 按 ALL_KEYS 长度逐键红灯拦截）里不许出现这一条键，
//    同步十二份译文的代价换不来任何品宣收益：访客认的是那串拉丁字母标识，
//    翻成「由能言 AI 驱动」反而把品牌名拆成两套写法（同 i18n/brandName.test.ts 那条
//    「不许混用汉字品牌名」的老坑）。
// ② **不随租户品牌改名**：租户白标（F-75）改的是「这个客户的面板叫什么」，
//    而这一行说的是「底下跑的是谁的引擎」——平台方的品宣位，与 brandName 无关，
//    所以这里**不接 {brand} 插值**，也不许改成 `Powered by {brand} AI`。
// ③ **只此一个出处**：五张渲染面（全站页脚 SiteFooter / 落地页 / 报价页 / 对照页 /
//    后端直出 /docs/*）一律引用本常量，禁止在组件里再抄一遍字面量——
//    等值锁＝`BrandTagline.lock.test.ts`（含「删掉任一面即红」的反证）。
// ④ ★ **视觉档也只有一个出处**（2026-10-14 用户后令「提亮＋重点提示」时补立）：
//    面色/字色/字重/圆角/内垫一律写在下面那份**导出的** `TAGLINE_STYLE` 里，四面调用**不带 style**；
//    后端直出面（Go 那份 CSS）拿不到组件，只能同值重声明 `.footer .brand-tagline{…}`，
//    等值由 `BrandTagline.lock.test.ts` G1–G5 钉住（**按 var() 解析后的实值逐维比，两侧各自的令牌表**，
//    不是比令牌名——比名字恰好放过「名字照抄、值没落地」那一族静默失效：直出面 :root 只有 --lc-white）。
//    理由：这一族原本四面各内联一份 `fontWeight: 600`，改「提亮」就得同时改五处，
//    而「改四面留一面」正是本仓页脚链接／手册入口那批反复出现过的漂移形态。
//
// RTL 说明：阿语等 RTL 语种下，纯拉丁短语在 RTL 段落里会被双向算法重排标点与顺序，
// 故组件自带 dir="ltr" + isolate（隔离而非覆盖整行方向，前后的「·」分隔符仍随页面方向）。
// ============================================================================
import { CSSProperties } from 'react'

/** 页脚品宣尾注的字面量（唯一事实源；不许本地化、不许替换成租户品牌名） */
export const BRAND_TAGLINE = 'Powered by LangCross AI'

/** ★ 2026-10-14 用户后令「这个页脚的句子能不能提亮并且做重点提示」⇒ 从「继承页脚灰字」升为**白底徽标档**。
 *
 * 四个维度全部取交付既有令牌，**零新增色值**（这是本仓 §一·5「界面规格＝UI 交付真值」的硬要求：
 * 想要新形状只能从既有档位里挑，不能自己调一个十六进制）：
 *   面色 `var(--lc-fill-white)` ＝ #FFFFFF —— 实心白填充档，主按钮／「注册即送」徽标／白底 CTA 同档；
 *   字色 `var(--lc-invert-fg)` ＝ #000000 —— 反相面上的文字（白底必须配黑字，配 #E7E9EA 会显脏）；
 *   字重 600 —— 徽标档；圆角 999px ＝ 全站胶囊档（components.css `.lc-btn--pill` 同值）；
 *   内垫 `1px 8px` ＝ theme.css `.draft-stage` 那一族小胶囊的同值档。
 *
 * ⚠️ **字号与行高不指定**：字号继承所在页脚那一行（五张面的页脚字号档本就不同：全站页脚 15px、
 *    报价/对照页 14px），统一抬档属未授权的规格改动；行高不写是为了不给五张面各造一个新档。
 * ⚠️ 这一档由**等值锁**钉死（`BrandTagline.lock.test.ts` G1 跨语言逐维等值＋G2 交付真值点名＋
 *    G3 四面不带 style 负向＋G4 五枚反证＋G5 直出面在位性与令牌在位；另有
 *    `BrandTagline.dom.test.tsx` ⑤ 证明这一身确实落到渲染节点、`readability.test.ts` G 段管白档不许退成文字灰）——
 *    **禁止**把它写成「只许更亮／更大」的单向锁：历史上三批提亮（09-18 令牌批、#35、#67/#68）
 *    正是被单向锁一路推离交付稿，返工成本＝全站 50 文件。
 *    要改这一档，先改 UI-ANNOTATIONS §6「用户后令覆盖记录」里尾注那一行的真值，再同步上面那几条锁。 */
// ★ 导出给闸门按**值**比对（不是正则扫源码）：BrandTagline.lock.test.ts 的 G 段拿这份对象
//    与后端直出面那条 CSS 规则逐维等值，两侧谁改谁红。组件本体继续吃同一份，绝无第二处。
export const TAGLINE_STYLE: CSSProperties = {
  unicodeBidi: 'isolate',
  whiteSpace: 'nowrap',
  display: 'inline-block',
  background: 'var(--lc-fill-white)',
  color: 'var(--lc-invert-fg)',
  fontWeight: 600,
  borderRadius: '999px',
  padding: '1px 8px',
}

/**
 * 页脚品宣尾注组件。
 * 调用方负责分隔符（`·`）与容器，本组件只出那一句固定文字**与它的那一身样式**，
 * 这样后端直出面（Go 字符串）与 React 面的判据可以是同一条字面量，
 * 而「提亮/重点提示」这一档也只有一个写点（★ 2026-10-14：四面原先各内联一份 `fontWeight: 600`，
 * 已收回组件里——五张面里改四处留一处的漂移形态就是从这里长出来的，G 段把它钉成负向锁）。
 */
export function BrandTagline({ style }: { style?: CSSProperties }) {
  return (
    <span dir="ltr" data-testid="brand-tagline" style={{ ...TAGLINE_STYLE, ...style }}>
      {BRAND_TAGLINE}
    </span>
  )
}
