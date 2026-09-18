// ============================================================================
// components/admin/AssistP.tsx — AI 助手管理面板（★ autosales；改造 1A 更新）
// 职责：内嵌 AI 助手服务的管理台（知识库/话术/流程/功能入口/配置/会话）。
//
// 说明（改造 1A，2026-09-17）：assist 服务已融合进主仓单 module，管理台 Token
// 不再要求用户手工粘贴——本面板挂载时经主后台 /api/admin/assist/token（仅超管）
// 拉取生效 Token，写入同源 localStorage('assist_tok')，iframe 内的管理台页面
// 加载时自行读取该键并静默校验（绿=已验证 / 红=不匹配）。
//
// 注：/assist-api 是**同源**路径反代（Caddy uri strip_prefix），故父页面与 iframe
// 共用同一 localStorage；若部署为跨域 iframe，本注入会失效，需回退手工粘贴。
//
// 2026-09-18（UI 融合）：面板底色随全站暗色主题调整（iframe 容器底 #0E1014、
//   状态条改用中性灰边框/浅色字），标题与状态文案去掉 emoji 前缀改由颜色表意。
// ============================================================================

import { useCallback, useEffect, useState } from 'react'
import { Button } from '@/ui/langcross/src'
import { ASSIST_API } from '@/api/assist'
import { adminAssistToken, adminAssistTokenRotate } from '@/api'
import { toastSuccess, toastError } from '@/lib/toastBus'

/** 管理台读取 Token 的 localStorage 键（与 assist web/admin.html 约定一致） */
const ASSIST_TOK_KEY = 'assist_tok'

/** 加载态：checking=正在取 Token | ready=已注入可渲染 | manual=拿不到 Token 需手工填 | error=接口异常 */
type Phase = 'checking' | 'ready' | 'manual' | 'error'

