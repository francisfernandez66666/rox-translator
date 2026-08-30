// ============================================================================
// index.ts — 翻译助手开放 API TypeScript SDK（零第三方依赖，Node 18+ / 浏览器通用）
// 对接端点（异步任务模型）：
//   POST /openapi/v1/tasks           创建任务（JSON=文本；multipart=文件批量）
//   GET  /openapi/v1/tasks/status    轮询状态（未完成 status=queued/processing）
//   GET  /openapi/v1/tasks/download  文件产物下载
//   GET  /openapi/v1/balance         查询 token 余额与 ≈句数
//   POST /openapi/v1/kb/stats · /openapi/v1/billing/usage · /openapi/v1/apikey/rotate
// 认证方式：Bearer Token（Authorization: Bearer <api_key>），在管理后台「API Key」面板签发
//
// 快速上手：
//   import { TranslatorClient } from "@langcross/translator-sdk";
//   const cli = new TranslatorClient("https://translator.example.com", "tk_xxx");
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
// 错误处理：余额不足时抛出 TranslatorError，error_code == "insufficient_balance"
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
  balance_tokens: number; // 剩余 token 数
}

/** 翻译 API 调用异常 */
export class TranslatorError extends Error {
  status?: number;       // HTTP 状态码
  error_code?: string;   // 业务错误码
  body?: unknown;        // 原始响应体
  constructor(message: string, status?: number, error_code?: string, body?: unknown) {
    super(message);
    this.name = "TranslatorError";
    this.status = status;
    this.error_code = error_code;
    this.body = body;
  }
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
        throw new TranslatorError(
          (data && (data.message || data.error)) || `HTTP ${resp.status}`,
          resp.status,
          data && data.error_code,
          data
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
   * 创建文件批量翻译任务（≤20 个，总量 ≤30MB）。
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
      const gap = interval ?? (st as any).type === "files" ? 60 : 15;
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
   */
  async downloadFile(taskId: number, savePath: string, fileId?: number): Promise<void> {
    const url = `/openapi/v1/tasks/download?id=${taskId}${fileId ? `&file_id=${fileId}` : ""}`;
    const resp = await fetch(this.baseUrl + url, { headers: { "Authorization": `Bearer ${this.apiKey}` } });
    if (!resp.ok) throw new TranslatorError(`下载失败 HTTP ${resp.status}`, resp.status);
    const buf = Buffer.from(await resp.arrayBuffer());
    require("fs").writeFileSync(savePath, buf);
  }

  /**
   * 查询租户余额 token。
   *
   * @returns {balance_tokens, balance_sentences_approx}
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
