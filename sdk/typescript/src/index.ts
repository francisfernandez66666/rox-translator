// 翻译助手 langcross TypeScript 客户端（与 Python SDK 契约一致）。
// 约定：成功响应为 202/200 且不含 success 字段，以 task_id 存在为准；
// 业务错误为 HTTP 200 + {success:false, error_code, message} 或 HTTP 4xx/5xx。
// 鉴权：X-API-Key 头。

export interface TaskCreated {
  task_id: number;
  mode: string;
  type: string;
  status: string;
  file_count?: number;
}

export interface TaskStatus {
  task_id: number;
  status: "queued" | "processing" | "completed" | "failed";
  progress?: number;
  error_code?: string;
  message?: string;
  files?: { file_id: number; name: string; url: string }[];
}

export interface Balance {
  balance_tokens: number;
}

export class TranslatorError extends Error {
  status?: number;
  error_code?: string;
  body?: unknown;
  constructor(message: string, status?: number, error_code?: string, body?: unknown) {
    super(message);
    this.name = "TranslatorError";
    this.status = status;
    this.error_code = error_code;
    this.body = body;
  }
}

export class TranslatorClient {
  private baseUrl: string;
  private apiKey: string;
  private timeout: number;

  constructor(baseUrl: string, apiKey: string, timeout = 30) {
    this.baseUrl = baseUrl.replace(/\/+$/, "");
    this.apiKey = apiKey;
    this.timeout = timeout * 1000;
  }

  private async request(method: string, path: string, body?: BodyInit, contentType?: string): Promise<any> {
    const headers: Record<string, string> = { "X-API-Key": this.apiKey };
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

  /** 创建文本翻译任务（异步）。 */
  async createTask(text: string, targetLangs: string[] = ["en"], mode = "pro", title = ""): Promise<TaskCreated> {
    const r: any = await this.request("POST", "/tasks", JSON.stringify({ text, target_langs: targetLangs, mode, title }), "application/json");
    if (r.task_id === undefined) throw new TranslatorError(r.message || "创建任务失败", undefined, r.error_code, r);
    return r;
  }

  /** 创建文件批量翻译任务（≤20 个，总量 ≤30MB）。files 为本地路径数组（Node 环境）。 */
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
    const r: any = await this.request("POST", "/tasks", body, `multipart/form-data; boundary=${boundary}`);
    if (r.task_id === undefined) throw new TranslatorError(r.message || "创建任务失败", undefined, r.error_code, r);
    return r;
  }

  /** 查询任务状态。 */
  async getTask(taskId: number): Promise<TaskStatus> {
    const r: any = await this.request("GET", `/tasks/status?id=${taskId}`);
    if (r.status === undefined) throw new TranslatorError(r.message || "查询失败", undefined, r.error_code, r);
    return r;
  }

  /** 阻塞等待任务完成（默认按类型 15s/60s 轮询）。 */
  async waitTask(taskId: number, interval?: number, timeoutSec = 3600): Promise<TaskStatus> {
    const iv = interval ?? 30;
    const deadline = Date.now() + timeoutSec * 1000;
    // eslint-disable-next-line no-constant-condition
    while (true) {
      const st = await this.getTask(taskId);
      if (st.status === "completed" || st.status === "failed") return st;
      if (Date.now() > deadline) throw new TranslatorError("等待任务超时", undefined, "timeout");
      await new Promise((res) => setTimeout(res, iv * 1000));
    }
  }

  /** 文本翻译并等待结果（便捷封装）。 */
  async translateAndWait(text: string, targetLangs: string[] = ["en"], mode = "pro"): Promise<TaskStatus> {
    const t = await this.createTask(text, targetLangs, mode);
    return this.waitTask(t.task_id);
  }

  /** 下载产物到 savePath（fileId 可选，缺省 zip 全部）。 */
  async downloadFile(taskId: number, savePath: string, fileId?: number): Promise<void> {
    const url = `/tasks/download?id=${taskId}${fileId ? `&file_id=${fileId}` : ""}`;
    const resp = await fetch(this.baseUrl + url, { headers: { "X-API-Key": this.apiKey } });
    if (!resp.ok) throw new TranslatorError(`下载失败 HTTP ${resp.status}`, resp.status);
    const buf = Buffer.from(await resp.arrayBuffer());
    require("fs").writeFileSync(savePath, buf);
  }

  /** 租户余额 token。 */
  async balance(): Promise<Balance> {
    return this.request("GET", "/balance");
  }

  /** 知识库统计。 */
  async kbStats(): Promise<any> {
    return this.request("GET", "/kb/stats");
  }

  /** 用量明细。 */
  async usage(): Promise<any> {
    return this.request("GET", "/usage");
  }

  /** 轮换 API Key。 */
  async rotateApiKey(): Promise<any> {
    return this.request("POST", "/keys/rotate");
  }
}
