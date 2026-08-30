package com.langcross.sdk;

/** 翻译助手 SDK 异常（携带 HTTP 状态与错误码）。 */
public class TranslatorError extends RuntimeException {
    public final Integer status;
    public final String errorCode;

    public TranslatorError(String message, Integer status, String errorCode) {
        super(message);
        this.status = status;
        this.errorCode = errorCode;
    }
}
