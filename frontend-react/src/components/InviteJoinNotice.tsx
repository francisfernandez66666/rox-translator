// ============================================================================
// components/InviteJoinNotice.tsx — 受邀落地页的「你在加入哪家企业」告知块
// （★ 2026-10-24 多身份 B4-d，方案 §6.5 第 4 条 + §6.3 无效码显式报错）。
//
// 为什么单独成文件而不是写在 Login.tsx 的两张卡里：
//   这一句必须落在**两张卡**上——合法链接落在注册卡（预填＋锁定），死链／校验中落在登录卡
//   （主站 unified 下注册卡根本不可达）。写在卡内就会是两份拷贝，
//   而本仓这一族的坏形态从来是"改两面漏一面"（同 AGENTS §一·5 页脚尾注那条）。
//
// 三条判据口径：
//   1. **死链也出声**：后端对 invalid/used/revoked/expired 一律回 HTTP 200 + valid=false，
//      就是为了这一面能说"这条链接已经过期了，请找管理员重新获取"而不是抛一个报错页；
//      四档各一句（下一步不同：催重发 / 确认是否别人已用 / 找管理员撤销了没）。
//      后端出栈的那句中文 message 是给运维的，当事人这一面必须拿他自己的语言，所以这里按 reason 查词典。
//   2. **取不到 ≠ 死链**：请求本身失败（网络抖动）与"服务端说这条死了"是两件事，
//      文案分开（checkFail vs r*），前者不许把人引去"找管理员重新生成"。
//   3. **加入后资料可见范围必须在点提交之前说**（§6.5 第 4 条本体）——
//      入职进的是企业：账号资料对企业管理员可见、企业知识库与企业预算随之打开。
//      这一句漏掉等于让人盲填一份入职资料。
//
// 样式：只用登录卡既有档（.auth-note / .auth-err），不新增视觉规格（AGENTS §一·5 界面规格＝UI 交付真值）。
// ============================================================================
import { useT } from '@/i18n'

/** 匿名校验回执里这一面要用的字段（与 api/invites.ts 的 inviteLinkPublic 同形，只取渲染需要的） */
export interface InvitePeek {
  valid?: boolean
  reason?: 'invalid' | 'used' | 'revoked' | 'expired'
  tenant_name?: string
  org_name?: string
  /** 管理员建号时写下的受邀人用户名（登录侧用来预填，本组件不渲染它） */
  username?: string
  bound_email?: string
  email_locked?: boolean
}

// 组件入参：checking=公开校验口还在飞；peek=回执（null 只代表"网络失败"，
// 不代表"服务端判死"——两种形态的文案不同，所以由调用方显式区分后传进来）。
interface Props {
  /** 请求在飞 */
  checking: boolean
  /** 回执；网络失败时调用方传 null（与"服务端判死"区分开） */
  peek: InvitePeek | null
}

// 服务端 reason → 词典键：四种失败各一句可行动文案（用过的码要点名"找邀请人再发一枚"，
// 只写"邀请码无效"会让人反复重试同一链接）。键缺失时由取词链回落 en→zh，不在这里兜底。
const REASON_KEY: Record<string, string> = {
  invalid: 'auth.invite.rInvalid',
  used: 'auth.invite.rUsed',
  revoked: 'auth.invite.rRevoked',
  expired: 'auth.invite.rExpired',
}

// 四档分派（data-state 是 e2e 的判据靶子，改名要同步动那几条锁）：
//   checking→在飞一行；peek=null→checkFail（网络失败，不引去"找管理员重发"）；
//   valid=false→按 reason 出四档死链文案；合法→企业名主句＋部门/锁定邮箱（回执有才出）＋两条资料告知。
export default function InviteJoinNotice({ checking, peek }: Props) {
  const [, t, tpl] = useT()
  const lines: string[] = []
  if (checking) return <p className="auth-note" data-testid="invite-notice">{t('auth.invite.checking')}</p>
  if (!peek) return <p className="auth-err" data-testid="invite-notice" data-state="checkfail">{t('auth.invite.checkFail')}</p>
  if (!peek.valid) {
    const key = REASON_KEY[peek.reason || 'invalid'] || 'auth.invite.rInvalid'
    return <p className="auth-err" data-testid="invite-notice" data-state="dead">{t(key)}</p>
  }
  // 合法链接：企业名是主句，部门／锁定邮箱按回执有没有再出
  lines.push(tpl('auth.invite.joinOrg', { org: peek.tenant_name || '' }))
  if (peek.org_name) lines.push(tpl('auth.invite.joinDept', { dept: peek.org_name }))
  if (peek.email_locked && peek.bound_email) lines.push(tpl('auth.invite.lockedEmail', { email: peek.bound_email }))
  lines.push(t('auth.invite.dataNote'))
  lines.push(t('auth.invite.selfNote'))
  return (
    <div data-testid="invite-notice" data-state="valid" style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      {lines.map((line) => <p key={line} className="auth-note">{line}</p>)}
    </div>
  )
}
