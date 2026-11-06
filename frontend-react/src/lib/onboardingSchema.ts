// ============ lib/onboardingSchema.ts · 职责说明 ============
// O 组（2026-11-06 用户令「注册登录后识别没产品就引导」）的**步骤事实源**：
// 纯数据＋纯函数，不含 UI、不含 fetch、不含 JSX。
//
// 为什么做成配置驱动（用户当场拍的形态，同时结掉待裁 24）：
//   三面归一（跨境对外／中文私域／门店现场）共用一台引擎是既定方向，引导步骤天然不同。
//   写死在组件里＝每个场景各抄一份向导，抄到第三份时字段名、落库口、完成判据必然分叉
//   ——那正是本仓「同族缺陷在多个面各写一遍」反复付学费的形态。
//
// ★ 三条硬口径（改这个文件前必读）：
//  ① 每个步骤的 `write` 必须是一张**真实存在的写口**（writeTargets 那份名单）。
//     配置驱动的坏法不是编译错，而是"配置写了、口不存在"⇒ 界面上一颗点了 404 的按钮，
//     而 jsdom 单测全 mock 掉 api 层看不见。守卫＝src/lib/onboardingSchema.lock.test.ts（按全集判）。
//  ② 步骤的完成判据必须是**可从服务端现读的事实**（有没有产品、语种是否非空），
//     不许用"前端记自己走没走过"的会话态当判据——那会把"读失败"读成"没走过"，
//     把老用户一次次推进向导（同 §一·5「空态不许伪装成检查过」那一族）。
//  ③ 目标语言**只存产品行**（用户裁定）：本文件不出现任何"用户默认语种/租户默认语种"的写口。
//     未声明＝空数组＝界面显示"未设定"，禁止退化成"跟随访客界面语种"（那是系统替客户决定对外说法）。
//
// ========================================

/** 字段控件类型：文本／多行／语种多选／开关 */
export type OnboardFieldKind = 'text' | 'textarea' | 'langs' | 'toggle'

/** 一个采集字段（labelKey/hintKey 全部走 12 语种词典，不许在这里内联中文） */
export type OnboardField = {
  /** 收集后的键名，直接作为写口入参的键 */
  key: string
  kind: OnboardFieldKind
  /** 词典键（label） */
  labelKey: string
  /** 词典键（可选说明） */
  hintKey?: string
  /** 必填：空值不许进步骤 */
  required?: boolean
  /** 语种多选的候选集档位（只有 langs 用得上） */
  maxPick?: number
}

/** 写口标识：**只能取下面那张真值表里的值**（否则锁判红） */
export type OnboardWriteTarget = 'product.profile' | 'product.langs' | 'product.seo' | 'skip'

/** 一步 */
export type OnboardStep = {
  id: string
  titleKey: string
  descKey: string
  fields: OnboardField[]
  write: OnboardWriteTarget
}

/** 场景码：三面各一份步骤子集（缺省 crossborder＝今天的对外场景） */
export type OnboardScene = 'crossborder' | 'privatecrm' | 'storefront'

/**
 * writeTargets 写口真值表（**唯一一份**，锁按它判"配置里不许出现不存在的口"）。
 * 每一项都注明实际调用的 api 函数名，改 api 时这里必须同步，否则锁红在"口不存在"上。
 */
export const WRITE_TARGETS: Record<OnboardWriteTarget, string> = {
  'product.profile': 'productSave',
  'product.langs': 'productSave',
  'product.seo': 'seoSet',
  // skip ＝ 这一步不落库，只给下一步当上下文（例：行业/公司在注册时已采集，这里只回显确认）
  skip: '',
}

/** 站点实际支持的对外语种（与后端 hreflang 码表同源的那一份档位，不在这里造第二份清单） */
export const LANG_OPTIONS = ['en', 'ja', 'ko', 'de', 'fr', 'es', 'pt', 'ru', 'ar', 'th', 'zh-hant'] as const

