// panels/workflow.ts — 翻译流程引擎/evals 评估面板 i18n 键（中英）
// 导出本面板中英双语词典：zh 为对应 i18n key 的中文显示文本，en 为英文显示文本（键一一对应），最终由 i18n/index.ts 合并到全局词典。
export const zh: Record<string, string> = {
  'workflow.title': '流程引擎设置',
  'workflow.hint': '工单翻译流程步骤启停。关闭某步则跳过（审批关闭后工单翻译完成后直接完成）。',
  'workflow.saveFlow': '保存流程配置',
  // ---- evals 评估看板 ----
  'workflow.evalsTitle': 'evals 评估看板',
  'workflow.refresh': '刷新',
  'workflow.colId': 'ID',
  'workflow.colTask': '任务',
  'workflow.colLang': '语言',
  'workflow.colScore': '总分',
  'workflow.colStatus': '状态',
  'workflow.colTime': '时间',
  'workflow.colOutput': '译文',
  'workflow.savedFlow': '流程配置已保存',
}

// 英文文案词典：键与上方 zh 一一对应。
export const en: Record<string, string> = {
  'workflow.title': 'Workflow Engine Settings',
  'workflow.hint': 'Enable/disable steps of the ticket translation workflow. Disabled steps are skipped (with approval off, tickets complete right after translation).',
  'workflow.saveFlow': 'Save Workflow Config',
  // ---- Evals dashboard ----
  'workflow.evalsTitle': 'Evals Dashboard',
  'workflow.refresh': 'Refresh',
  'workflow.colId': 'ID',
  'workflow.colTask': 'Task',
  'workflow.colLang': 'Language',
  'workflow.colScore': 'Score',
  'workflow.colStatus': 'Status',
  'workflow.colTime': 'Time',
  'workflow.colOutput': 'Output',
  'workflow.savedFlow': 'Workflow config saved',
}