// ============================================================================
// instantTier.caps.test.ts — S2「档位四面同源」的前端半段（★ 2026-10-31 补）
//
// 背景：`lib/instantTier.ts` 里那张表是**出厂默认**，而运营可在管理台改即时档位。
// 界面必须用 `/api/instant/caps` 的现值覆盖，否则运营调档后 UI 仍按旧档压图/提示
// （对外口径与实扣不一致）。本文件钉住三件事：
//   ① 覆盖层生效（免费/付费两档各自独立）；
//   ② 脏值（负数/0/非数字/缺失）一律忽略，保留默认（fail-open）；
//   ③ reset 后回到出厂默认。
// ============================================================================
import { describe, expect, it, beforeEach } from 'vitest'
import {
  INSTANT_TIER_LIMITS,
  applyInstantCaps,
  instantLimits,
  instantHardLimits,
  resetInstantCaps,
} from './instantTier'

const MIB = 1024 * 1024

describe('instantTier × /api/instant/caps', () => {
  beforeEach(() => resetInstantCaps())

  it('无覆盖时用出厂默认（1 MiB/30 s 与 3 MiB/60 s）', () => {
    expect(instantLimits('free')).toEqual(INSTANT_TIER_LIMITS.free)
    expect(instantLimits('paid')).toEqual(INSTANT_TIER_LIMITS.paid)
    expect(instantHardLimits()).toEqual(INSTANT_TIER_LIMITS.paid)
  })

  it('覆盖层按档独立生效，且长边仍用默认（caps 不含长边）', () => {
    applyInstantCaps({ free_image_max_mb: 2, free_audio_max_sec: 45, image_max_mb: 4, audio_max_sec: 90 })
    expect(instantLimits('free')).toEqual({
      imageMaxBytes: 2 * MIB,
      imageMaxSide: INSTANT_TIER_LIMITS.free.imageMaxSide,
      audioMaxSec: 45,
    })
    expect(instantLimits('paid')).toEqual({
      imageMaxBytes: 4 * MIB,
      imageMaxSide: INSTANT_TIER_LIMITS.paid.imageMaxSide,
      audioMaxSec: 90,
    })
    expect(instantHardLimits().imageMaxBytes).toBe(4 * MIB)
  })

  it('脏值忽略：负数/0/非数字/缺字段都保留默认', () => {
    applyInstantCaps({
      free_image_max_mb: -1,
      free_audio_max_sec: 0,
      image_max_mb: 'x' as unknown as number,
      // 故意缺 audio_max_sec
    })
    expect(instantLimits('free')).toEqual(INSTANT_TIER_LIMITS.free)
    expect(instantLimits('paid')).toEqual(INSTANT_TIER_LIMITS.paid)
  })

  it('null/undefined 不改变任何档（探针失败 fail-open）', () => {
    applyInstantCaps({ free_image_max_mb: 2 })
    applyInstantCaps(null)
    expect(instantLimits('free').imageMaxBytes).toBe(2 * MIB)
    resetInstantCaps()
    expect(instantLimits('free').imageMaxBytes).toBe(1 * MIB)
  })
})