// AssistP AI 助手管理面板：iframe 托管 assist 管理台 + 管理 Token 自动注入
export default function AssistP() {
  const [phase, setPhase] = useState<Phase>('checking')
  const [source, setSource] = useState<string>('')
  const [errMsg, setErrMsg] = useState('')
  // iframe 重挂载计数：Token 变更后强制刷新（改 key 触发重建，避免缓存旧页面）
  const [reloadKey, setReloadKey] = useState(0)
  const [rotateVal, setRotateVal] = useState('')
  const [rotating, setRotating] = useState(false)

  // 拉取 Token 并注入同源 localStorage；注入成功才渲染 iframe（避免先渲染再刷新的闪烁）
  const loadToken = useCallback(async () => {
    setPhase('checking')
    try {
      const r = await adminAssistToken()
      if (!r.success) {
        setErrMsg(r.message || '读取失败')
        setPhase(r.message && /超管|权限|403/.test(r.message) ? 'manual' : 'error')
        return
      }
      const tok = (r.token || '').trim()
      if (!tok) {
        // 未配置（source=none）：不覆盖用户可能已手工填过的值
        setErrMsg('主后台尚未配置管理 Token（可由超管在下方设置，或配置环境变量 ASSIST_ADMIN_TOKEN）')
        setPhase('manual')
        return
      }
      try { localStorage.setItem(ASSIST_TOK_KEY, tok) } catch { /* 隐私模式：忽略，回落手工填 */ }
      setSource(r.source || '')
      setPhase('ready')
    } catch (e: any) {
      setErrMsg(e?.message || '接口异常')
      setPhase('error')
    }
  }, [])

  useEffect(() => { void loadToken() }, [loadToken])

  // 轮换 Token（超管）：写库后重新注入并重建 iframe
  async function rotate() {
    if (rotating) return
    setRotating(true)
    try {
      const r = await adminAssistTokenRotate(rotateVal.trim())
      if (!r.success) { toastError(r.message || '保存失败'); return }
      toastSuccess(rotateVal.trim() ? 'Token 已更新' : 'Token 已清除（回落环境变量）')
      setRotateVal('')
      await loadToken()
      setReloadKey((k) => k + 1)
    } catch (e: any) {
      toastError(e?.message || '保存失败')
    } finally { setRotating(false) }
  }

  // 将 Token 生效来源映射为中文展示文案（env=部署侧环境变量 / db=库内密文 / 其他=空）
  const srcLabel = source === 'env' ? '环境变量 ASSIST_ADMIN_TOKEN' : source === 'db' ? '主后台库内配置' : ''

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 12, marginBottom: 8, flexWrap: 'wrap' }}>
        <h2 style={{ margin: 0 }}> AI 助手管理</h2>
        <span style={{ color: '#6b7280', fontSize: 13 }}>
          知识库 · 话术 · 流程 · 功能入口 · 配置（数据存于 AI 助手独立服务，与业务库隔离）
        </span>
        <span style={{ marginLeft: 'auto', display: 'flex', gap: 6, alignItems: 'center' }}>
          <Button size="sm" variant="secondary" onClick={() => { void loadToken().then(() => setReloadKey((k) => k + 1)) }}>
            重新加载
          </Button>
        </span>
      </div>

      {/* 管理 Token 状态条（改造 1A：免手填；异常时给出可自助的处置路径）
          三态配色：ready=绿底浅灰字、checking=灰底灰字、manual/error=暖橙警示底；
          文案不再用 emoji 前缀，状态由颜色 + 文字表达。
          注意：ready 分支的底色仍是浅色 #f0fdf4 而文字改成浅色 #E7E9EA，
          明暗档未同步（浅底配浅字几乎不可读），后续需一并把底色换深色。 */}
      <div style={{
        fontSize: 12, borderRadius: 8, padding: '8px 12px', marginBottom: 8,
        background: phase === 'ready' ? '#f0fdf4' : phase === 'checking' ? '#f8fafc' : '#fff7ed',
        border: `1px solid ${phase ==='ready'?'#464C58': phase ==='checking'?'#e2e8f0':'#fed7aa'}`,
        color: phase ==='ready'?'#E7E9EA': phase ==='checking'?'#9AA0AA':'#b45309',
      }}>
        {phase === 'checking' && '正在获取管理 Token…'}
 {phase ==='ready'&& ` 管理 Token 已自动注入（来源：${srcLabel}），无需手工粘贴`}
 {phase ==='manual'&& ` ${errMsg}`}
 {phase ==='error'&& ` 获取管理 Token 失败：${errMsg}`}
      </div>

      {phase === 'manual' && (
        <div style={{ fontSize: 12, color: '#6b7280', marginBottom: 8, lineHeight: 1.7 }}>
          兜底：可在下方填入与 AI 助手服务一致的 Token（环境变量 <code>ASSIST_ADMIN_TOKEN</code>，
          或主后台库内配置），保存后管理台即自动使用；也可在管理台右上角手工粘贴。
        </div>
      )}

      {(phase === 'manual' || phase === 'ready') && (
        <div style={{ display: 'flex', gap: 8, marginBottom: 10, alignItems: 'center', flexWrap: 'wrap' }}>
          <input
            className="lc-input"
            value={rotateVal}
            onChange={(e) => setRotateVal(e.target.value)}
            type="password"
            placeholder="设置/轮换管理 Token（留空=清除库内配置，回落环境变量）"
            style={{ maxWidth: 420 }}
            aria-label="管理 Token"
          />
          <Button size="sm" variant="primary" disabled={rotating} onClick={rotate}>保存 Token</Button>
        </div>
      )}

      {phase === 'checking' ? null : (
        // iframe 容器：底色与内嵌 assist 管理台一致（#0E1014），
        // 目的是页面加载瞬间不闪白；高度按视口扣掉上方说明区，minHeight 保证小屏可滚动。
        <div style={{
          border: '1.2px solid var(--lc-border-card)', borderRadius: 10, overflow: 'hidden',
          height: 'calc(100vh - 250px)', minHeight: 460, background: '#0E1014',
        }}>
          <iframe
            key={reloadKey}
            src={`${ASSIST_API}/assist/admin`}
            title="AI 助手管理台"
            style={{ width: '100%', height: '100%', border: 'none' }}
          />
        </div>
      )}

      <p style={{ color: '#9ca3af', fontSize: 12, marginTop: 8 }}>
        服务部署与反代配置见 <code>backend-go/internal/assist/README.md</code>；管理台页面与初始数据已随二进制内嵌，无需投放静态文件。
      </p>
    </div>
  )
}
