// ============ panels/alerts.ts · 职责说明 ============
// 监控告警面板 i18n 键
// 导出本面板中英双语词典：zh 为对应 i18n key 的中文显示文本，en 为英文显示文本（键一一对应），最终由 i18n/index.ts 合并到全局词典。
// =============================================
export const zh: Record<string, string> = {
  'alerts.title': '监控告警',
  'alerts.refresh': '刷新',
  'alerts.all': '全部',
  'alerts.open': '未处理',
  'alerts.resolved': '已解决',
  'alerts.colLevel': '级别',
  'alerts.colKind': '类型',
  'alerts.colTenant': '组织',
  'alerts.colContent': '内容',
  'alerts.colStatus': '状态',
  'alerts.colTime': '时间',
  'alerts.close': '关闭',
  'alerts.empty': '暂无告警',
}

// 英文文案词典：键与上方 zh 一一对应。
export const en: Record<string, string> = {
  'alerts.title': 'Monitoring Alerts',
  'alerts.refresh': 'Refresh',
  'alerts.all': 'All',
  'alerts.open': 'Open',
  'alerts.resolved': 'Resolved',
  'alerts.colLevel': 'Level',
  'alerts.colKind': 'Type',
  'alerts.colTenant': 'Tenant',
  'alerts.colContent': 'Content',
  'alerts.colStatus': 'Status',
  'alerts.colTime': 'Time',
  'alerts.close': 'Close',
  'alerts.empty': 'No alerts',
}