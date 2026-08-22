# ============================================================================
# translator_sdk.py — 翻译助手开放 API Python SDK（零第三方依赖）
# 对接端点：/openapi/v1/translate · /openapi/v1/kb/stats ·
#          /openapi/v1/billing/usage · /openapi/v1/apikey/rotate
# 认证方式：API Key（Bearer），在管理后台「API Key」面板签发
# 用法示例：
#   from translator_sdk import TranslatorClient
#   cli = TranslatorClient(base_url="https://translator.example.com", api_key="tk_xxx")
#   r = cli.translate("蓝牙钥匙已激活", ["en", "ja"])
#   print(r["translations"])   # {"en": "...", "ja": "..."}
# ============================================================================

import json
import urllib.request
import urllib.error


class TranslatorError(Exception):
    """开放 API 调用异常：携带 HTTP 状态码与响应体。"""

    def __init__(self, message, status=None, body=None):
        super().__init__(message)
        self.status = status
        self.body = body


class TranslatorClient:
    """翻译助手开放 API 客户端。

    :param base_url: 服务地址，如 https://translator.example.com
    :param api_key:  开放 API Key（管理后台签发，形如 tk_... 或随机串）
    :param timeout:  单请求超时秒数（默认 30；长文本建议调大）
    """

    def __init__(self, base_url: str, api_key: str, timeout: int = 30):
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.timeout = timeout

    # ---------- 内部请求 ----------
    def _post(self, path: str, payload: dict) -> dict:
        url = f"{self.base_url}{path}"
        data = json.dumps(payload).encode("utf-8")
        req = urllib.request.Request(
            url,
            data=data,
            method="POST",
            headers={
                "Content-Type": "application/json",
                "Authorization": f"Bearer {self.api_key}",
            },
        )
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                return json.loads(resp.read().decode("utf-8"))
        except urllib.error.HTTPError as e:
            body = e.read().decode("utf-8", errors="replace")
            raise TranslatorError(f"HTTP {e.code}: {body}", status=e.code, body=body)
        except urllib.error.URLError as e:
            raise TranslatorError(f"连接失败: {e.reason}")

    # ---------- 业务接口 ----------
    def translate(self, text: str, target_langs=None) -> dict:
        """翻译文本。

        :param text: 源文本（必填）
        :param target_langs: 目标语言代码列表，如 ["en","ja"]；缺省 ["en"]
        :return: {success, translations: {lang: text}, sources, mode, sentence_balance}
        :raises TranslatorError: 句数耗尽（sentence_exhausted）或服务错误时抛出
        """
        body = {"text": text}
        if target_langs:
            body["target_langs"] = list(target_langs)
        r = self._post("/openapi/v1/translate", body)
        if not r.get("success"):
            raise TranslatorError(r.get("message", "翻译失败"),
                                  status=r.get("code"), body=r)
        return r

    def translate_batch(self, texts, target_langs=None, stop_on_error=False):
        """批量翻译（逐条调用，生成器返回 (原文, 结果或异常)）。"""
        for t in texts:
            try:
                yield t, self.translate(t, target_langs)
            except TranslatorError as e:
                if stop_on_error:
                    raise
                yield t, e

    def kb_stats(self) -> dict:
        """查询本租户知识库统计（需 Key 具备 kb/all 权限）。"""
        r = self._post("/openapi/v1/kb/stats", {})
        if not r.get("success"):
            raise TranslatorError(r.get("message", "查询失败"), body=r)
        return r

    def usage(self) -> dict:
        """查询本租户用量汇总（需 Key 具备 billing/all 权限）。"""
        r = self._post("/openapi/v1/billing/usage", {})
        if not r.get("success"):
            raise TranslatorError(r.get("message", "查询失败"), body=r)
        return r

    def rotate_api_key(self) -> dict:
        """轮换当前 Key（旧 Key 立即失效，请妥善保管新 Key）。"""
        r = self._post("/openapi/v1/apikey/rotate", {})
        if not r.get("success"):
            raise TranslatorError(r.get("message", "轮换失败"), body=r)
        return r


if __name__ == "__main__":
    # 简易命令行自测：python3 translator_sdk.py <base_url> <api_key> <text>
    import sys

    if len(sys.argv) != 4:
        print("用法: python3 translator_sdk.py <base_url> <api_key> <text>")
        sys.exit(1)
    cli = TranslatorClient(sys.argv[1], sys.argv[2])
    out = cli.translate(sys.argv[3], ["en"])
    print(json.dumps(out["translations"], ensure_ascii=False, indent=2))
