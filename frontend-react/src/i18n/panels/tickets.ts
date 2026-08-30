// ============ panels/tickets.ts · 职责说明 ============
// 翻译工单/审批台面板 i18n 键
// 导出本面板中英双语词典：zh 为对应 i18n key 的中文显示文本，en 为英文显示文本（键一一对应），最终由 i18n/index.ts 合并到全局词典。
// =============================================
export const zh: Record<string, string> = {
  'tickets.title': '工单工作台',
  'tickets.titlePlaceholder': '标题',
  'tickets.sourcePlaceholder': '源文本（中文）',
  'tickets.targetPlaceholder': '目标语言，逗号分隔 (en,de)',
  'tickets.createTicket': '创建工单',
  'tickets.colNo': '单号',
  'tickets.colTitle': '标题',
  'tickets.colStatus': '状态',
  'tickets.colSource': '源文本',
  'tickets.colTarget': '目标',
  'tickets.colOps': '操作',
  'tickets.run': '运行流程',
  'tickets.detail': '详情',
  'tickets.ticketDetail': '工单 {no} 详情',
  // ---- 审批台 ----
  'tickets.approvalTitle': '审批台',
  'tickets.refresh': '刷新',
  'tickets.approve': '批准',
  'tickets.reasonPlaceholder': '驳回原因',
  'tickets.suggestionPlaceholder': '改进建议',
  'tickets.reject': '驳回',
  'tickets.noApproval': '暂无待审批工单',
  'tickets.errorSourceRequired': '源文本必填',
  'tickets.runDone': '工单 {no} 流程执行完成',
}

// 英文文案词典：键与上方 zh 一一对应。
export const en: Record<string, string> = {
  'tickets.title': 'Ticket Workspace',
  'tickets.titlePlaceholder': 'Title',
  'tickets.sourcePlaceholder': 'Source text (Chinese)',
  'tickets.targetPlaceholder': 'Target languages, comma-separated (en,de)',
  'tickets.createTicket': 'Create Ticket',
  'tickets.colNo': 'No.',
  'tickets.colTitle': 'Title',
  'tickets.colStatus': 'Status',
  'tickets.colSource': 'Source',
  'tickets.colTarget': 'Target',
  'tickets.colOps': 'Actions',
  'tickets.run': 'Run Workflow',
  'tickets.detail': 'Detail',
  'tickets.ticketDetail': 'Ticket {no} Details',
  // ---- 审批台 ----
  'tickets.approvalTitle': 'Approval Desk',
  'tickets.refresh': 'Refresh',
  'tickets.approve': 'Approve',
  'tickets.reasonPlaceholder': 'Reject reason',
  'tickets.suggestionPlaceholder': 'Improvement suggestion',
  'tickets.reject': 'Reject',
  'tickets.noApproval': 'No tickets awaiting approval',
  'tickets.errorSourceRequired': 'Source text is required',
  'tickets.runDone': 'Workflow for ticket {no} completed',
}