// ============================================================================
// components/admin/AssistP.tsx — AI 助手管理面板（★ autosales）
// 职责：内嵌 ai-assist 独立服务的管理台（知识库/话术/流程/功能入口/配置/会话）。
// 说明：ai-assist 为独立 Go 服务（同源 /assist-api 反代），管理台自身带 Token 鉴权，
//       此处 iframe 全量托管，避免与主后台 UI 耦合。
// ============================================================================

import { ASSIST_API } from '@/api/assist'

export default function AssistP() {
  const src = `${ASSIST_API}/assist/admin`
  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 12, marginBottom: 8 }}>
        <h2 style={{ margin: 0 }}>🤖 AI 助手管理</h2>
        <span style={{ color: '#6b7280', fontSize: 13 }}>
          知识库 · 话术 · 流程 · 功能入口 · 配置（数据存于 ai-assist 独立服务）
        </span>
      </div>
      <div style={{
        border: '1px solid #e5e8ef', borderRadius: 10, overflow: 'hidden',
        height: 'calc(100vh - 210px)', minHeight: 480, background: '#fff',
      }}>
        <iframe
          src={src}
          title="AI 助手管理台"
          style={{ width: '100%', height: '100%', border: 'none' }}
        />
      </div>
      <p style={{ color: '#9ca3af', fontSize: 12, marginTop: 8 }}>
        首次使用需在右上角填入管理 Token（环境变量 ASSIST_ADMIN_TOKEN）。服务启动与反代配置见 <code>ai-assist/README.md</code>。
      </p>
    </div>
  )
}
