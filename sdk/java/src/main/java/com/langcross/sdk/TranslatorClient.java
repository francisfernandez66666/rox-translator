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
 * 鉴权：X-API-Key 头。需 Java 11+ 与 jackson-databind（见 pom.xml）。
 */
public class TranslatorClient {
    private final String baseUrl;
    private final String apiKey;
    private final ObjectMapper mapper = new ObjectMapper();
    private final HttpClient http = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(10)).build();

    public TranslatorClient(String baseUrl, String apiKey) {
        this.baseUrl = baseUrl.replaceAll("/+$", "");
        this.apiKey = apiKey;
    }

    private JsonNode request(String method, String path, String body, String contentType) {
        try {
            HttpRequest.Builder b = HttpRequest.newBuilder()
                    .uri(URI.create(baseUrl + path))
                    .timeout(Duration.ofSeconds(120))
                    .header("X-API-Key", apiKey);
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

    /** 创建文本翻译任务（异步）。返回含 task_id 的 JSON 对象。 */
    public JsonNode createTask(String text, List<String> targetLangs, String mode, String title) {
        Map<String, Object> payload = new java.util.HashMap<>();
        payload.put("text", text);
        if (targetLangs != null) payload.put("target_langs", targetLangs);
        payload.put("mode", mode == null ? "pro" : mode);
        if (title != null && !title.isEmpty()) payload.put("title", title);
        JsonNode r = request("POST", "/tasks", json(payload), "application/json");
        if (!r.has("task_id")) throw new TranslatorError(r.path("message").asText("创建任务失败"), null, r.path("error_code").asText());
        return r;
    }

    public JsonNode createTask(String text, List<String> targetLangs) {
        return createTask(text, targetLangs, "pro", "");
    }

    /** 查询任务状态。 */
    public JsonNode getTask(long taskId) {
        JsonNode r = request("GET", "/tasks/status?id=" + taskId, null, null);
        if (!r.has("status")) throw new TranslatorError(r.path("message").asText("查询失败"), null, r.path("error_code").asText());
        return r;
    }

    /** 阻塞等待任务完成（每 interval 秒轮询，超时 timeoutSec）。 */
    public JsonNode waitTask(long taskId, int interval, int timeoutSec) {
        int waited = 0;
        while (true) {
            JsonNode st = getTask(taskId);
            String status = st.path("status").asText();
            if ("completed".equals(status) || "failed".equals(status)) return st;
            if (waited >= timeoutSec) throw new TranslatorError("等待任务超时", null, "timeout");
            try { Thread.sleep(interval * 1000L); } catch (InterruptedException ignored) {}
            waited += interval;
        }
    }

    /** 文本翻译并等待结果。 */
    public JsonNode translateAndWait(String text, List<String> targetLangs) {
        JsonNode t = createTask(text, targetLangs);
        return waitTask(t.get("task_id").asLong(), 15, 3600);
    }

    /** 租户余额 token。 */
    public JsonNode balance() {
        return request("GET", "/balance", null, null);
    }

    /** 用量明细。 */
    public JsonNode usage() {
        return request("GET", "/usage", null, null);
    }

    /** 知识库统计。 */
    public JsonNode kbStats() {
        return request("GET", "/kb/stats", null, null);
    }

    /** 轮换 API Key。 */
    public JsonNode rotateApiKey() {
        return request("POST", "/keys/rotate", null, null);
    }

    private String json(Object o) {
        try { return mapper.writeValueAsString(o); } catch (IOException e) { throw new RuntimeException(e); }
    }

    /** 便捷：将 target_langs 由逗号串转列表。 */
    public static List<String> langs(String csv) {
        List<String> out = new ArrayList<>();
        for (String s : csv.split(",")) if (!s.isBlank()) out.add(s.trim());
        return out;
    }
}
