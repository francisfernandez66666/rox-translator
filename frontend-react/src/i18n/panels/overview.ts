// ============ panels/overview.ts · 职责说明 ============
// 系统看板/仪表盘面板 i18n 键
// 导出本面板中英双语词典：zh 为对应 i18n key 的中文显示文本，en 为英文显示文本（键一一对应），最终由 i18n/index.ts 合并到全局词典。
// =============================================
export const zh: Record<string, string> = {
  'overview.title': '系统看板',
  'overview.refresh': '刷新',
  'overview.exportAuditCsv': '导出审计 CSV',
  'overview.prometheus': '📊 Prometheus 指标',
  'overview.kbEntries': '知识库条目',
  'overview.balance': '组织余额 (token)',
  'overview.flowSteps': '流程步骤启用',
  'overview.usageTypes': '用量类型',
  'overview.breakerOpen': '🔴 熔断',
  'overview.breakerNormal': '🟢 正常',
  'overview.mainModel': '主模型状态',
  'overview.llmErrorRate': 'LLM 错误率',
  'overview.recentAudit': '最近审计日志',
  'overview.colTime': '时间',
  'overview.colAction': '操作',
  'overview.colResource': '资源',
  'overview.colDetail': '详情',
  'overview.colChange': '变更轨迹',
  'overview.diffOldNew': '旧 {old} → 新 {new}',
  'overview.exportFailed': '导出失败',
  'overview.tabSystem': '系统看板',
  'overview.tabUsage': '用量看板',
}

// 英文文案词典：键与上方 zh 一一对应。
export const en: Record<string, string> = {
  'overview.title': 'Dashboard',
  'overview.refresh': 'Refresh',
  'overview.exportAuditCsv': 'Export Audit CSV',
  'overview.prometheus': '📊 Prometheus Metrics',
  'overview.kbEntries': 'KB entries',
  'overview.balance': 'Org balance (tokens)',
  'overview.flowSteps': 'Workflow steps enabled',
  'overview.usageTypes': 'Usage types',
  'overview.breakerOpen': '🔴 Open',
  'overview.breakerNormal': '🟢 Healthy',
  'overview.mainModel': 'Main model status',
  'overview.llmErrorRate': 'LLM error rate',
  'overview.recentAudit': 'Recent audit logs',
  'overview.colTime': 'Time',
  'overview.colAction': 'Action',
  'overview.colResource': 'Resource',
  'overview.colDetail': 'Detail',
  'overview.colChange': 'Change',
  'overview.diffOldNew': 'Old {old} → New {new}',
  'overview.exportFailed': 'Export failed',
  'overview.tabSystem': 'System Board',
  'overview.tabUsage': 'Usage Board',
}
