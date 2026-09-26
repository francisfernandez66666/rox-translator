// ============================================================================
// translator-sdk.mjs — 翻译助手开放 API JavaScript SDK（零依赖，fetch/Node18+/浏览器通用）
// 对接端点（异步任务模型）：
//   POST /openapi/v1/tasks           创建任务（JSON=文本；multipart=文件批量）
//   GET  /openapi/v1/tasks/status    轮询状态
//   GET  /openapi/v1/tasks/download  文件产物下载
//   GET  /openapi/v1/balance         查询积分余额与 ≈句数
// 用法示例：
//   import { TranslatorClient } from "./translator-sdk.mjs";
//   const cli = new TranslatorClient("https://translator.example.com", "rk_xxx");
//
//   // 文本翻译：提交 → 自动轮询（15s）→ 译文
//   const r = await cli.translateAndWait("蓝牙钥匙已激活", ["en", "ja"]);
//   console.log(r.translations);
//
//   // 文件批量：提交 → 轮询（60s）→ 下载产物
//   const t = await cli.createFileTask(files, ["en"], "pro");
//   await cli.waitTask(t.task_id);
//   await cli.downloadFile(t.task_id, "./out.zip");
// ============================================================================

export class TranslatorError extends Error {
  constructor(message, status, body, retryAfter) {
    super(message);
    this.name = "TranslatorError";
    this.status = status;
    // ★ F-64①（2026-09-26）：错误码正主键是 code（与文档 Error.code 枚举一致），
    // error_code 是状态码诚实改造**之前**的老服务端在发的别名；改造后的服务端只发 code。
    // 这里"code 优先、error_code 兜底"⇒ 混跑窗口里打老服务端也照样取得到码。
    // errorCode 属性名保留（历史公开面，改名＝打断在用浏览器 SDK 的接入方）。
    this.code = (body && typeof body === "object" && (body.code || body.error_code)) || null;
    this.errorCode = this.code;
    this.retryAfter = retryAfter ?? null; // 429 时还需等待的秒数，非限流为 null
    this.body = body;
  }
}

/**
 * 从错误响应取「还需等待秒数」：JSON 字段 retry_after 优先，HTTP Retry-After 头兜底
 * （中间层可能只透传其中之一）。只认纯数字秒，取不到给 null。
 */
