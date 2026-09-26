// ============================================================================
// index.ts — 翻译助手开放 API TypeScript SDK（零第三方依赖，Node 18+）
// ★ 环境说明（2026-09-16 修订宣称）：文本任务/轮询/余额等在浏览器可用；
//   createFileTask / downloadFile 依赖 Node fs 与 Buffer，仅限 Node 环境。
//   浏览器端文件任务请使用 @langcross/translator-sdk (js) 的 FormData 变体。
// 对接端点（异步任务模型）：
//   POST /openapi/v1/tasks           创建任务（JSON=文本；multipart=文件批量）
//   GET  /openapi/v1/tasks/status    轮询状态（未完成 status=queued/processing）
//   GET  /openapi/v1/tasks/download  文件产物下载
//   GET  /openapi/v1/balance         查询积分余额与 ≈句数
//   POST /openapi/v1/kb/stats · /openapi/v1/billing/usage · /openapi/v1/apikey/rotate
// 认证方式：Bearer Token（Authorization: Bearer <api_key>），在管理后台「API Key」面板签发
//
// 快速上手：
//   import { TranslatorClient } from "@langcross/translator-sdk";
//   const cli = new TranslatorClient("https://translator.example.com", "rk_xxx");
//
//   // 文本翻译：提交任务 → 自动轮询（15s）→ 返回译文
//   const r = await cli.translateAndWait("蓝牙钥匙已激活", ["en", "ja"]);
//   console.log(r.translations);  // {en: "...", ja: "..."}
//
//   // 文件批量翻译：提交 → 自动轮询（60s）→ 下载产物到目录
//   const t = await cli.createFileTask(files, ["en"], "pro");
//   await cli.waitTask(t.task_id);
//   await cli.downloadFile(t.task_id, "./out.zip");
//
// 错误处理（★ 2026-09-26 F-64① 状态码诚实改造后的口径）：
//   对外契约面的失败一律以**真实 HTTP 状态码**发出（400/401/402/403/404/409/429/500），
//   响应体带 code（正主，与文档 Error.code 枚举一致）。error_code 只是**状态码诚实改造之前的老服务端**
//   在发的别名；改造后的服务端统一只发 code（不再同值下发，避免两套码名并存）。
//   TranslatorError 把两者都收敛到 .code（.error_code 保留兼容，勿删）；
//   限流类（429）另给 .retryAfter 秒数，调用方按它退避即可。
//   唯一仍在 200 里表达"没做成"的是**任务状态**：status:"failed" 是业务对象的状态，
//   不是本次请求失败，所以 getTask/waitTask 正常返回、由 status 字段分支。
// ============================================================================

/** 任务创建成功响应 */
export interface TaskCreated {
  task_id: number;   // 任务 ID（用于轮询/下载）
  mode: string;      // 翻译模式（fast/pro）
  type: string;      // 任务类型（text/files）
  status: string;    // 初始状态（queued）
  file_count?: number; // 文件数量（仅文件任务）
}

/** 任务状态响应 */
export interface TaskStatus {
  task_id: number;
  status: "queued" | "processing" | "completed" | "failed"; // 任务状态
  progress?: number;    // 进度百分比（0-100）
  error_code?: string;  // 错误码（如 insufficient_balance）
  message?: string;     // 错误信息
  files?: { file_id: number; name: string; url: string }[]; // 文件列表（仅文件任务完成时）
}

/** 余额查询响应 */
export interface Balance {
  balance_points: number; // 剩余积分数
}

/** 翻译 API 调用异常 */
export class TranslatorError extends Error {
  status?: number;        // HTTP 状态码（★ F-64①：失败时即真实状态码，不再恒 200）
  code?: string;          // 业务错误码正主（与文档 Error.code 枚举同名同值）
  error_code?: string;    // 老服务端（状态码诚实改造之前）在发的别名；保留只为不打断在生产的接入方
  retryAfter?: number;    // 429 时还需等待的秒数（JSON retry_after 优先，Retry-After 头兜底）
  body?: unknown;         // 原始响应体
  constructor(message: string, status?: number, error_code?: string, body?: unknown, retryAfter?: number) {
    super(message);
    this.name = "TranslatorError";
    this.status = status;
    this.error_code = error_code;
    this.code = error_code;
    this.retryAfter = retryAfter;
    this.body = body;
  }
}

