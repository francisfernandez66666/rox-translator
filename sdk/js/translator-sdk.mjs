// ============================================================================
// translator-sdk.mjs — 翻译助手开放 API JavaScript SDK（零依赖，fetch/Node18+/浏览器通用）
// 对接端点：/openapi/v1/translate · /openapi/v1/kb/stats ·
//          /openapi/v1/billing/usage · /openapi/v1/apikey/rotate
// 用法示例：
//   import { TranslatorClient } from "./translator-sdk.mjs";
//   const cli = new TranslatorClient("https://translator.example.com", "tk_xxx");
//   const r = await cli.translate("蓝牙钥匙已激活", ["en", "ja"]);
//   console.log(r.translations);
// ============================================================================

export class TranslatorError extends Error {
  constructor(message, status, body) {
    super(message);
    this.name = "TranslatorError";
    this.status = status; // HTTP 状态码或业务错误码（如 sentence_exhausted）
    this.body = body;
  }
}

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

  /** @internal 统一 POST */
  async #post(path, payload) {
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), this.timeoutMs);
    let resp;
    try {
      resp = await fetch(this.base + path, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${this.apiKey}`,
        },
        body: JSON.stringify(payload),
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
      throw new TranslatorError(`HTTP ${resp.status}: ${text}`, resp.status, text);
    }
    if (!resp.ok) throw new TranslatorError(data.message || `HTTP ${resp.status}`, resp.status, data);
    return data;
  }

  /**
   * 翻译文本
   * @param {string} text 源文本（必填）
   * @param {string[]} [targetLangs] 目标语言代码列表，缺省 ["en"]
   * @returns {Promise<{success:boolean, translations:Record<string,string>, sources?:object, mode?:string, sentence_balance?:number}>}
   */
  async translate(text, targetLangs) {
    const body = { text };
    if (targetLangs && targetLangs.length) body.target_langs = targetLangs;
    const r = await this.#post("/openapi/v1/translate", body);
    if (!r.success) throw new TranslatorError(r.message || "翻译失败", r.code, r);
    return r;
  }

  /** 批量翻译：逐条调用，返回 [{text, result|error}]；stopOnError=true 时遇错抛出 */
  async translateBatch(texts, targetLangs, stopOnError = false) {
    const out = [];
    for (const t of texts) {
      try {
        out.push({ text: t, result: await this.translate(t, targetLangs) });
      } catch (e) {
        if (stopOnError) throw e;
        out.push({ text: t, error: e });
      }
    }
    return out;
  }

  /** 知识库统计（需 Key 具备 kb/all 权限） */
  async kbStats() {
    const r = await this.#post("/openapi/v1/kb/stats", {});
    if (!r.success) throw new TranslatorError(r.message || "查询失败", undefined, r);
    return r;
  }

  /** 用量汇总（需 Key 具备 billing/all 权限） */
  async usage() {
    const r = await this.#post("/openapi/v1/billing/usage", {});
    if (!r.success) throw new TranslatorError(r.message || "查询失败", undefined, r);
    return r;
  }

  /** 轮换当前 API Key（旧 Key 立即失效） */
  async rotateApiKey() {
    const r = await this.#post("/openapi/v1/apikey/rotate", {});
    if (!r.success) throw new TranslatorError(r.message || "轮换失败", undefined, r);
    return r;
  }
}

export default TranslatorClient;
