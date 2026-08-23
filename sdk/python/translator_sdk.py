# ============================================================================
# translator_sdk.py — 翻译助手开放 API Python SDK（零第三方依赖）
# 对接端点（异步任务模型）：
#   POST /openapi/v1/tasks           创建任务（JSON=文本；multipart=文件批量）
#   GET  /openapi/v1/tasks/status    轮询状态（未完成 status=queued/processing）
#   GET  /openapi/v1/tasks/download  文件产物下载
#   GET  /openapi/v1/balance         查询 token 余额与 ≈句数
#   POST /openapi/v1/kb/stats · /openapi/v1/billing/usage · /openapi/v1/apikey/rotate
# 认证方式：API Key（Bearer），在管理后台「API Key」面板签发
#
# 快速上手：
#   from translator_sdk import TranslatorClient
#   cli = TranslatorClient(base_url="https://translator.example.com", api_key="tk_xxx")
#
#   # 文本翻译：提交任务 → 自动轮询（15s）→ 返回译文
#   r = cli.translate_and_wait("蓝牙钥匙已激活", ["en", "ja"])
#   print(r["translations"])          # {"en": "...", "ja": "..."}
#
#   # 文件批量翻译：提交 → 自动轮询（60s）→ 下载产物到目录
#   cli.create_file_task(["手册.docx", "清单.xlsx"], ["en"], mode="pro")
#   r = cli.wait_task(task_id)
#   cli.download_files(task_id, save_dir="./out")
#
# 错误处理：余额不足时抛出 TranslatorError，error_code == "insufficient_balance"
# ============================================================================

import json
import time
import urllib.request
import urllib.error
import uuid


class TranslatorError(Exception):
    """开放 API 调用异常：携带 HTTP 状态码、错误码与响应体。"""

    def __init__(self, message, status=None, error_code=None, body=None):
        super().__init__(message)
        self.status = status
        self.error_code = error_code  # 如 insufficient_balance / rate_limited
        self.body = body