/**
 * 从错误响应体/头里取「还需等待秒数」。
 * 两处给法（字段给应用、头给通用 HTTP 客户端）本应同值，但中间层可能只透传其中之一，故都读；
 * 只认纯数字秒（本服务不发 HTTP-date 形态），取不到就 undefined——宁可少给一个退避提示，
 * 也不把日期串丢给调用方去 Number()。
 */
function pickRetryAfter(data: any, headers?: { get?: (k: string) => string | null }): number | undefined {
  let raw: unknown = undefined;
  if (data && typeof data === "object" && "retry_after" in data) raw = (data as any).retry_after;
  if (raw === undefined && headers && typeof headers.get === "function") {
    try { raw = headers.get("Retry-After") ?? headers.get("retry-after"); } catch { raw = undefined; }
  }
  const n = typeof raw === "number" ? raw : Number(typeof raw === "string" ? raw.trim() : NaN);
  return Number.isFinite(n) && n > 0 ? n : undefined;
}

/**
 * 翻译助手开放 API 客户端。
 *
 * @param baseUrl - 服务地址，如 https://translator.example.com
 * @param apiKey - 开放 API Key（管理后台签发）
 * @param timeout - 单请求超时秒数（默认 30）
 */
export class TranslatorClient {
  private baseUrl: string;
  private apiKey: string;
  private timeout: number;

  constructor(baseUrl: string, apiKey: string, timeout = 30) {
    this.baseUrl = baseUrl.replace(/\/+$/, "");
    this.apiKey = apiKey;
    this.timeout = timeout * 1000;
  }

