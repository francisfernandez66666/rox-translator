package com.langcross.sdk;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;

import java.io.IOException;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;

/**
 * 翻译助手 langcross Java 客户端（与 Python/TypeScript SDK 契约一致）。
 *
 * <p>对接端点（异步任务模型）：
 * <ul>
 *   <li>POST /openapi/v1/tasks           — 创建任务（JSON=文本；multipart=文件批量）</li>
 *   <li>GET  /openapi/v1/tasks/status    — 轮询状态（未完成 status=queued/processing）</li>
 *   <li>GET  /openapi/v1/tasks/download  — 文件产物下载</li>
 *   <li>GET  /openapi/v1/balance         — 查询 token 余额与 ≈句数</li>
 *   <li>POST /openapi/v1/kb/stats        — 知识库统计</li>
 *   <li>POST /openapi/v1/billing/usage   — 用量明细</li>
 *   <li>POST /openapi/v1/apikey/rotate   — 轮换 API Key</li>
 * </ul>
 *
 * <p>认证方式：Bearer Token（Authorization: Bearer <api_key>），在管理后台「API Key」面板签发。
 * <p>快速上手：
 * <pre>{@code
 *   TranslatorClient cli = new TranslatorClient("https://translator.example.com", "tk_xxx");
 *
 *   // 文本翻译：提交任务 → 自动轮询（15s）→ 返回译文
 *   JsonNode r = cli.translateAndWait("蓝牙钥匙已激活", List.of("en", "ja"));
 *   System.out.println(r.get("translations"));
 *
 *   // 查询余额
 *   JsonNode balance = cli.balance();
 *   System.out.println("剩余 token: " + balance.get("balance_tokens"));
 * }</pre>
 *
 * <p>错误处理：余额不足时抛出 TranslatorError，error_code == "insufficient_balance"
 * <p>依赖：Java 11+ 与 jackson-databind（见 pom.xml）
 */
