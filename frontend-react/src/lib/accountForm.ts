// ============ accountForm.ts · 账号表单的共用形状判据 ============
// ★ 2026-10-21 IAM 重做 P0-6 建立。
//
// 这一层**只判形状**：邮箱像不像一个邮箱、口令够不够长。
// 刻意**不判存在性**——"这个邮箱注册过没有"是防枚举口径下服务端不许回答的那一半
// （判据本体＝backend-go/internal/api/auth.go 的 `forgotAlwaysMsg`，三条出口逐字同句）。
//
// 为什么单独成一个文件而不是各页各写一份正则：同一件事此前在注册、找回、换绑三张面上
// 只有换绑那一张有校验（modals.tsx），另两张连 `type="email"` 都没挂 ⇒ 用户写错一个字母
// 要等到最后一步"验证码错误"才发现（运营者报的第 3 条里"没校验我输入的邮箱对不对"的那一半）。
// 同 AGENTS §一·14「清单只有一份」那一族纪律。
// ================================================================

/** EMAIL_SHAPE_RE 邮箱形状判据：本地串校验，不发网络、不查 DNS、不验 MX。 */
export const EMAIL_SHAPE_RE = /^[^\s@]+@[^\s@]+\.[^\s@]{2,}$/

/** isEmailShape 判邮箱形状（先 trim；空串按"不合法"处理，让调用方的"必填"那一腿先出声）。 */
export function isEmailShape(v: string): boolean {
  return EMAIL_SHAPE_RE.test((v || '').trim())
}

/**
 * PWD_MIN_LEN 口令最小长度——与后端 `internal/iam/account.go` 的 `PasswordMinLen` **同值**。
 * ⚠️ 后端才是判据（服务端四处写口都走 `auth.ValidatePassword`），前端这一份只是展示提示；
 *    历史上它曾是**唯一实际闸**（重置/改密/管理台建号三条链后端只判非空），那是缺陷不是设计。
 *    改这个数字必须两侧同批改，并复跑 `go test ./internal/iam/`。
 */
export const PWD_MIN_LEN = 6
