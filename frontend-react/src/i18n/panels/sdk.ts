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
  // ★ F-27（2026-09-25）订正假话：SDK 包从未发布到 PyPI / npm 公共仓库，「同步发版」是不实承诺；
  //   现口径 = 本地交付物由本站托管下载（scripts/build_sdk.sh 产出进 public/sdk/），真发公共仓库属运营动作。
  'sdk.changelogHint': '版本变更记录见仓库 sdk/CHANGELOG.md；安装包由本站下载分发，未发布到 PyPI / npm 公共仓库。',
  // ★ F-27 新键：安装命令与下载链接均指向本站托管的本地交付物（文件名运行时取自 public/sdk/manifest.json）
  'sdk.srcHint': '安装命令与下载均指向本站托管的本地交付包（文件名见各卡片），无需从 PyPI / npm 公共仓库安装。',
  'sdk.downloadPython': '下载 wheel 包：{name}',
  'sdk.downloadTs': '下载 npm 包：{name}',
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
  // ★ F-27（2026-09-25）en 侧同步订正：与 zh 同口径——包由本站分发，公共仓库未发布（parity 闸要求双语逐键对等）。
  'sdk.changelogHint': 'See sdk/CHANGELOG.md in the repo; SDK packages are distributed from this site and are not published to public PyPI / npm registries.',
  // ★ F-27 新键（en）：{name} 占位符由 tpl() 填真实托管文件名
  'sdk.srcHint': 'Install commands and download links below refer to packages hosted on this site (file names shown on each card), not to public PyPI / npm registries.',
  'sdk.downloadPython': 'Download wheel: {name}',
  'sdk.downloadTs': 'Download npm package: {name}',
  'sdk.extTitle': 'Browser selection-translation extension',
  'sdk.extDesc': 'Select text on any page to translate it with the same API Key. Unzip the download, enable Developer mode on the extensions page, then choose "Load unpacked" and pick that folder. Full steps are in INSTALL.txt inside the zip.',
  'sdk.extDownload': 'Download extension zip',
}
