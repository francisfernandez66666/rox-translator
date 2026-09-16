// content.js — 划词翻译内容脚本：选中文字 → 浮动按钮 → 调开放 API → 气泡展示
// 配置（服务地址/API Key/目标语言）来自 chrome.storage.local（popup 页设置）。
// 安全加固（2026-09-04）：storage.sync → storage.local；所有 DOM 注入统一 textContent；
//      远程请求仅允许投递到「已授权源」（与 popup 申请的 optional_host_permissions 一致）。
(() => {
  let cfg = { baseUrl: "", apiKey: "", langs: "en" };
  let btn = null;
  let bubble = null;
  let selText = "";

  // 启动加载配置，并监听设置变更
  chrome.storage.local.get(cfg, (saved) => { cfg = { ...cfg, ...saved }; });
  chrome.storage.onChanged.addListener((changes) => {
    for (const k of Object.keys(changes)) cfg[k] = changes[k].newValue;
  });

  // 目标服务地址安全预检：仅允许 http/https 且可解析为合法 origin。
  // 说明：content script 无法枚举自身已授权源（由 popup 按源申请 optional_host_permissions，
  //      跨域 fetch 由浏览器 CORS/授权机制拦截），此处负责挡掉协议走私与注入源。
  function isTrustedBaseUrl(url) {
    try {
      const u = new URL(url);
      if (u.protocol !== "http:" && u.protocol !== "https:") return false;
      if (!u.hostname) return false;
      return true;
    } catch { return false; }
  }

  // 确保浮动「译」按钮存在并定位到选区上方
  // 参数：x, y —— 按钮左上角坐标（CSS 像素）
  // 说明：懒创建单例按钮并挂到 documentElement；点击后调用 translateSel 发起翻译
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

  // 隐藏按钮与翻译气泡（点击空白处时调用）
  function hideAll() {
    if (btn) btn.style.display = "none";
    hideBubble();
    clearMarks(); // ★ F10：气泡收起同时清除选区高亮回显
  }

  function hideBubble() {
    if (bubble) { bubble.remove(); bubble = null; }
  }

  // 展示翻译气泡（先销毁旧气泡再创建新气泡）
  // 参数：x, y —— 气泡左上角坐标（CSS 像素）；text —— 显示内容（可为 loading 提示或译文）
  // 说明：根据视口宽度自适应气泡宽度，并做边界收敛避免溢出
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

  // 调用开放 API 翻译当前选区文本
  // 参数：x, y —— 结果气泡定位坐标（若未配置服务地址/API Key 则给出提示）
  // 行为：展示 loading 气泡 → POST /openapi/v1/translate → 将各语言译文渲染到气泡；
  //      失败时区分超时/网络错误/业务错误给出对应提示
  async function translateSel(x, y) {
    if (!selText) return;
    if (!cfg.baseUrl || !cfg.apiKey) {
      showBubble(x, y, "请先点击扩展图标配置服务地址与 API Key");
      return;
    }
    // 安全加固：仅允许投递到 http/https 且来源可解析的地址，防协议走私与注入源
    if (!isTrustedBaseUrl(cfg.baseUrl)) {
      showBubble(x, y, "服务地址不合法，请检查配置（仅支持 http/https）");
      return;
    }
    showBubble(x, y, "翻译中…");
    const langs = (cfg.langs || "en").split(",").map(s => s.trim()).filter(Boolean);
    try {
      // ★ 整改 R-L3：翻译模式尊重用户设置（cfg.mode，缺省 fast 保证划译秒级体验），
      //   专业套餐用户可在设置中选 pro 以启用校对+知识库；不再硬编码 fast 丢失质量。
      const mode = cfg.mode || "fast";
      const resp = await fetch(cfg.baseUrl.replace(/\/+$/, "") + "/openapi/v1/translate", {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: "Bearer " + cfg.apiKey },
        body: JSON.stringify({ text: selText, target_langs: langs, mode }),
        signal: (typeof AbortSignal !== "undefined" && AbortSignal.timeout)
          ? AbortSignal.timeout(30000) : undefined,
      });
      const data = await resp.json();
      if (!data.success || !data.translations) {
        const code = data.error_code || "";
        const hint = ERR_HINT[code] || (data.message || ("HTTP " + resp.status));
        showBubble(x, y, "翻译失败" + (code ? "（" + code + "）" : "") + "：" + hint);
        return;
      }
      // ★ F10：气泡增强——译文 + 模式/消耗回显 + 原文对照，并高亮页面选区
      highlightSel();
      const lines = Object.entries(data.translations).map(([lc, v]) => lc + ": " + v);
      if (data.mode) lines.push("模式: " + data.mode);
      if (data.tokens_used) lines.push("消耗: " + data.tokens_used + " token");
      if (data.source_text) lines.push("原文: " + data.source_text);
      showBubble(x, y, lines.join("\n"));
    } catch (e) {
      showBubble(x, y, e.name === "TimeoutError" ? "翻译超时，请重试或缩短选区" : ("网络错误：" + e.message));
    }
  }

  // ★ F10：错误码提示表（与前端 E11 同口径：稳定 error_code → 用户可读指引）
  const ERR_HINT = {
    invalid_api_key: "API Key 无效或已吊销，请到扩展设置更新",
    key_quota_exceeded: "该 API Key 当日配额已用完",
    insufficient_balance: "账户余额不足，请充值或升级套餐",
    forbidden: "API Key 无翻译权限",
    text_too_long: "选区过长超出单次限制，请缩短选区",
    rate_limited: "请求过于频繁，稍后重试",
    no_result: "翻译无结果，请重试",
    task_failed: "翻译任务失败，请重试",
  };

  // ★ F10：术语/结果回显——翻译成功后高亮当前选区（mark 包裹），下一次翻译或点击空白清除
  let markEls = [];
  function clearMarks() {
    for (const m of markEls) { const p = m.parentNode; if (p) { p.replaceChild(document.createTextNode(m.textContent), m); p.normalize(); } }
    markEls = [];
  }
  function highlightSel() {
    clearMarks();
    try {
      const sel = window.getSelection();
      if (!sel || sel.isCollapsed || !sel.rangeCount) return;
      const range = sel.getRangeAt(0);
      if (range.collapsed || range.startContainer !== range.endContainer) return; // 跨节点选区不做破坏性包裹
      const mk = document.createElement("mark");
      mk.className = "__trz_mark__";
      mk.appendChild(range.extractContents());
      range.insertNode(mk);
      markEls.push(mk);
    } catch { /* 受保护 DOM 忽略 */ }
  }

  // ★ F10：快捷键（background.js 命令转发）——对当前选区直接翻译
  chrome.runtime.onMessage.addListener((msg) => {
    if (!msg || msg.type !== "trz:shortcut-translate") return;
    const sel = window.getSelection();
    const t = sel ? String(sel).trim() : "";
    if (!t) return;
    selText = t;
    translateSel(window.innerWidth / 2 - 200, Math.max(60, window.innerHeight / 3));
  });

  // 选区监听：mouseup 时若非空白选区且配置就绪则显示按钮
  document.addEventListener("mouseup", (e) => {
    if (e.target === btn || (bubble && bubble.contains(e.target))) return;
    setTimeout(() => {
      const sel = window.getSelection();
      const t = sel ? String(sel).trim() : "";
      if (!t || t.length < 2 || !cfg.baseUrl) { hideAll(); return; }
      selText = t;
      // ★ 健壮性（2026-09-16）：异常选区下 getRangeAt 可能抛错，兜底隐藏而非未捕获异常
      let range;
      try { range = sel.getRangeAt(0).getBoundingClientRect(); }
      catch { hideAll(); return; }
      ensureButton(Math.min(range.left, window.innerWidth - 40), Math.max(4, range.top - 36));
    }, 10);
  });

  // 点击空白处隐藏
  document.addEventListener("mousedown", (e) => {
    if (e.target !== btn && (!bubble || !bubble.contains(e.target))) hideAll();
  });
})();
