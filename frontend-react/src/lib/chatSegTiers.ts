// ============ 本文件职责中文说明 ============
// ★ 2026-11-01 DEF-P1-05 B2（即时翻译段落能力）两条运行期档位的
//   **字段名、配置键、合法区间与代码缺省**的唯一前端出处（形态照 src/lib/textLineTiers.ts）。
//
// 为什么单独一个纯模块（而不是放在 src/api/billing.ts 里）：
// 管理台各面板的测试夹具都按枚举 mock @/api 的表面；常量一旦也从那个 barrel 走，
// 每加一个档位就要同时改 N 份 mock——漏一处的表现不是报错，而是整块表单渲染崩。
// 纯模块不经过任何 mock，界面读到的就是产品真正在用的那份区间。
//
// ★★ 这一族与逐行翻译那族**判据方向相反**，改之前先看清：
//   `per_paragraph` 与 `max_points` 的 **0 都是真实档位**（0＝关掉分段走整篇／0＝不设积分上限），
//   所以下界是 0 而不是 1。等值由 src/lib/chatSegTiers.crossSide.lock.test.ts 三方钉住
//   （前端三份表 ⇔ api/chat_seg_config.go 的 chatSegCfgFields ⇔ engine 那两个常量与钳位值）。
//   谁把这里改成 [1,N]，就是把"关掉分段"这颗闸从运营手里拿走——那条改法由锁当场判红，不靠注释。
// ============================================

/** 两条档位的请求体字段名（顺序即表单展示顺序，也是"两项必须一次交齐"的判据来源） */
export const CHAT_SEG_TIER_FIELDS = [
  "per_paragraph",
  "max_points",
] as const;

/** ChatSegTierField：档位字段名的联合类型，与 CHAT_SEG_TIER_FIELDS 逐键同源 */
export type ChatSegTierField = (typeof CHAT_SEG_TIER_FIELDS)[number];

/** 字段名 → system_config 键名（界面要回显"库里存的那一份"，两侧必须同源于这一处映射） */
export const CHAT_SEG_TIER_CONFIG_KEYS: Record<ChatSegTierField, string> = {
  per_paragraph: "text_chat_per_paragraph",
  max_points: "chat_segment_max_points",
};

/** 合法区间（含端点）。与后端白名单逐字相等——见文件头那条三方锁。⚠️ 0 在本族是合法档 */
export const CHAT_SEG_TIER_RANGES: Record<
  ChatSegTierField,
  [number, number]
> = {
  per_paragraph: [0, 1],
  max_points: [0, 100000],
};

/** 代码缺省档（与后端 chatSegCfgFields.def 同值；界面提示与夹具共用，避免各处手写数字） */
export const CHAT_SEG_TIER_DEFAULTS: Record<ChatSegTierField, number> = {
  per_paragraph: 1,
  max_points: 60,
};
