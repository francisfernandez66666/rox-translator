// @vitest-environment node
// ============================================================================
// src/lib/onboardingSchema.lock.test.ts — 引导流"配置驱动"那半边的机械守卫（O 组，2026-11-06）
//
// 配置驱动唯一的真风险不是写错字，而是**配置里写了一张不存在的口**：
// 界面照样渲染、按钮照样能点，点下去 404 或静默不落库，而 jsdom 单测把 api 层整体 mock 掉——
// 于是"走完引导但什么都没发生"可以绿灯活很久（AGENTS §三「有脚本无调度要问到第二层」在配置层的同形）。
// 这个文件就是那"第二层"：每一步声明的写口、采集的键名，都必须同时对得上
// ① 前端 api 层真实导出的函数 ② 后端真实注册的路由 ③ 后端真实解析的 JSON 键。
//
// 运行：npx vitest run src/lib/onboardingSchema.lock.test.ts
// ============================================================================
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { LANG_OPTIONS, SCENE_STEPS, WRITE_TARGETS, onboardDone, parseOnboardScene, stepIsCompletable } from './onboardingSchema'

const ROOT = join(__dirname, '..', '..', '..')
const API = readFileSync(join(ROOT, 'frontend-react/src/api/product.ts'), 'utf8')
const BE = ['product_api.go', 'product_public_api.go', 'product_asset_file.go']
  .map((f) => readFileSync(join(ROOT, 'backend-go/internal/api', f), 'utf8'))
  .join('\n')
const SEO_META = JSON.parse(readFileSync(join(ROOT, 'frontend-react/src/seo/route_meta.json'), 'utf8')) as { languages: string[] }

/** 剥注释：说明里写过的函数名/键名不算点位（本仓负向锁第四次踩同一坑，先剥再判） */
function strip(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '').replace(/\/\*[\s\S]*?\*\//g, '')
}
const API_CODE = strip(API)
const BE_CODE = strip(BE)

/** 把全部场景的全部步骤摊平（判据一律按全集判，不按"我看到的那几步"） */
const ALL_STEPS = Object.values(SCENE_STEPS).flat()

describe('引导流配置表（O 组）', () => {
  it('S1 每个步骤声明的写口都在这张真值表里，且真值表里的口都真存在于前端 api 层', () => {
    const unknown = ALL_STEPS.filter((s) => !(s.write in WRITE_TARGETS)).map((s) => `${s.id}→${s.write}`)
    expect(unknown, '步骤写了未登记的写口').toEqual([])
    const missing = Object.entries(WRITE_TARGETS)
      .filter(([k, fn]) => k !== 'skip' && !new RegExp(`export async function ${fn}\\b`).test(API_CODE))
      .map(([k, fn]) => `${k}→${fn}`)
    expect(missing, '写口指向的 api 函数不存在（配置驱动最坏的坏法）').toEqual([])
  })

  it('S2 写口对应的 HTTP 路由必须在后端真实注册（前端函数存在≠后端有这个口）', () => {
    for (const path of ['/api/product/save', '/api/product/seo']) {
      expect(BE_CODE, `后端未注册 ${path}，但引导表把它当写口`).toContain(`"${path}"`)
    }
  })

  it('S3 采集字段提交的键名，后端必须真解析它（两侧同尺；不解析＝回 success 而库里一字未动）', () => {
    const jsonTags = new Set<string>()
    for (const m of BE_CODE.matchAll(/`json:"([a-z_]+)"`/g)) jsonTags.add(m[1])
    const keysOf = (stepId: string): string[] => {
      const s = ALL_STEPS.find((x) => x.id === stepId)
      return (s?.fields ?? []).map((f) => f.key)
    }
    // productSave 收 name/tagline/use_case/target_langs；seoSet 收 product_id/indexable
    for (const k of ['name', 'tagline', 'use_case', 'target_langs', 'indexable']) {
      expect(jsonTags.has(k), `后端没解析 json 键 ${k}，而引导步骤要交它`).toBe(true)
    }
    expect(keysOf('langs'), '语种步骤交的是别的键名').toContain('target_langs')
    expect(keysOf('seo'), 'SEO 步骤交的是别的键名').toContain('indexable')
  })

  it('S4 每个场景都必须真的能"造出一个产品"（否则引导走完判据仍不成立，用户被无限提醒）', () => {
    for (const [scene, steps] of Object.entries(SCENE_STEPS)) {
      const writes = steps.map((s) => s.write)
      expect(writes, `场景 ${scene} 没有任何一步会建档`).toContain('product.profile')
    }
  })

  it('S5 语种候选必须是站点码表的子集（勾一个 hreflang 表外的码＝对外页铺出点不开的链接）', () => {
    const outside = LANG_OPTIONS.filter((c) => !SEO_META.languages.includes(c) && !SEO_META.languages.includes(c.replace('-', '_')))
    expect(outside, '引导里的语种不在站点码表内').toEqual([])
    // 反向：码表里的 zh（界面语，不是对外目标语）不该出现在候选里——"说给哪国客户"与"界面用什么语"是两件事
    expect(LANG_OPTIONS).not.toContain('zh')
  })

  it('S6 完成判据：读失败＝不提醒（不许把一次接口抖动读成"这人没产品"）', () => {
    expect(onboardDone(false, false), '读失败必须不推进引导').toBe(true)
    expect(onboardDone(true, true)).toBe(true)
    expect(onboardDone(false, true), '确实没有产品＝要提醒').toBe(false)
  })

  it('S7 必填门只卡必填字段；语种允许空（"未设定"是合法态，不是缺陷）', () => {
    const product = ALL_STEPS.find((s) => s.id === 'product')!
    expect(stepIsCompletable(product, { name: '   ' }), '全空格的产品名不该放行').toBe(false)
    expect(stepIsCompletable(product, { name: '整机' })).toBe(true)
    const langs = ALL_STEPS.find((s) => s.id === 'langs')!
    expect(stepIsCompletable(langs, { target_langs: [] }), '没勾语种必须仍可继续').toBe(true)
  })

  it('S8 未知场景码一律回落 crossborder（不猜、不报错，同 captcha provider 那档纪律）', () => {
    expect(parseOnboardScene('weird-scene')).toBe('crossborder')
    expect(parseOnboardScene(undefined)).toBe('crossborder')
    expect(parseOnboardScene('privatecrm')).toBe('privatecrm')
    // 私域场景**不该**有对外页收录那一步（那一面没有公开页），这是三面归一的差异所在
    expect(SCENE_STEPS.privatecrm.map((s) => s.write)).not.toContain('product.seo')
  })

  it('反证 R1：把某一步的写口改成一张不存在的口，S1 必须红（证明这守卫不是摆设）', () => {
    const mutated = API_CODE.replace(/export async function productSave\b/, 'export async function productSavedX\b')
    const hit = Object.entries(WRITE_TARGETS)
      .filter(([k, fn]) => k !== 'skip' && !new RegExp(`export async function ${fn}\\b`).test(mutated))
      .map(([k]) => k)
    expect(hit.length, '变异没落地或守卫失明').toBeGreaterThan(0)
  })

  it('反证 R2：删掉后端对 target_langs 的解析，S3 必须红', () => {
    const mutated = BE_CODE.replace(/`json:"target_langs"`/g, '`json:"targetLangsX"`')
    expect(mutated).not.toBe(BE_CODE)
    const tags = new Set<string>()
    for (const m of mutated.matchAll(/`json:"([a-z_]+)"`/g)) tags.add(m[1])
    expect(tags.has('target_langs'), '后端不再解析 target_langs，S3 应当判红').toBe(false)
  })
})