function pickRetryAfter(body, resp) {
  let raw = body && typeof body === "object" ? body.retry_after : undefined;
  if (raw === undefined && resp?.headers?.get) {
    try { raw = resp.headers.get("Retry-After") ?? resp.headers.get("retry-after"); } catch { raw = undefined; }
  }
  const n = typeof raw === "number" ? raw : Number(typeof raw === "string" ? raw.trim() : NaN);
  return Number.isFinite(n) && n > 0 ? n : undefined;
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

export class TranslatorClient {
  /**
   * @param {string} baseUrl 服务地址，如 https://translator.example.com
   * @param {string} apiKey  开放 API Key（管理后台签发）
   * @param {number} timeoutMs 单请求超时毫秒（默认 30000）
   */
  constructor(baseUrl, apiKey, timeoutMs = 30000) {
    this.base = String(baseUrl).replace(/\/+$/, "");
    this.apiKey = apiKey;
    this.timeoutMs = timeoutMs;
  }

  /** @internal 统一 fetch（JSON 响应 + 错误码提取） */
  async #fetch(path, init) {
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), this.timeoutMs);
    let resp;
    try {
      resp = await fetch(this.base + path, {
        ...init,
        headers: { Authorization: `Bearer ${this.apiKey}`, ...(init.headers || {}) },
        signal: ctrl.signal,
      });
    } catch (e) {
      throw new TranslatorError(`连接失败: ${e.message}`);
    } finally {
      clearTimeout(timer);
    }
    const text = await resp.text();
    let data;
    try {
      data = JSON.parse(text);
    } catch {
      if (!resp.ok) throw new TranslatorError(`HTTP ${resp.status}: ${text}`, resp.status, text);
      return text; // 二进制/文本响应由调用方处理（download 单独走 raw 分支）
    }
    // ★ F-64①：失败现在带真实状态码，下面的 !resp.ok 分支是主通道；
    // 仍保留"2xx + success:false"这条判据，是因为新前端 SDK 会打到尚未升级的老服务端
    // （混跑窗口），那时失败确实伪装成 200。
    if (!resp.ok || (data && data.success === false)) {
      throw new TranslatorError(
        (data && (data.message || data.error)) || `HTTP ${resp.status}`,
        resp.status, data, pickRetryAfter(data, resp));
    }
    return data;
  }

  #postJson(path, payload) {
    return this.#fetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
  }

  // ---------- 任务创建 ----------
  /** 创建文本翻译任务。mode: "pro"(默认)/"fast"。返回 {task_id, mode, type:"text", status:"queued"}（202）。 */
  createTask(text, targetLangs, mode = "pro", title = "") {
    const body = { text, mode };
    if (targetLangs) body.target_langs = targetLangs;
    if (title) body.title = title;
    return this.#postJson("/openapi/v1/tasks", body);
  }

  /** 创建文件批量翻译任务（≤20 个 / ≤40MB）。files: File|Blob[]（带 name）。 */
  async createFileTask(files, targetLangs, mode = "pro", title = "") {
    const fd = new FormData();
    for (const f of files) fd.append("files", f, f.name || `file-${Date.now()}`);
    fd.append("target_langs", (targetLangs || []).join(",") || "en");
    fd.append("mode", mode);
    if (title) fd.append("title", title);
    const r = await this.#fetch("/openapi/v1/tasks", { method: "POST", body: fd });
    // ★ 契约对齐（2026-08-26 全仓评审 D1）：成功响应无 success 字段，以 task_id 为准
    //  （★ F-64①：超限/缺文件等业务失败已由 #fetch 按真实状态码抛出，
    //    这里只剩「2xx 空壳」兜底——中间层把失败改写成 200 时不许默默返回）
    if (!r || r.task_id == null) throw new TranslatorError(r?.message || "创建任务失败", 200, r);
    return r;
  }

  /** 查询任务状态。status ∈ queued/processing/completed/failed（失败带 error_code/message）。 */
  getTask(taskId) {
    return this.#fetch(`/openapi/v1/tasks/status?id=${taskId}`);
  }

  /** 阻塞轮询直至终态；interval 毫秒，缺省按任务类型（文本 15s / 文件 60s，与后端口径一致）。 */
  async waitTask(taskId, interval = null, timeoutMs = 30 * 60 * 1000) {
    const deadline = Date.now() + timeoutMs;
    let gapMs = interval;
    for (;;) {
      const r = await this.getTask(taskId);
      if (r.status === "completed") return r;
      if (r.status === "failed") {
        // ★ 契约对齐（D1）：失败出参字段为 message/error_code（无 error 字段）
        //   ★ F-64①：这里的 200 是**对的**——失败发生在任务执行期，请求本身成功，
        //   status:"failed" 是任务状态而非 HTTP 失败（对外文档已按此口径写明）。
        throw new TranslatorError(r.message || "任务失败", 200, r);
      }
      if (Date.now() > deadline) throw new TranslatorError("轮询超时，任务仍在处理");
      if (!gapMs) gapMs = r.type === "files" ? 60000 : 15000;
      await sleep(gapMs);
    }
  }

  /** 一站式文本翻译：提交 + 等待完成，返回含 translations 的响应。 */
  async translateAndWait(text, targetLangs, mode = "pro", timeoutMs) {
    const created = await this.createTask(text, targetLangs, mode);
    return this.waitTask(created.task_id, null, timeoutMs);
  }

  /** 下载文件任务产物（fileId 缺省打包 zip），保存为 Blob 由调用方落地。 */
  async downloadFile(taskId, fileId = null) {
    const qs = `/openapi/v1/tasks/download?id=${taskId}` + (fileId ? `&file_id=${fileId}` : "");
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), this.timeoutMs * 10); // 大文件放宽超时
    try {
      const resp = await fetch(this.base + qs, {
        headers: { Authorization: `Bearer ${this.apiKey}` },
        signal: ctrl.signal,
      });
      if (!resp.ok) {
        // ★ F-64①：旧写法只给一行"下载失败 HTTP 409"，错误体整个丢掉 ⇒ 调用方分不清
        // "产物还没翻完（该等）"、"Key 没权限（该找管理员）"与"任务 id 写错（该改代码）"，
        // 也拿不到 429 的退避秒数。这里把 message/code/retryAfter 补回来。
        const raw = await resp.text().catch(() => "");
        let data;
        try { data = raw ? JSON.parse(raw) : undefined; } catch { data = undefined; }
        throw new TranslatorError(
          (data && (data.message || data.error)) || `下载失败 HTTP ${resp.status}`,
          resp.status, data ?? raw, pickRetryAfter(data, resp));
      }
      return await resp.blob();
    } finally {
      clearTimeout(timer);
    }
  }

  /** 查询积分余额与 ≈句数：{balance_points, balance_sentences_approx}。 */
  balance() {
    return this.#fetch("/openapi/v1/balance");
  }

  /** 知识库统计（需 kb 权限）。 */
  kbStats() {
    return this.#postJson("/openapi/v1/kb/stats", {});
  }

  /** 用量汇总（需 billing 权限）。 */
  usage() {
    return this.#postJson("/openapi/v1/billing/usage", {});
  }

  /** 轮换当前 Key（旧 Key 立即失效）。 */
  rotateApiKey() {
    return this.#postJson("/openapi/v1/apikey/rotate", {});
  }
}
