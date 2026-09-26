package com.langcross.sdk;

/**
 * 翻译助手 SDK 异常（携带 HTTP 状态、错误码与限流退避时长）。
 *
 * <p>★ F-64①（2026-09-26 状态码诚实改造）：{@code status} 现在就是**真实 HTTP 状态码**
 * （400/401/402/403/404/409/429/500），不再是「失败也回 200、只有 body 知道」。
 * <p>{@code code} 是错误码正主（与对外文档 Error.code 枚举同名同值）；{@code errorCode}
 * 是 &lt;1.0.4 客户端在读的别名，两者同值，字段都保留以免打断已在生产的调用方。
 * <p>{@code retryAfterSeconds} 仅限流（429）时有值：还需等待的秒数，取 JSON 的 retry_after，
 * 取不到再取 HTTP Retry-After 头；没有则为 {@code null}。
 */
public class TranslatorError extends RuntimeException {
    public final Integer status;
    public final String errorCode;
    public final String code;
    public final Integer retryAfterSeconds;

    public TranslatorError(String message, Integer status, String errorCode) {
        this(message, status, errorCode, null);
    }

    public TranslatorError(String message, Integer status, String errorCode, Integer retryAfterSeconds) {
        super(message);
        this.status = status;
        this.errorCode = errorCode;
        this.code = errorCode;
        this.retryAfterSeconds = retryAfterSeconds;
    }
}