class TranslatorClient:
    """翻译助手开放 API 客户端。

    :param base_url: 服务地址，如 https://translator.example.com
    :param api_key:  开放 API Key（管理后台签发）
    :param timeout:  单请求超时秒数（默认 30）
    """

    def __init__(self, base_url: str, api_key: str, timeout: int = 30):
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.timeout = timeout

    # ---------- 内部请求 ----------
    def _headers(self):
        return {"Authorization": f"Bearer {self.api_key}"}

    def _request(self, method: str, path: str, body: bytes = None,
                 content_type: str = None, raw: bool = False):
        url = f"{self.base_url}{path}"
        headers = self._headers()
        if content_type:
            headers["Content-Type"] = content_type
        req = urllib.request.Request(url, data=body, method=method, headers=headers)
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                data = resp.read()
                return data if raw else json.loads(data.decode("utf-8"))
        except urllib.error.HTTPError as e:
            body = e.read().decode("utf-8", errors="replace")
            try:
                parsed = json.loads(body)
                raise TranslatorError(parsed.get("message", f"HTTP {e.code}"),
                                      status=e.code,
                                      error_code=parsed.get("error_code"),
                                      body=parsed)
            except json.JSONDecodeError:
                raise TranslatorError(f"HTTP {e.code}: {body}", status=e.code, body=body)
        except urllib.error.URLError as e:
            raise TranslatorError(f"连接失败: {e.reason}")

    def _post_json(self, path: str, payload: dict) -> dict:
        return self._request("POST", path,
                             json.dumps(payload).encode("utf-8"), "application/json")

    def _get(self, path: str) -> dict:
        return self._request("GET", path)

    # ---------- 任务创建 ----------
    def create_task(self, text: str, target_langs=None, mode: str = "pro",
                    title: str = "") -> dict:
        """创建文本翻译任务（异步）。

        :param text: 源文本（必填）
        :param target_langs: 目标语言代码列表，缺省 ["en"]
        :param mode: "pro" 专业校对（默认）/ "fast" 快速（无知识库）
        :return: {task_id, ticket_no, status:"queued", poll_interval_sec, balance_tokens, ...}
        """
        body = {"text": text, "mode": mode}
        if target_langs:
            body["target_langs"] = list(target_langs)
        if title:
            body["title"] = title
        r = self._post_json("/openapi/v1/tasks", body)
        if not r.get("success"):
            raise TranslatorError(r.get("message", "创建任务失败"),
                                  error_code=r.get("error_code"), body=r)
        return r

    def create_file_task(self, files, target_langs=None, mode: str = "pro",
                         title: str = "") -> dict:
        """创建文件批量翻译任务（≤20 个，总量 ≤30MB）。

        :param files: 文件路径列表，如 ["手册.docx", "清单.xlsx"]
        :param target_langs: 目标语言代码列表，缺省 ["en"]
        :param mode: "pro" / "fast"
        :return: 同 create_task，另含 file_count
        """
        boundary = "----TranslatorSDKBoundary" + uuid.uuid4().hex
        lines = []
        for p in files:
            with open(p, "rb") as fh:
                data = fh.read()
            name = p.replace("\\", "/").split("/")[-1]
            lines.append(f"--{boundary}".encode())
            lines.append(
                f'Content-Disposition: form-data; name="files"; filename="{name}"'.encode())
            lines.append(b"Content-Type: application/octet-stream")
            lines.append(b"")
            lines.append(data)
        for key, val in (("target_langs", ",".join(target_langs or []) or "en"),
                         ("mode", mode), ("title", title)):
            lines.append(f"--{boundary}".encode())
            lines.append(f'Content-Disposition: form-data; name="{key}"'.encode())
            lines.append(b"")
            lines.append(val.encode("utf-8"))
        lines.append(f"--{boundary}--".encode())
        body = b"\r\n".join(lines)
        r = self._request("POST", "/openapi/v1/tasks", body,
                          f"multipart/form-data; boundary={boundary}")
        if not r.get("success"):
            raise TranslatorError(r.get("message", "创建任务失败"),
                                  error_code=r.get("error_code"), body=r)
        return r

    # ---------- 任务轮询 / 结果 / 下载 ----------
    def get_task(self, task_id: int) -> dict:
        """查询任务状态。status ∈ queued / processing / completed / failed；
        失败时响应携带 error_code（如 insufficient_balance）。"""
        r = self._get(f"/openapi/v1/tasks/status?id={task_id}")
        if not r.get("success"):
            raise TranslatorError(r.get("message", "查询失败"),
                                  error_code=r.get("error_code"), body=r)
        return r

    def wait_task(self, task_id: int, interval: int = None,
                  timeout: int = 1800) -> dict:
        """阻塞轮询直至终态（completed / failed）。

        :param interval: 轮询间隔秒数；缺省按服务端建议（文本 15s / 文件 60s）
        :param timeout: 总超时秒数（默认 30 分钟），超时抛 TranslatorError
        """
        deadline = time.time() + timeout
        gap = interval
        while True:
            r = self.get_task(task_id)
            st = r.get("status")
            if st == "completed":
                return r
            if st == "failed":
                raise TranslatorError(r.get("error") or "任务失败",
                                      error_code=r.get("error_code"), body=r)
            if time.time() > deadline:
                raise TranslatorError(f"轮询超时（{timeout}s），任务仍在处理")
            gap = gap or r.get("poll_interval_sec") or 15
            time.sleep(gap)

    def translate_and_wait(self, text: str, target_langs=None,
                           mode: str = "pro", timeout: int = 1800) -> dict:
        """一站式文本翻译：提交任务并阻塞等待完成，返回含 translations 的响应。"""
        created = self.create_task(text, target_langs, mode)
        return self.wait_task(created["task_id"],
                              interval=created.get("poll_interval_sec"),
                              timeout=timeout)

    def download_file(self, task_id: int, save_path: str, file_id: int = None):
        """下载文件任务的翻译产物（file_id 缺省时多文件打包 zip）。

        :param save_path: 保存到本地的完整路径
        """
        qs = f"/openapi/v1/tasks/download?id={task_id}"
        if file_id:
            qs += f"&file_id={file_id}"
        data = self._request("GET", qs, raw=True)
        with open(save_path, "wb") as fh:
            fh.write(data)
        return save_path

    # ---------- 余额与辅助接口 ----------
    def balance(self) -> dict:
        """查询 token 余额与 ≈句数换算：
        {balance_tokens, balance_sentences_approx}"""
        return self._get("/openapi/v1/balance")

    def kb_stats(self) -> dict:
        """查询本租户知识库统计（需 Key 具备 kb/all 权限）。"""
        r = self._post_json("/openapi/v1/kb/stats", {})
        if not r.get("success"):
            raise TranslatorError(r.get("message", "查询失败"), body=r)
        return r

    def usage(self) -> dict:
        """查询本租户用量汇总（需 Key 具备 billing/all 权限）。"""
        r = self._post_json("/openapi/v1/billing/usage", {})
        if not r.get("success"):
            raise TranslatorError(r.get("message", "查询失败"), body=r)
        return r

    def rotate_api_key(self) -> dict:
        """轮换当前 Key（旧 Key 立即失效，请妥善保管新 Key）。"""
        r = self._post_json("/openapi/v1/apikey/rotate", {})
        if not r.get("success"):
            raise TranslatorError(r.get("message", "轮换失败"), body=r)
        return r


if __name__ == "__main__":
    # 命令行自测：python3 translator_sdk.py <base_url> <api_key> <text>
    import sys

    if len(sys.argv) != 4:
        print("用法: python3 translator_sdk.py <base_url> <api_key> <text>")
        sys.exit(1)
    cli = TranslatorClient(sys.argv[1], sys.argv[2])
    out = cli.translate_and_wait(sys.argv[3], ["en"])
    print(json.dumps(out.get("translations"), ensure_ascii=False, indent=2))
