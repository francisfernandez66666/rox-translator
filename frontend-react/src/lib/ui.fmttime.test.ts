// ============================================================================
// ui.fmttime.test.ts — fmtTime 时区口径双 TZ 等值锁（★ F-14 批G，2026-09-25）
// 缺陷背景：后端时间为 UTC ISO 串，旧 fmtTime 一律裸切片，东八区用户看跨日记录
// 会「显示错一天」。本锁分三半：
//   ① TZ 无关锁：local 缺省（false）时旧「原样切片」行为一字不变，既有调用点零误伤；
//   ② 双 TZ 等值锁：同一 UTC 串在 local=true 下按**运行时本地时区**出日期与时分——
//      期望值用纯 Date 本地取值器（getFullYear/getHours…）独立算出，与被测的
//      Intl 格式化是两条独立链路，不是同义反复；必须真跑两条命令各一遍、两条都绿：
//        TZ=UTC            npx vitest run src/lib/ui.fmttime.test.ts
//        TZ=Asia/Shanghai  npx vitest run src/lib/ui.fmttime.test.ts
//      （用例按 Intl resolvedOptions().timeZone 选档位；跑在未钉 TZ 的机器时区上
//        双 TZ 档自动跳过，①③ 的 TZ 无关锁照常生效。）
//   ③ TZ 无关锁：fail-closed 契约（空值 '—'、非法串回原样），列表逐行调用不许抛。
// ============================================================================
import { describe, it, expect } from 'vitest'
import { fmtTime } from './ui'

/** 被测样本：UTC 18:30 —— 东八区已跨日到次日 02:30，正是缺陷的触发形态 */
const UTC_ISO = '2026-09-25T18:30:00Z'

/** 当前进程实际生效的运行时区（以 Date/Intl 行为准，不读 process.env.TZ：
 *  Node 会归一档位且 worker 场景下 env 与 Date 真实行为可能脱节） */
const zone = Intl.DateTimeFormat().resolvedOptions().timeZone

/** 两位补零 */
const pad = (n: number) => String(n).padStart(2, '0')
/** 用纯 Date 本地取值器独立算出「该时区正确的 yyyymmddHHMM」——期望值与被测实现不同链路 */
function localDigits(iso: string): string {
  const d = new Date(iso)
  return `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}${pad(d.getHours())}${pad(d.getMinutes())}`
}
/** 抹掉全部非数字（分隔符/标点不进比较位），只留数字序列做等值锁 */
const digitsOnly = (s: string) => s.replace(/\D/g, '')

// ---------- ① 旧口径不回归（TZ 无关：裸切片行为与时区无关，任何档下逐字不变） ----------
describe('fmtTime 旧口径（local 缺省）不回归', () => {
  it('缺省调用仍是 UTC 串原样切片 YYYY-MM-DD HH:MM:SS', () => {
    expect(fmtTime(UTC_ISO)).toBe('2026-09-25 18:30:00')
    expect(fmtTime('2026-09-25T18:30:00+08:00')).toBe('2026-09-25 18:30:00')
  })
  it('空值仍返回破折号占位', () => {
    expect(fmtTime(undefined)).toBe('—')
    expect(fmtTime('')).toBe('—')
  })
})

// ---------- ② 双 TZ 等值锁：同一 UTC 串，两个时区各断各自正确的本地日历日 ----------
describe('fmtTime local=true · 双 TZ 等值锁（TZ=UTC / TZ=Asia/Shanghai 各跑一遍）', () => {
  /** 两档钉死的数字序列：UTC 当日 18:30 / 东八区跨日次日 02:30 */
  const DIGITS_BY_ZONE: Record<string, string> = {
    UTC: '202609251830',
    'Asia/Shanghai': '202609260230',
  }
  const expected = DIGITS_BY_ZONE[zone]
  const wrong = zone === 'Asia/Shanghai' ? DIGITS_BY_ZONE.UTC : DIGITS_BY_ZONE['Asia/Shanghai']
  // 只在两个钉死时区下跑等值；其他机器时区跳过（由两条命令实跑承担闸门职责）
  const maybeIt = expected ? it : it.skip

  maybeIt(`${zone} 下 local 口径断出该时区自己的本地日期字面值，且不出现另一时区档`, () => {
    // 运行时区生效自证：TZ 没透传进 worker 时直接红灯，不留假绿
    expect(new Date(UTC_ISO).getHours()).toBe(zone === 'Asia/Shanghai' ? 2 : 18)
    // 等值锁：纯 Date 取值器独立算出的 yyyymmddHHMM 必须逐位等于渲染结果的数字序列
    expect(digitsOnly(fmtTime(UTC_ISO, true))).toBe(localDigits(UTC_ISO))
    expect(localDigits(UTC_ISO)).toBe(expected)
    // 反向锁：跨日错档（旧裸切片在另一时区下的数字序列）不得出现
    expect(digitsOnly(fmtTime(UTC_ISO, true))).not.toBe(wrong)
  })

  it('local 口径仍守空值契约', () => {
    expect(fmtTime(undefined, true)).toBe('—')
    expect(fmtTime('', true)).toBe('—')
  })
})

// ---------- ③ fail-closed：非法日期不得抛（列表逐行调用，抛一次白屏一页） ----------
describe('fmtTime local=true · fail-closed', () => {
  it('非法日期原样返回，不抛异常', () => {
    expect(() => fmtTime('not-a-date', true)).not.toThrow()
    expect(fmtTime('not-a-date', true)).toBe('not-a-date')
  })
})
