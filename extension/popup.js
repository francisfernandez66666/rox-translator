// popup.js — 设置页逻辑：读取/保存 chrome.storage.local（不同步上云）
// 安全：API Key 仅存本机（storage.local），回显掩码；服务地址保存时按源申请
//       该服务器的跨域访问权限（optional_host_permissions，替代全站 http/https）
const $ = (id) => document.getElementById(id);

// 从完整 URL 中提取带通配的源（scheme://host[:port]/*），用于按源授权
function originPattern(baseUrl) {
  try {
    const u = new URL(baseUrl);
    const port = u.port ? ":" + u.port : "";
    return `${u.protocol}//${u.hostname}${port}/*`;
  } catch {
    return "";
  }
}

// 打开时回填已保存配置；API Key 不回显明文，仅提示已保存
chrome.storage.local.get({ baseUrl: "", apiKey: "", langs: "en", mode: "fast" }, (cfg) => {
  $("baseUrl").value = cfg.baseUrl;
  $("apiKey").placeholder = cfg.apiKey ? "已保存（留空则不修改）" : "tk_...";
  $("apiKey").value = "";
  $("langs").value = cfg.langs;
  $("mode").value = cfg.mode || "fast";
});

// 保存并提示：Key 留空表示保持原值；为服务地址申请该源跨域权限
$("save").addEventListener("click", () => {
  const baseUrl = $("baseUrl").value.trim().replace(/\/+$/, "");
  const newKey = $("apiKey").value.trim();
  const langs = $("langs").value.trim() || "en";
  const mode = $("mode").value || "fast";

  const apply = (cfg) => {
    chrome.storage.local.set(cfg, () => {
      // 服务地址变更时按源申请授权，跨域翻译才可用（否则 fetch 被浏览器拦截）
      const origin = originPattern(baseUrl);
      if (origin) {
        chrome.permissions.request({ origins: [origin] }, (granted) => {
          const ok = $("ok");
          ok.style.display = "block";
          const hint = granted
            ? "已保存 ✓ 选中网页文字即可翻译"
            : "已保存，但未授予该服务地址访问权限，翻译将失败（请重开或重试授权）";
          ok.textContent = hint;
          setTimeout(() => (ok.style.display = "none"), 3500);
        });
      } else {
        const ok = $("ok");
        ok.style.display = "block";
        ok.textContent = "已保存 ✓（未检测到合法服务地址）";
        setTimeout(() => (ok.style.display = "none"), 2500);
      }
    });
  };

  chrome.storage.local.get({ baseUrl: "", apiKey: "" }, (prev) => {
    apply({
      baseUrl: baseUrl || prev.baseUrl,
      // 留空保留原 Key；填入新值时覆盖
      apiKey: newKey ? newKey : prev.apiKey,
      langs,
      mode,
    });
  });
});