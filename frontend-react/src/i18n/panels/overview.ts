// ============ panels/overview.ts · 职责说明 ============
// 系统看板/仪表盘面板 i18n 键
// 导出本面板中英双语词典：zh 为对应 i18n key 的中文显示文本，en 为英文显示文本（键一一对应），最终由 i18n/index.ts 合并到全局词典。
// =============================================
export const zh: Record<string, string> = {
  'overview.title': '系统看板',
  'overview.refresh': '刷新',
  'overview.exportAuditCsv': '导出审计 CSV',
// ★ F-20/F-39（2026-09-25 UAT 修复批G）：本键的界面入口（管理台总览 Prometheus 按钮）已按 F-20 移除——
//   /metrics 属平台运维面信息，不再暴露给租户管理员；键保留不删（删键需 12 语种文件同步手术、零收益）。
'overview.prometheus':'Prometheus 指标',
  'overview.kbEntries': '知识库条目',
  // ★ F-16（批G 前端半）标签语义订正：总览余额卡改读双桶合计 total_points（免费/体验+付费永久），
  // 值同步「组织余额合计 (积分)」；十语种 locales 同键由后续序列组落地同口径译文。
  'overview.balance': '组织余额合计 (积分)',
  'overview.flowSteps': '流程步骤启用',
  'overview.usageTypes': '用量类型',
'overview.breakerOpen':'熔断',
'overview.breakerNormal':'正常',
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
// ★ F-20/F-39（2026-09-25 UAT 修复批G）：本键的界面入口（管理台总览 Prometheus 按钮）已按 F-20 移除
//   （/metrics 不再暴露给租户管理员）；键保留不删，理由同上 zh 侧注释。
'overview.prometheus':'Prometheus Metrics',
  'overview.kbEntries': 'KB entries',
  // ★ F-16（批G 前端半）标签语义订正（与上方 zh 同口径）：余额卡读数改为双桶合计 total_points。
  'overview.balance': 'Total org balance (credits)',
  'overview.flowSteps': 'Workflow steps enabled',
  'overview.usageTypes': 'Usage types',
'overview.breakerOpen':'Open',
'overview.breakerNormal':'Healthy',
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
