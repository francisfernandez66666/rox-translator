// content.js — 划词翻译内容脚本：选中文字 → 浮动按钮 → 调开放 API → 气泡展示
// 配置（服务地址/API Key/目标语言）来自 chrome.storage（popup 页设置）。
(() => {
  let cfg = { baseUrl: "", apiKey: "", langs: "en" };
  let btn = null;
  let bubble = null;
  let selText = "";

  // 启动加载配置，并监听设置变更
  chrome.storage.sync.get(cfg, (saved) => { cfg = { ...cfg, ...saved }; });
  chrome.storage.onChanged.addListener((changes) => {
    for (const k of Object.keys(changes)) cfg[k] = changes[k].cfg ?? changes[k].newValue;
    if (changes.baseUrl) cfg.baseUrl = changes.baseUrl.newValue;
    if (changes.apiKey) cfg.apiKey = changes.apiKey.newValue;
    if (changes.langs) cfg.langs = changes.langs.newValue;
  });

  // 创建浮动翻译按钮（懒创建，复用单例）
  function ensureButton(x, y) {
    if (!btn) {
      btn = document.createElement("div");
      btn.id = "__trz_btn__";
      btn.textContent = "译";
      btn.addEventListener("mousedown", (e) => e.stopPropagation());
      btn.addEventListener("click", (e) => {
        e.preventDefault();
        e.stopPropagation();
        const rect = btn.getBoundingClientRect();
        translateSel(rect.left, rect.bottom + 8);
      });
      document.documentElement.appendChild(btn);
    }
    btn.style.left = x + "px";
    btn.style.top = y + "px";
    btn.style.display = "block";
  }

  // 隐藏按钮与气泡
  function hideAll() {
    if (btn) btn.style.display = "none";
    hideBubble();
  }

  function hideBubble() {
    if (bubble) { bubble.remove(); bubble = null; }
  }

  // 展示气泡（loading 态 → 结果态）
  function showBubble(x, y, text) {
    hideBubble();
    bubble = document.createElement("div");
    bubble.id = "__trz_bubble__";
    bubble.textContent = text || "…";
    document.documentElement.appendChild(bubble);
    const bw = Math.min(420, window.innerWidth - 24);
    bubble.style.width = bw + "px";
    bubble.style.left = Math.max(8, Math.min(x, window.innerWidth - bw - 8)) + "px";
    bubble.style.top = Math.min(y, window.innerHeight - 120) + "px";
  }

  // 调用开放 API 翻译当前选区
  async function translateSel(x, y) {
    if (!selText) return;
    if (!cfg.baseUrl || !cfg.apiKey) {
      showBubble(x, y, "请先点击扩展图标配置服务地址与 API Key");
      return;
    }
    showBubble(x, y, "翻译中…");
    const langs = (cfg.langs || "en").split(",").map(s => s.trim()).filter(Boolean);
    try {
      const resp = await fetch(cfg.baseUrl.replace(/\/+$/, "") + "/openapi/v1/translate", {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: "Bearer " + cfg.apiKey },
        body: JSON.stringify({ text: selText, target_langs: langs }),
      });
      const data = await resp.json();
      if (!data.success || !data.translations) {
        showBubble(x, y, "翻译失败：" + (data.message || resp.status));
        return;
      }
      showBubble(x, y,
        Object.entries(data.translations).map(([lc, v]) => lc + ": " + v).join("\n"));
    } catch (e) {
      showBubble(x, y, "网络错误：" + e.message);
    }
  }

  // 选区监听：mouseup 时若非空白选区且配置就绪则显示按钮
  document.addEventListener("mouseup", (e) => {
    if (e.target === btn || (bubble && bubble.contains(e.target))) return;
    setTimeout(() => {
      const sel = window.getSelection();
      const t = sel ? String(sel).trim() : "";
      if (!t || t.length < 2 || !cfg.baseUrl) { hideAll(); return; }
      selText = t;
      const range = sel.getRangeAt(0).getBoundingClientRect();
      ensureButton(Math.min(range.left, window.innerWidth - 40), Math.max(4, range.top - 36));
    }, 10);
  });

  // 点击空白处隐藏
  document.addEventListener("mousedown", (e) => {
    if (e.target !== btn && (!bubble || !bubble.contains(e.target))) hideAll();
  });
})();
