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
  // ★ 2026-09-23 补扩展交付链：插件 zip 由 scripts/build_extension.sh 打进 public/extensions/，
  //   页面只挂 latest 固定名（版本号以 extension/manifest.json 为唯一事实源，界面不再复刻一份）
  'sdk.extTitle': '浏览器划词插件',
  'sdk.extDesc': '选中网页文字即翻，走同一 API Key。下载 zip 解压后，在扩展页开启「开发者模式」→「加载已解压的扩展程序」选该目录；详细说明见包内 INSTALL.txt。',
  'sdk.extDownload': '下载插件 zip',
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
  'sdk.extTitle': 'Browser selection-translation extension',
  'sdk.extDesc': 'Select text on any page to translate it with the same API Key. Unzip the download, enable Developer mode on the extensions page, then choose "Load unpacked" and pick that folder. Full steps are in INSTALL.txt inside the zip.',
  'sdk.extDownload': 'Download extension zip',
}