public class TranslatorClient {
    /** 服务基地址（不含尾部斜杠） */
    private final String baseUrl;
    /** API Key（Bearer Token） */
    private final String apiKey;
    /** JSON 序列化/反序列化器 */
    private final ObjectMapper mapper = new ObjectMapper();
    /** HTTP 客户端（连接超时 10s） */
    private final HttpClient http = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(10)).build();

    /**
     * 构造翻译客户端。
     *
     * @param baseUrl 服务地址，如 https://translator.example.com
     * @param apiKey  开放 API Key（管理后台签发）
     */
    public TranslatorClient(String baseUrl, String apiKey) {
        this.baseUrl = baseUrl.replaceAll("/+$", "");
        this.apiKey = apiKey;
    }

    /**
     * 内部统一请求方法（JSON 响应 + 错误码提取）。
     *
     * @param method      HTTP 方法（GET/POST）
     * @param path        API 路径（如 /openapi/v1/tasks）
     * @param body        请求体（null 表示无body）
     * @param contentType Content-Type（null 表示不设置）
     * @return 解析后的 JSON 对象
     * @throws TranslatorError 请求失败时抛出
     */
    private JsonNode request(String method, String path, String body, String contentType) {
        try {
            HttpRequest.Builder b = HttpRequest.newBuilder()
                    .uri(URI.create(baseUrl + path))
                    .timeout(Duration.ofSeconds(120))
                    .header("Authorization", "Bearer " + apiKey);
            if (body != null) {
                b.method(method, HttpRequest.BodyPublishers.ofString(body));
                if (contentType != null) b.header("Content-Type", contentType);
            } else {
                b.method(method, HttpRequest.BodyPublishers.noBody());
            }
            HttpResponse<String> resp = http.send(b.build(), HttpResponse.BodyHandlers.ofString());
            String txt = resp.body();
            JsonNode data = txt.isEmpty() ? mapper.createObjectNode() : mapper.readTree(txt);
            if (resp.statusCode() < 200 || resp.statusCode() >= 300) {
                throw new TranslatorError(
                        data.has("message") ? data.get("message").asText() : ("HTTP " + resp.statusCode()),
                        resp.statusCode(),
                        data.has("error_code") ? data.get("error_code").asText() : null);
            }
            return data;
        } catch (IOException | InterruptedException e) {
            throw new TranslatorError("连接失败: " + e.getMessage(), null, null);
        }
    }

    /**
     * 创建文本翻译任务（异步）。
     *
     * @param text        源文本（必填）
     * @param targetLangs 目标语言代码列表，如 ["en", "ja"]
     * @param mode        翻译模式："pro" 专业校对（默认）/ "fast" 快速
     * @param title       任务标题（可选）
     * @return 包含 task_id 的 JSON 对象
     */
    public JsonNode createTask(String text, List<String> targetLangs, String mode, String title) {
        Map<String, Object> payload = new java.util.HashMap<>();
        payload.put("text", text);
        if (targetLangs != null) payload.put("target_langs", targetLangs);
        payload.put("mode", mode == null ? "pro" : mode);
        if (title != null && !title.isEmpty()) payload.put("title", title);
        JsonNode r = request("POST", "/openapi/v1/tasks", json(payload), "application/json");
        if (!r.has("task_id")) throw new TranslatorError(r.path("message").asText("创建任务失败"), null, r.path("error_code").asText());
        return r;
    }

    /**
     * 创建文本翻译任务（异步），使用默认参数。
     *
     * @param text        源文本
     * @param targetLangs 目标语言列表
     * @return 包含 task_id 的 JSON 对象
     */
    public JsonNode createTask(String text, List<String> targetLangs) {
        return createTask(text, targetLangs, "pro", "");
    }

    /**
     * 查询任务状态。
     *
     * @param taskId 任务 ID
     * @return 任务状态（status ∈ queued/processing/completed/failed）
     */
    public JsonNode getTask(long taskId) {
        JsonNode r = request("GET", "/openapi/v1/tasks/status?id=" + taskId, null, null);
        if (!r.has("status")) throw new TranslatorError(r.path("message").asText("查询失败"), null, r.path("error_code").asText());
        return r;
    }

    /**
     * 阻塞等待任务完成（按任务类型：文本 15s / 文件 60s 轮询）。
     *
     * @param taskId     任务 ID
     * @param interval   轮询间隔秒数（0 表示按任务类型自动选择）
     * @param timeoutSec 总超时秒数
     * @return 终态任务状态（completed/failed）
     */
    public JsonNode waitTask(long taskId, int interval, int timeoutSec) {
        int waited = 0;
        while (true) {
            JsonNode st = getTask(taskId);
            String status = st.path("status").asText();
            if ("completed".equals(status) || "failed".equals(status)) return st;
            if (waited >= timeoutSec) throw new TranslatorError("等待任务超时", null, "timeout");
            // 首轮按任务类型定默认间隔（文件任务产物大，60s 更合理）
            int gap = interval > 0 ? interval : "files".equals(st.path("type").asText()) ? 60 : 15;
            try { Thread.sleep(gap * 1000L); } catch (InterruptedException ignored) {}
            waited += gap;
        }
    }

    /**
     * 一站式文本翻译：提交任务并阻塞等待完成，返回含 translations 的响应。
     *
     * @param text        源文本
     * @param targetLangs 目标语言列表
     * @return 完成的任务状态（含 translations 字段）
     */
    public JsonNode translateAndWait(String text, List<String> targetLangs) {
        JsonNode t = createTask(text, targetLangs);
        return waitTask(t.get("task_id").asLong(), 0, 3600);
    }

    /**
     * 查询租户余额 token。
     *
     * @return {balance_tokens, balance_sentences_approx}
     */
    public JsonNode balance() {
        return request("GET", "/openapi/v1/balance", null, null);
    }

    /**
     * 查询本租户用量汇总（需 Key 具备 billing 权限）。
     *
     * @return 用量明细
     */
    public JsonNode usage() {
        return request("POST", "/openapi/v1/billing/usage", json(Map.of()), "application/json");
    }

    /**
     * 查询本租户知识库统计（需 Key 具备 kb 权限）。
     *
     * @return 知识库统计信息
     */
    public JsonNode kbStats() {
        return request("POST", "/openapi/v1/kb/stats", json(Map.of()), "application/json");
    }

    /**
     * 轮换当前 API Key（旧 Key 立即失效，请妥善保管新 Key）。
     *
     * @return 新 Key 信息
     */
    public JsonNode rotateApiKey() {
        return request("POST", "/openapi/v1/apikey/rotate", json(Map.of()), "application/json");
    }

    /**
     * 将对象序列化为 JSON 字符串。
     *
     * @param o 待序列化对象
     * @return JSON 字符串
     */
    private String json(Object o) {
        try { return mapper.writeValueAsString(o); } catch (IOException e) { throw new RuntimeException(e); }
    }

    /**
     * 便捷方法：将逗号分隔的语言代码串转为列表。
     *
     * @param csv 逗号分隔的语言代码，如 "en,ja,ko"
     * @return 语言代码列表
     */
    public static List<String> langs(String csv) {
        List<String> out = new ArrayList<>();
        for (String s : csv.split(",")) if (!s.isBlank()) out.add(s.trim());
        return out;
    }
}
