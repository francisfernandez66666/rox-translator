// ============ panels/sdk.ts · 职责说明 ============
// 官方 SDK 集成面板（外部调用 Hub 第三子 tab）i18n 键。
// 导出 zh/en 双语词典，由 i18n/index.ts 合并到全局词典。
// =============================================
export const zh: Record<string, string> = {
  'sdk.title': '官方 SDK',
  'sdk.hint': '三端官方 SDK 与开放 API 同一契约（同一 API Key 鉴权）。安装后按下例初始化客户端即可调用同步短文翻译与文件工单。',
  'sdk.name.python': 'Python',
  'sdk.name.typescript': 'TypeScript / JS',
  'sdk.name.java': 'Java',
  'sdk.copy': '复制',
  'sdk.copied': '已复制',
  'sdk.keysHint': 'API Key 在「开放 API」子 tab 创建（仅展示一次，请妥存）。',
  'sdk.changelogHint': '版本变更记录见仓库 sdk/CHANGELOG.md；npm / PyPI 同步发版。',
}

// 英文文案字典（与 zh 同 key 对齐，parity.test.ts 守护双语一致性）
export const en: Record<string, string> = {
  'sdk.title': 'Official SDKs',
  'sdk.hint': 'Official SDKs share the OpenAPI contract and API Key auth. Install, initialize the client, then call synchronous text translation or file tickets.',
  'sdk.name.python': 'Python',
  'sdk.name.typescript': 'TypeScript / JS',
  'sdk.name.java': 'Java',
  'sdk.copy': 'Copy',
  'sdk.copied': 'Copied',
  'sdk.keysHint': 'Create an API Key in the "Open API" tab (shown once, keep it safe).',
  'sdk.changelogHint': 'See sdk/CHANGELOG.md in the repo; npm / PyPI are released together.',
}