/** 步骤「公司／行业」：只回显确认，不再采一遍（行业与角色在注册链已落库） */
const stepCompany: OnboardStep = {
  id: 'company',
  titleKey: 'onboard.step.company',
  descKey: 'onboard.step.company.desc',
  // 行业与角色在注册链已采集，这一步只做**回显确认**（write=skip），
  // 再收一遍＝两处入口各存一份，迟早和注册表单打架（AGENTS §一·5 报价单源同族教训）
  fields: [
    { key: 'industry_note', kind: 'text', labelKey: 'onboard.field.industryNote' },
  ],
  write: 'skip',
}

/** 步骤「第一款产品」：名称必填（没有名字就建不出档），定位与用途可选 */
const stepProduct: OnboardStep = {
  id: 'product',
  titleKey: 'onboard.step.product',
  descKey: 'onboard.step.product.desc',
  fields: [
    { key: 'name', kind: 'text', labelKey: 'onboard.field.productName', required: true },
    { key: 'tagline', kind: 'text', labelKey: 'onboard.field.tagline' },
    { key: 'use_case', kind: 'textarea', labelKey: 'onboard.field.useCase' },
  ],
  write: 'product.profile',
}

/** 步骤「目标语言」：只写产品行的 target_langs，空数组是合法态（未设定） */
const stepLangs: OnboardStep = {
  id: 'langs',
  titleKey: 'onboard.step.langs',
  descKey: 'onboard.step.langs.desc',
  fields: [
    { key: 'target_langs', kind: 'langs', labelKey: 'onboard.field.targetLangs', maxPick: 12 },
  ],
  write: 'product.langs',
}

/** 步骤「搜索引擎收录」：出厂默认开，但默认开必须在这一屏说出口 */
const stepSeo: OnboardStep = {
  id: 'seo',
  titleKey: 'onboard.step.seo',
  descKey: 'onboard.step.seo.desc',
  fields: [
    // ★ 出厂默认开＋**必须在这里明示一次**（一期裁定 3／§5.8：默认开 ≠ 默认告知）
    { key: 'indexable', kind: 'toggle', labelKey: 'onboard.field.seoIndexable', hintKey: 'onboard.field.seoIndexable.hint' },
  ],
  write: 'product.seo',
}

/** 场景 → 步骤序列（新增场景＝在这里加一行，不许在组件里 if/else 分支） */
export const SCENE_STEPS: Record<OnboardScene, OnboardStep[]> = {
  crossborder: [stepCompany, stepProduct, stepLangs, stepSeo],
  // 中文私域：养客场景没有"对外公开页"这一件 ⇒ 去掉 seo 步，其余同构
  privatecrm: [stepCompany, stepProduct, stepLangs],
  // 门店现场：现场话术优先，语种仍要（一张海报要出几种语言）
  storefront: [stepCompany, stepProduct, stepLangs],
}

/** 解析场景码：未知／缺省一律回 crossborder（不猜、不报错——同 captcha provider 那档的回落纪律） */
export function parseOnboardScene(raw?: string | null): OnboardScene {
  return raw === 'privatecrm' || raw === 'storefront' ? raw : 'crossborder'
}

/** 这一步能不能"完成"：必填字段都有值（langs 允许空，因为"未设定"是合法态） */
export function stepIsCompletable(step: OnboardStep, values: Record<string, unknown>): boolean {
  return step.fields.every((f) => {
    if (!f.required) return true
    const v = values[f.key]
    if (typeof v === 'string') return v.trim() !== ''
    if (Array.isArray(v)) return v.length > 0
    return v !== undefined && v !== null
  })
}

/** 全部步骤走完的判据（供"是否还要弹提醒"用）：**这一步由服务端事实回答，不由会话态回答** */
export function onboardDone(hasProducts: boolean, readOk: boolean): boolean {
  if (!readOk) return true // 读失败＝不知道 ⇒ 不许把人推进向导（宁可不提醒，也不误伤老用户）
  return hasProducts
}