  /**
   * 内部统一请求方法（JSON 响应 + 错误码提取）
   * @param method - HTTP 方法（GET/POST）
   * @param path - API 路径（如 /openapi/v1/tasks）
   * @param body - 请求体（可选）
   * @param contentType - Content-Type（可选）
   * @returns 解析后的 JSON 对象
   */
  private async request(method: string, path: string, body?: BodyInit, contentType?: string): Promise<any> {
    const headers: Record<string, string> = { "Authorization": `Bearer ${this.apiKey}` };
    if (contentType) headers["Content-Type"] = contentType;
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.timeout);
    try {
      const resp = await fetch(this.baseUrl + path, {
        method,
        headers,
        body,
        signal: controller.signal,
      });
      const text = await resp.text();
      const data = text ? JSON.parse(text) : {};
      if (!resp.ok) {
        // ★ F-64①：错误码正主是 code（文档 Error.code），error_code 是状态码诚实改造**之前**的老服务端别名；
        // 改造后的服务端只发 code ⇒ "code 优先、error_code 兜底"，混跑窗口两边都拿得到。
        throw new TranslatorError(
          (data && (data.message || data.error)) || `HTTP ${resp.status}`,
          resp.status,
          (data && (data.code || data.error_code)) || undefined,
          data,
          pickRetryAfter(data, resp.headers)
        );
      }
      return data;
    } catch (e: any) {
      if (e instanceof TranslatorError) throw e;
      throw new TranslatorError("连接失败: " + e.message);
    } finally {
      clearTimeout(timer);
    }
  }

  /**
   * 创建文本翻译任务（异步）。
   *
   * @param text - 源文本（必填）
   * @param targetLangs - 目标语言代码列表，缺省 ["en"]
   * @param mode - "pro" 专业校对（默认）/ "fast" 快速（无知识库）
   * @param title - 任务标题（可选）
   * @returns {task_id, mode, type, status:"queued"}（202 响应）
   */
  async createTask(text: string, targetLangs: string[] = ["en"], mode = "pro", title = ""): Promise<TaskCreated> {
    const r: any = await this.request("POST", "/openapi/v1/tasks", JSON.stringify({ text, target_langs: targetLangs, mode, title }), "application/json");
    if (r.task_id === undefined) throw new TranslatorError(r.message || "创建任务失败", undefined, r.error_code, r);
    return r;
  }

  /**
   * 创建文件批量翻译任务（≤20 个，总量 ≤40MB）。
   *
   * @param files - 本地文件路径数组（Node 环境）
   * @param targetLangs - 目标语言代码列表，缺省 ["en"]
   * @param mode - "pro" / "fast"
   * @param title - 任务标题（可选）
   * @returns 同 createTask，另含 file_count
   */
  async createFileTask(files: string[], targetLangs: string[] = ["en"], mode = "pro", title = ""): Promise<TaskCreated> {
    const fs = require("fs") as typeof import("fs");
    const boundary = "----TranslatorSDKBoundary" + Math.random().toString(16).slice(2);
    const chunks: Buffer[] = [];
    for (const p of files) {
      const data = fs.readFileSync(p);
      const name = p.split("/").pop()!.split("\\").pop()!;
      chunks.push(Buffer.from(`--${boundary}\r\n`));
      chunks.push(Buffer.from(`Content-Disposition: form-data; name="files"; filename="${name}"\r\n`));
      chunks.push(Buffer.from("Content-Type: application/octet-stream\r\n\r\n"));
      chunks.push(data);
      chunks.push(Buffer.from("\r\n"));
    }
    for (const [k, v] of [["target_langs", targetLangs.join(",") || "en"], ["mode", mode], ["title", title]] as const) {
      chunks.push(Buffer.from(`--${boundary}\r\n`));
      chunks.push(Buffer.from(`Content-Disposition: form-data; name="${k}"\r\n\r\n`));
      chunks.push(Buffer.from(v + "\r\n"));
    }
    chunks.push(Buffer.from(`--${boundary}--\r\n`));
    const body = Buffer.concat(chunks);
    const r: any = await this.request("POST", "/openapi/v1/tasks", body, `multipart/form-data; boundary=${boundary}`);
    if (r.task_id === undefined) throw new TranslatorError(r.message || "创建任务失败", undefined, r.error_code, r);
    return r;
  }

  /**
   * 查询任务状态。
   *
   * @param taskId - 任务 ID
   * @returns 任务状态（status ∈ queued/processing/completed/failed）
   */
  async getTask(taskId: number): Promise<TaskStatus> {
    const r: any = await this.request("GET", `/openapi/v1/tasks/status?id=${taskId}`);
    if (r.status === undefined) throw new TranslatorError(r.message || "查询失败", undefined, r.error_code, r);
    return r;
  }

  /**
   * 阻塞等待任务完成（默认按类型 15s/60s 轮询）。
   *
   * @param taskId - 任务 ID
   * @param interval - 轮询间隔秒数（缺省按任务类型：文本 15s / 文件 60s）
   * @param timeoutSec - 总超时秒数（默认 3600 = 1 小时）
   * @returns 终态任务状态（completed/failed）
   */
  async waitTask(taskId: number, interval?: number, timeoutSec = 3600): Promise<TaskStatus> {
    const deadline = Date.now() + timeoutSec * 1000;
    // eslint-disable-next-line no-constant-condition
    while (true) {
      const st = await this.getTask(taskId);
      if (st.status === "completed" || st.status === "failed") return st;
      if (Date.now() > deadline) throw new TranslatorError("等待任务超时", undefined, "timeout");
      // 首轮按任务类型定默认间隔（文件任务产物大，60s 更合理）
      // ★ 修复（2026-09-16）：旧写法 `interval ?? st.type === "files" ? 60 : 15` 因 ?? 与 ?:
      //   的优先级实际解析为 `(interval ?? cond) ? 60 : 15`——显式传入的 interval 恒被吞掉。
      const gap = interval ?? ((st as any).type === "files" ? 60 : 15);
      await new Promise((res) => setTimeout(res, gap * 1000));
    }
  }

  /**
   * 一站式文本翻译：提交任务并阻塞等待完成，返回含 translations 的响应。
   *
   * @param text - 源文本
   * @param targetLangs - 目标语言列表
   * @param mode - 翻译模式
   * @returns 完成的任务状态（含 translations 字段）
   */
  async translateAndWait(text: string, targetLangs: string[] = ["en"], mode = "pro"): Promise<TaskStatus> {
    const t = await this.createTask(text, targetLangs, mode);
    return this.waitTask(t.task_id);
  }

  /**
   * 下载文件任务产物到本地路径。
   *
   * @param taskId - 任务 ID
   * @param savePath - 本地保存路径
   * @param fileId - 文件 ID（可选，缺省打包 zip 全部）
   * @remarks Node 环境（依赖 fs 写盘）。浏览器请用 fetch 自行取 Blob。
   */
  async downloadFile(taskId: number, savePath: string, fileId?: number): Promise<void> {
    const url = `/openapi/v1/tasks/download?id=${taskId}${fileId ? `&file_id=${fileId}` : ""}`;
    const resp = await fetch(this.baseUrl + url, { headers: { "Authorization": `Bearer ${this.apiKey}` } });
    if (!resp.ok) {
      // ★ F-64①：旧写法只把状态码塞进异常，**响应体整个丢掉**，
      // 于是产物未就绪（409 not_ready）、Key 无权限（403 forbidden）、任务不存在（404）
      // 在调用方眼里全是"下载失败 HTTP 409"一行字——既不知道该等多久，也分不清
      // "我传错 id" 和"服务端还没翻完"。这里按错误体补 message/code/retryAfter。
      const raw = await resp.text().catch(() => "");
      let data: any = undefined;
      try { data = raw ? JSON.parse(raw) : undefined; } catch { data = undefined; }
      throw new TranslatorError(
        (data && (data.message || data.error)) || `下载失败 HTTP ${resp.status}`,
        resp.status,
        (data && (data.code || data.error_code)) || undefined,
        data ?? raw,
        pickRetryAfter(data, resp.headers)
      );
    }
    const buf = Buffer.from(await resp.arrayBuffer());
    // ★ R-L2 负向守卫（2026-09-16）：2xx 但内容是 JSON 错误体＝服务端把失败伪装成成功
    // （老后端口径），按二进制写盘会产出"内容为 JSON 的假产物"。判据保留，
    // 覆盖"新 SDK 打老后端"的混跑窗口。
    if (buf.length > 0 && buf.length < 64 * 1024) {
      const head = buf.toString("utf8").trim();
      if (head.startsWith("{")) {
        try {
          const parsed = JSON.parse(head);
          if (parsed && parsed.success === false) {
            throw new TranslatorError(
              parsed.message || "产物未就绪或下载失败", resp.status,
              parsed.code || parsed.error_code, parsed, pickRetryAfter(parsed, resp.headers));
          }
        } catch (e) {
          if (e instanceof TranslatorError) throw e; // JSON.parse 失败＝真二进制，继续写盘
        }
      }
    }
    require("fs").writeFileSync(savePath, buf);
  }

  /**
   * 查询租户余额（积分）。
   *
   * @returns {balance_points, balance_sentences_approx}
   */
  async balance(): Promise<Balance> {
    return this.request("GET", "/openapi/v1/balance");
  }

  /**
   * 查询本租户知识库统计（需 Key 具备 kb 权限）。
   *
   * @returns 知识库统计信息
   */
  async kbStats(): Promise<any> {
    return this.request("POST", "/openapi/v1/kb/stats", JSON.stringify({}), "application/json");
  }

  /**
   * 查询本租户用量汇总（需 Key 具备 billing 权限）。
   *
   * @returns 用量明细
   */
  async usage(): Promise<any> {
    return this.request("POST", "/openapi/v1/billing/usage", JSON.stringify({}), "application/json");
  }

  /**
   * 轮换当前 API Key（旧 Key 立即失效，请妥善保管新 Key）。
   *
   * @returns 新 Key 信息
   */
  async rotateApiKey(): Promise<any> {
    return this.request("POST", "/openapi/v1/apikey/rotate", JSON.stringify({}), "application/json");
  }
}
