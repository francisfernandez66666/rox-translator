// ============ 本文件职责中文说明 ============
// 2026-10-26 用户追加裁决九-4：逐行翻译两条档位（补译轮数／上游断早停连续数）的
// **字段名与合法区间**唯一前端出处。
//
// 为什么单独一个纯模块（而不是放在 src/api/billing.ts 里）：
// 管理台各面板的测试夹具都按枚举 mock @/api 的表面；常量一旦也从那个 barrel 走，
// 每加一个档位就要同时改 N 份 mock——漏一处的表现不是报错，而是整块表单渲染崩。
// 纯模块不经过任何 mock，界面读到的就是产品真正在用的那份区间。
//
// ★ 区间不是前端自己拍的：[1,5] 与 [2,10] 必须与后端 textLineCfgFields 的 lo/hi
//   以及读侧钳位（engine.LineRetryMax／LineDeadStreakMin）逐字相等；等值由
//   PlansP.textline.dom.test.tsx 的跨侧锁机械钉住（改一侧必红）。
//   这一族**没有 0 档语义**：0＝一枪都不打／一行失败就打死整语种，前后端都不收。
// ============================================

/** 两条档位的请求体字段名（顺序即表单展示顺序，也是“两项必须一次交齐”的判据来源） */
export const TEXT_LINE_TIER_FIELDS = [
  "retry_max",
  "upstream_dead_streak",
] as const;

/** TextLineTierField：档位字段名的联合类型，与 TEXT_LINE_TIER_FIELDS 逐键同源 */
export type TextLineTierField = (typeof TEXT_LINE_TIER_FIELDS)[number];

/** 字段名 → system_config 键名（界面要回显“库里存的那一份”，两侧必须同源于这一处映射） */
export const TEXT_LINE_TIER_CONFIG_KEYS: Record<TextLineTierField, string> = {
  retry_max: "text_line_retry_max",
  upstream_dead_streak: "text_line_upstream_dead_streak",
};

/** 合法区间（含端点）。与后端白名单逐字相等——见文件头那条跨侧锁 */
export const TEXT_LINE_TIER_RANGES: Record<
  TextLineTierField,
  [number, number]
> = {
  retry_max: [1, 5],
  upstream_dead_streak: [2, 10],
};

/** 代码缺省档（与后端 textLineCfgFields.def 同值；界面提示与夹具共用，避免各处手写数字） */
export const TEXT_LINE_TIER_DEFAULTS: Record<TextLineTierField, number> = {
  retry_max: 3,
  upstream_dead_streak: 3,
};
