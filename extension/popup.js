// popup.js — 设置页逻辑：读取/保存 chrome.storage.sync
// $ —— 根据元素 id 获取 DOM 节点的快捷辅助函数
const $ = (id) => document.getElementById(id);

// 打开时回填已保存配置
chrome.storage.sync.get({ baseUrl: "", apiKey: "", langs: "en", mode: "fast" }, (cfg) => {
  $("baseUrl").value = cfg.baseUrl;
  $("apiKey").value = cfg.apiKey;
  $("langs").value = cfg.langs;
  $("mode").value = cfg.mode || "fast";
});

// 保存并提示
$("save").addEventListener("click", () => {
  chrome.storage.sync.set(
    {
      baseUrl: $("baseUrl").value.trim().replace(/\/+$/, ""),
      apiKey: $("apiKey").value.trim(),
      langs: $("langs").value.trim() || "en",
      mode: $("mode").value || "fast",
    },
    () => {
      const ok = $("ok");
      ok.style.display = "block";
      setTimeout(() => (ok.style.display = "none"), 2500);
    }
  );
});
