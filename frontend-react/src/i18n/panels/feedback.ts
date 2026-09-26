// ============ panels/feedback.ts · 职责说明 ============
// 用户反馈面板 i18n 键
// 导出本面板中英双语词典：zh 为对应 i18n key 的中文显示文本，en 为英文显示文本（键一一对应），最终由 i18n/index.ts 合并到全局词典。
// =============================================
export const zh = {
  'fb.title': '翻译反馈',
  'fb.hint': '你的反馈将直接提交给平台管理员，帮助我们改进翻译质量。',
  'fb.placeholder': '请描述问题，例如：术语翻译不准确 / 语句不通顺 / 漏翻…',
  'fb.withContext': '同意附带翻译上下文（源文与译文）一起提交',
  'fb.submit': '提交反馈',
  'fb.submitting': '提交中…',
  'fb.done': '反馈已提交，感谢您的意见！',
  'fb.entry': '反馈',
  'fb.entryTip': '对这条翻译结果提交反馈',
'fb.panelTitle':'用户反馈',
  'fb.all': '全部',
  'fb.open': '待处理',
  'fb.resolved': '已处理',
  'fb.colTarget': '对象',
  'fb.colContent': '反馈内容',
  'fb.colMode': '模式',
  'fb.colStatus': '状态',
  'fb.targetTicket': '工单',
  'fb.targetText': '文本',
  // ★ O-12（2026-09-26 〇-U 批 I-8）：匿名留资（/api/lead）复用 feedbacks 通道，
  //   行上 TenantID/UserID 都是 0 ⇒ 旧写法把「对象」渲染成「文本」、将用户列显示成 #0，
  //   读起来像脏数据。两键把 lead 如实标出来（不新造假用户、不改落库口径）。
  'fb.targetLead': '留资',
  'fb.userLead': '留资访客（未注册）',
  'fb.ctxBtn': '上下文',
  'fb.resolve': '处理',
  'fb.resolvePrompt': '处理反馈 #{id}（可输入备注）：',
  'fb.empty': '暂无反馈',
}

// 英文文案词典：键与上方 zh 一一对应。
export const en = {
  'fb.title': 'Translation Feedback',
  'fb.hint': 'Your feedback goes directly to platform admins and helps us improve quality.',
  'fb.placeholder': 'Describe the issue, e.g. wrong terminology / awkward phrasing / missing translation…',
  'fb.withContext': 'Attach translation context (source & translations) with this feedback',
  'fb.submit': 'Submit',
  'fb.submitting': 'Submitting…',
  'fb.done': 'Feedback submitted. Thank you!',
  'fb.entry': 'Feedback',
  'fb.entryTip': 'Give feedback on this translation',
'fb.panelTitle':'User Feedback',
  'fb.all': 'All',
  'fb.open': 'Open',
  'fb.resolved': 'Resolved',
  'fb.colTarget': 'Target',
  'fb.colContent': 'Content',
  'fb.colMode': 'Mode',
  'fb.colStatus': 'Status',
  'fb.targetTicket': 'Ticket',
  'fb.targetText': 'Text',
  'fb.targetLead': 'Lead',
  'fb.userLead': 'Lead visitor (unregistered)',
  'fb.ctxBtn': 'Context',
  'fb.resolve': 'Resolve',
  'fb.resolvePrompt': 'Resolve feedback #{id} (optional note):',
  'fb.empty': 'No feedback yet',
}
