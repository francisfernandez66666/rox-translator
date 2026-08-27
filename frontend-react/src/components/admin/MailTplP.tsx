// components/admin/MailTplP.tsx — 邮件模板配置（仅超管）
// 按用途区分的多个模板：注册验证码 / 找回密码 / 企业注册提醒 / 租户通知 / 系统告警。
// 支持 {var} 占位符；模板内容存于服务端，超管可随时修改并即时生效。
import { useEffect, useState } from 'react'
import { Button, Input, MessagePlugin, Textarea } from 'tdesign-react'
import { useT } from '@/i18n'
import { Panel } from './parts'
import { mailTemplatesGet, mailTemplatesSave, type MailTplItem } from '@/api/admin'

// 邮件模板面板组件（仅超管）：列出全部模板，逐项编辑主题/正文/抄送并保存
export default function MailTplP() {
  const [, t] = useT()
  const [list, setList] = useState<MailTplItem[]>([])
  const [draft, setDraft] = useState<Record<string, MailTplItem>>({})
  const [loaded, setLoaded] = useState(false)
  const [saving, setSaving] = useState<string | null>(null)

  useEffect(() => {
    let alive = true
    mailTemplatesGet()
      .then((j) => {
        if (!alive || !j.success) return
        const arr = Array.isArray(j.templates) ? j.templates : []
        setList(arr)
        const d: Record<string, MailTplItem> = {}
        for (const it of arr) d[it.code] = { ...it }
        setDraft(d)
        setLoaded(true)
      })
      .catch(() => setLoaded(true))
    return () => { alive = false }
  }, [])

  // 修改某模板某字段
  const setField = (code: string, k: 'subject' | 'body' | 'cc', v: string) => {
    setDraft((d) => ({ ...d, [code]: { ...(d[code] || ({} as MailTplItem)), [k]: v } }))
  }

  // 保存单个模板
  const save = async (code: string) => {
    const it = draft[code]
    if (!it) return
    setSaving(code)
    try {
      const j = await mailTemplatesSave({
        [code]: { subject: it.subject, body: it.body, cc: it.cc },
      })
      if (j.success) {
        MessagePlugin.success(t('mailTpl.saved'))
        setList((arr) => arr.map((x) => (x.code === code ? { ...x, subject: it.subject, body: it.body, cc: it.cc, is_modified: true } : x)))
      } else {
        MessagePlugin.error(j.message || 'error')
      }
    } catch (e: any) {
      MessagePlugin.error(e?.message || 'error')
    } finally {
      setSaving(null)
    }
  }

  // 恢复某模板为内置默认（清空自定义）
  const reset = async (code: string) => {
    setSaving(code)
    try {
      const j = await mailTemplatesSave({ [code]: { subject: '', body: '', cc: '' } })
      if (j.success) {
        const def = list.find((x) => x.code === code)
        if (def) {
          setDraft((d) => ({ ...d, [code]: { ...def, is_modified: false } }))
          setList((arr) => arr.map((x) => (x.code === code ? { ...x, is_modified: false } : x)))
        }
        MessagePlugin.success(t('mailTpl.resetOk'))
      } else {
        MessagePlugin.error(j.message || 'error')
      }
    } catch (e: any) {
      MessagePlugin.error(e?.message || 'error')
    } finally {
      setSaving(null)
    }
  }

  return (
    <Panel title={t('mailTpl.title')}>
      <p style={{ fontSize: 13, color: '#667', marginBottom: 14 }}>{t('mailTpl.hint')}</p>
      {!loaded ? (
        <div style={{ color: '#889' }}>…</div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 18, maxWidth: 760 }}>
          {list.map((it) => {
            const d = draft[it.code] || it
            return (
              <div key={it.code} style={{ border: '1px solid #e3e8f2', borderRadius: 10, padding: 14 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
                  <strong style={{ fontSize: 15 }}>{it.name}</strong>
                  <code style={{ fontSize: 12, color: '#889', background: '#f3f5fa', padding: '1px 6px', borderRadius: 4 }}>{it.code}</code>
                  {it.is_modified && (
                    <span style={{ fontSize: 12, color: '#c08a00', background: '#fff6e0', padding: '1px 6px', borderRadius: 4 }}>{t('mailTpl.modified')}</span>
                  )}
                </div>
                <div style={{ fontSize: 12, color: '#889', marginBottom: 10 }}>{it.desc}</div>
                <div style={{ fontSize: 12, color: '#5b6', marginBottom: 10 }}>
                  {t('mailTpl.vars')}：{it.vars.map((v) => `{${v}}`).join('  ')}
                </div>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                  <div>
                    <div style={{ fontSize: 13, marginBottom: 4 }}>{t('mailTpl.subject')}</div>
                    <Input value={d.subject} onChange={(v) => setField(it.code, 'subject', v)} placeholder={it.subject} />
                  </div>
                  <div>
                    <div style={{ fontSize: 13, marginBottom: 4 }}>{t('mailTpl.body')}</div>
                    <Textarea value={d.body} onChange={(v) => setField(it.code, 'body', v)} placeholder={it.body} autosize={{ minRows: 4, maxRows: 10 }} />
                  </div>
                  <div>
                    <div style={{ fontSize: 13, marginBottom: 4 }}>{t('mailTpl.cc')}</div>
                    <Input value={d.cc} onChange={(v) => setField(it.code, 'cc', v)} placeholder={t('mailTpl.ccPlaceholder')} />
                  </div>
                  <div style={{ display: 'flex', gap: 8 }}>
                    <Button theme="primary" loading={saving === it.code} onClick={() => save(it.code)}>{t('mailTpl.save')}</Button>
                    {it.is_modified && (
                      <Button theme="default" variant="outline" loading={saving === it.code} onClick={() => reset(it.code)}>{t('mailTpl.reset')}</Button>
                    )}
                  </div>
                </div>
              </div>
            )
          })}
        </div>
      )}
    </Panel>
  )
}
