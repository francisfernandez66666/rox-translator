// ============================================================================
// components/BrandTagline.tsx — ★ 页脚品宣尾注「Powered by LangCross AI」唯一事实源
// （2026-10-13 用户令②：「在所有有页脚的页面，页脚位置写一条：Powered by LangCross AI，
//   来加重品宣和 ai 赋能价值，重点提醒。」）
//
// 三条口径，改这一腿前必读：
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
//
// RTL 说明：阿语等 RTL 语种下，纯拉丁短语在 RTL 段落里会被双向算法重排标点与顺序，
// 故组件自带 dir="ltr" + isolate（隔离而非覆盖整行方向，前后的「·」分隔符仍随页面方向）。
// ============================================================================
import { CSSProperties } from 'react'

/** 页脚品宣尾注的字面量（唯一事实源；不许本地化、不许替换成租户品牌名） */
export const BRAND_TAGLINE = 'Powered by LangCross AI'

/** 默认样式：不指定颜色与字号——继承所在页脚那一行的档位（五张面的页脚字号档本就不同，
 *  统一抬档会把落地页 15px 的版权行与报价页 14px 的页脚拉成同一个尺寸，属未授权的规格改动）。
 *  只固定两件事：LTR 方向隔离 + 不换行（品牌名被劈成两行就不成品宣了）。 */
const TAGLINE_STYLE: CSSProperties = { unicodeBidi: 'isolate', whiteSpace: 'nowrap' }

/**
 * 页脚品宣尾注组件。
 * 调用方负责分隔符（`·`）与容器，本组件只出那一句固定文字，
 * 这样后端直出面（Go 字符串）与 React 面的判据可以是同一条字面量。
 */
export function BrandTagline({ style }: { style?: CSSProperties }) {
  return (
    <span dir="ltr" data-testid="brand-tagline" style={{ ...TAGLINE_STYLE, ...style }}>
      {BRAND_TAGLINE}
    </span>
  )
}
