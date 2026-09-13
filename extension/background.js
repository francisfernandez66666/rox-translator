// background.js — 服务工作者（★ F10）：快捷键命令路由到当前标签页内容脚本
chrome.commands.onCommand.addListener((cmd) => {
  if (cmd !== "translate-selection") return;
  chrome.tabs.query({ active: true, currentWindow: true }, (tabs) => {
    if (tabs[0] && tabs[0].id != null) {
      chrome.tabs.sendMessage(tabs[0].id, { type: "trz:shortcut-translate" });
    }
  });
});
