// ============================================================================
// lib/voiceFail.ts — 语音两件套（录音识别 / 译文朗读）失败文案的唯一取词口
//
// ★ 立这条的原因（改造方案 §六 第 9 条，2026-10-14）：
//   后端在语音失败时返回的是**中文人话 + 机读档位**（`{error, reason}`，见
//   `backend-go/internal/api/chat_audio.go` 的 chatVoiceFailure 与 fileproc 的 TtsReason*）。
//   改造前这里直接把 `error` 那句话 toast 给客户 ⇒ **俄语/泰语/阿拉伯语访客在界面上
//   看到的是一句中文**（同族缺陷在本仓已记过一次：挂件按钮名把中文原样塞进英文界面）。
//   正确分工：后端负责**是什么失败**（档位），前端负责**用客户那门语言怎么说**（词典）。
//
// ★ 为什么档位只有一份尺子：`reason` 的取值集合由后端定义，前端只做映射，
//   **禁止**在这里再判一次 message 的字面（那是把中文句子当协议用，改文案即 breaking change）。
//   不认识的值一律落到「暂时用不了」这一档（未知形态的代价只许是话术变通用，
//   不许是界面上凭空消失一句提示、更不许把译文判死）。
//
// ★ 译文第一（用户 2026-10-14 令）：这一条链的任何失败都只影响「有没有声音／有没有转写」，
//   函数只产出**提示文案**，不触碰任何翻译状态与消息内容。
// ============================================================================
import { t } from '@/i18n'
import { VoiceError } from '@/api/chatVoice'

// VOICE_FAIL_KEYS 本模块消费的三个词典键（**唯一清单**）。
// 列出来只为一个目的：让 chatVoice.lock.test.ts 能按这一份去核 12 份词典是否逐键补译，
// 而不是在测试里再抄一遍键名（抄一份就会漂）。新增档位要动这里，锁才会跟着去核词典。
export const VOICE_FAIL_KEYS = [
  'chat.voiceLangUnavailable',
  'chat.voiceTooLong',
  'chat.voiceUnavailable',
] as const

// voiceFailText 把一次语音失败换成**当前界面语言**的一句人话。
// 入参是 unknown：调用点在 catch 块里，异常可能来自 fetch／浏览器权限／后端错误体三种形态，
// 这里统一收敛，免得每个调用点各写一遍 instanceof。
//
// ★ 三个 case/default 分支与后端 `fileproc.TtsReason*` 逐字对齐（等值锁＝api/chatVoice.lock.test.ts V1–V3）：
//   这里写一支后端永不发出的档位＝死分支，锁判红；后端新增一档而这里没有分支＝落到 default，
//   代价只是话术变通用，属刻意的 fail-soft（宁可说得宽，不许说得错）。
export function voiceFailText(err: unknown): string {
  const reason = err instanceof VoiceError ? err.reason : 'failed'
  switch (reason) {
    // 语种没有配音：这是**能力边界**不是故障，所以话术里必须留一句「译文照常」，
    // 否则客户会以为整单翻译出了问题（历史上 TTS 失败被判死译文，正是这句话没交代）。
    case 'unsupported_lang':
      return t('chat.voiceLangUnavailable')
    // 译文超过单次朗读上限：可行动作是「分段」，与「服务器暂时用不了」是两件事。
    case 'too_long':
      return t('chat.voiceTooLong')
    // capability_missing（服务器未装依赖/未投放模型）／empty／failed／拿不到档位：
    // 一律归到「暂时用不了」——这三档的共同点是**客户自己做不了什么**，
    // 所以文案只许是"没有声音"这一件事，不许出现堆栈、路径、依赖名。
    default:
      return t('chat.voiceUnavailable')
  }
}
