# ============================================================================
# translator_sdk.py — 翻译助手开放 API Python SDK（零第三方依赖）
# 对接端点（异步任务模型）：
#   POST /openapi/v1/tasks           创建任务（JSON=文本；multipart=文件批量）
#   GET  /openapi/v1/tasks/status    轮询状态（未完成 status=queued/processing）
#   GET  /openapi/v1/tasks/download  文件产物下载
#   GET  /openapi/v1/balance         查询积分余额与 ≈句数
#   POST /openapi/v1/kb/stats · /openapi/v1/billing/usage · /openapi/v1/apikey/rotate
# 认证方式：API Key（Bearer），在管理后台「API Key」面板签发
#
# 快速上手：
#   from translator_sdk import TranslatorClient
#   cli = TranslatorClient(base_url="https://translator.example.com", api_key="rk_xxx")
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
# 错误处理（★ 2026-09-26 F-64① 状态码诚实改造后的口径）：
#   对外契约面的失败一律以**真实 HTTP 状态码**发出（400/401/402/403/404/409/429/500），
#   响应体带 code（正主，与文档 Error.code 枚举一致）。error_code 只是**状态码诚实改造之前的老服务端**
#   在发的别名；改造后的服务端统一只发 code（不再同值下发别名，免得两套码名长期并存、取法分叉）。
#   SDK 把两者都收敛到 TranslatorError.code（error_code 属性保留兼容，勿删）；
#   限流类（429）额外给出 retry_after 秒数，调用方按它退避即可，不必自己猜窗口。
#   唯一仍在 200 里表达「失败」的是**任务状态**轮询：status:"failed" 是业务对象的状态，
#   不是本次请求的失败，因此 get_task/wait_task 正常返回 200 并由 status 字段分支。
# ============================================================================

import json
import time
import urllib.request
import urllib.error
import uuid

# ★ P2 发布管线（2026-09-15）：发行版本号（与 pyproject.toml / npm package.json /
# java pom 三端对齐，scripts/release-sdk.sh 统一 bump；运行时可用于上报与排障）
__version__ = "1.0.2"


def _retry_after(parsed, headers=None):
    """从错误响应里取「还需等待秒数」：JSON 字段 retry_after 优先，HTTP Retry-After 头兜底。

    ★ F-64①/F-47：429 的两处给法（字段给应用、头给通用 HTTP 客户端）本就该同值，
    但中间层（网关/CDN）可能只透传其中之一，所以两侧都读。取不到就是 None
    （非限流错误本来不带，调用方 `if e.retry_after:` 一句即可分支）。
    HTTP 头的 HTTP-date 形态（如 "Wed, 21 Oct 2026 07:28:00 GMT"）本服务不下发，
    这里只认纯数字秒，其余一律 None——宁可少给一个退避提示，也不要把日期串丢给调用方去 int()。
    """
    val = None
    if isinstance(parsed, dict):
        val = parsed.get("retry_after")
    if val is None and headers is not None:
        try:
            val = headers.get("Retry-After")
        except Exception:
            val = None
    try:
        sec = int(str(val).strip())
    except (TypeError, ValueError):
        return None
    return sec if sec > 0 else None


class TranslatorError(Exception):
    """开放 API 调用异常：携带 HTTP 状态码、错误码与响应体。"""

    def __init__(self, message, status=None, error_code=None, body=None, retry_after=None):
        super().__init__(message)
        self.status = status
        self.error_code = error_code  # 如 insufficient_balance / rate_limited（历史属性名，保留）
        self.code = error_code        # ★ F-64①：正主键名（与文档 Error.code 同名），与 error_code 同值
        self.retry_after = retry_after  # ★ F-64①/F-47：429 时还需等待的秒数，无则 None
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
                # ★ F-64①：错误码正主键是 code（与文档 Error.code 枚举一致），error_code 是
                #   状态码诚实改造**之前**的老服务端在发的别名；改造后的服务端只发 code。
                #   所以取「code 优先、error_code 兜底」——混跑窗口里两边都能拿到码。
                raise TranslatorError(parsed.get("message", f"HTTP {e.code}"),
                                      status=e.code,
                                      error_code=parsed.get("code") or parsed.get("error_code"),
                                      body=parsed,
                                      retry_after=_retry_after(parsed, e.headers))
            except json.JSONDecodeError:
                # 非 JSON 错误体＝中间层（网关/CDN）替换过响应，只能按状态码抛
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
        :return: {task_id, mode, type:"text", status:"queued"}（202 响应）
        """
        body = {"text": text, "mode": mode}
        if target_langs:
            body["target_langs"] = list(target_langs)
        if title:
            body["title"] = title
        r = self._post_json("/openapi/v1/tasks", body)
        # ★ 契约对齐（2026-08-26 全仓评审 D1，★ F-64① 2026-09-26 更新）：
        #   后端成功响应为 202 且不含 success 字段，出参为 {task_id, mode, type, status:"queued"}
        #   ——以 task_id 存在为准；业务错误（缺文本、余额不足、超限、限流等）**已在 _request
        #   层按真实状态码抛出**（400/401/402/403/404/409/429/500），不再是「HTTP 200 + success:false」。
        #   下面这条判据因此只剩「上游被中间层改写成 2xx 空壳」的兜底作用，保留。
        if "task_id" not in r:
            raise TranslatorError(r.get("message", "创建任务失败"),
                                  error_code=r.get("code") or r.get("error_code"), body=r)
        return r

    def create_file_task(self, files, target_langs=None, mode: str = "pro",
                         title: str = "") -> dict:
        """创建文件批量翻译任务（≤20 个，总量 ≤40MB）。

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
        # ★ 契约对齐（D1）：同 create_task——成功响应无 success 字段，以 task_id 为准
        #   （★ F-64①：超限/缺文件等业务失败已按真实状态码在 _request 层抛出）
        if "task_id" not in r:
            raise TranslatorError(r.get("message", "创建任务失败"),
                                  error_code=r.get("code") or r.get("error_code"), body=r)
        return r

    # ---------- 任务轮询 / 结果 / 下载 ----------
    def get_task(self, task_id: int) -> dict:
        """查询任务状态。status ∈ queued / processing / completed / failed；
        失败时响应携带 error_code（如 insufficient_balance）与 message。"""
        r = self._get(f"/openapi/v1/tasks/status?id={task_id}")
        # ★ 契约对齐（D1）：轮询成功响应无 success 字段，以 status 存在为准。
        #   ★ F-64①（2026-09-26）：请求层面的失败（Key 无效 401、任务不存在 404、
        #   缺 id 400、限流 429……）一律已在 _request 层按真实状态码抛出；
        #   留在这里的这一判据只防「2xx 空壳」（中间层改写响应），不是业务错误通道。
        #   注意 **status:"failed" 仍走 200 正常返回**——那是任务的状态，不是请求的失败，
        #   终态信息由 wait_task 按 status 分支处理。
        if "status" not in r:
            raise TranslatorError(r.get("message", "查询失败"),
                                  error_code=r.get("code") or r.get("error_code"), body=r)
        return r

    def wait_task(self, task_id: int, interval: int = None,
                  timeout: int = 1800) -> dict:
        """阻塞轮询直至终态（completed / failed）。

        :param interval: 轮询间隔秒数；缺省按任务类型取值（文本 15s / 文件 60s，
          与后端建议口径一致；后端暂不在响应中下发 poll_interval_sec）
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
                # ★ 契约对齐（D1）：失败出参字段为 message/error_code（无 error 字段）
                #   ★ F-64①：该失败发生在**任务执行期**（模型不可达、余额在排队期间被扣光等），
                #   请求本身是成功的，所以状态码 200＋status:"failed"＋code/error_code 给业务码。
                raise TranslatorError(r.get("message") or "任务失败",
                                      error_code=r.get("code") or r.get("error_code"), body=r)
            if time.time() > deadline:
                raise TranslatorError(f"轮询超时（{timeout}s），任务仍在处理")
            if not gap:
                # 首轮按任务类型定默认间隔（文件任务产物大，60s 更合理）
                gap = 60 if r.get("type") == "files" else 15
            time.sleep(gap)

    def translate_and_wait(self, text: str, target_langs=None,
                           mode: str = "pro", timeout: int = 1800) -> dict:
        """一站式文本翻译：提交任务并阻塞等待完成，返回含 translations 的响应。"""
        created = self.create_task(text, target_langs, mode)
        return self.wait_task(created["task_id"], timeout=timeout)

    def download_file(self, task_id: int, save_path: str, file_id: int = None):
        """下载文件任务的翻译产物（file_id 缺省时多文件打包 zip）。

        :param save_path: 保存到本地的完整路径
        """
        qs = f"/openapi/v1/tasks/download?id={task_id}"
        if file_id:
            qs += f"&file_id={file_id}"
        data = self._request("GET", qs, raw=True)
        # ★ 整改 R-L2：产物未就绪/失败时下载接口返回 JSON（no_result/not_ready）而非二进制，
        # 若误存为文件会得到内容为 JSON 的「假产物」。识别到 JSON 错误体时直接抛错。
        # ★ F-64①（2026-09-26）：这类失败现在以 **409 Conflict**（not_ready/no_result）或
        #   400/401/403/404/429 发出，会在 _request 层直接抛 TranslatorError，走不到下面；
        #   保留这段是因为「老服务端 + 新 SDK」的混跑窗口仍存在，判据按 code→error_code 取码。
        try:
            parsed = json.loads(data.decode("utf-8"))
            if isinstance(parsed, dict) and not parsed.get("success", True):
                raise TranslatorError(
                    parsed.get("message", "产物未就绪或下载失败"),
                    error_code=parsed.get("code") or parsed.get("error_code"), body=parsed)
        except (json.JSONDecodeError, UnicodeDecodeError):
            pass  # 二进制产物，正常写入
        with open(save_path, "wb") as fh:
            fh.write(data)
        return save_path

    # ---------- 余额与辅助接口 ----------
    def balance(self) -> dict:
        """查询积分余额与 ≈句数：
        {balance_points, balance_sentences_approx}"""
        return self._get("/openapi/v1/balance")

    def kb_stats(self) -> dict:
        """查询本租户知识库统计（需 Key 具备 kb 权限）。"""
        r = self._post_json("/openapi/v1/kb/stats", {})
        # ★ F-64①：权限/额度类失败已按 401/403/429 抛出，这里的 success 判据只兜「2xx 空壳」
        if not r.get("success"):
            raise TranslatorError(r.get("message", "查询失败"),
                                  error_code=r.get("code") or r.get("error_code"), body=r)
        return r

    def usage(self) -> dict:
        """查询本租户用量汇总（需 Key 具备 billing 权限）。"""
        r = self._post_json("/openapi/v1/billing/usage", {})
        # ★ F-64①：同上（401/403/429 已在 _request 层抛出）
        if not r.get("success"):
            raise TranslatorError(r.get("message", "查询失败"),
                                  error_code=r.get("code") or r.get("error_code"), body=r)
        return r

    def rotate_api_key(self) -> dict:
        """轮换当前 Key（旧 Key 立即失效，请妥善保管新 Key）。"""
        r = self._post_json("/openapi/v1/apikey/rotate", {})
        # ★ F-64①：同上（401/403/429 已在 _request 层抛出）
        if not r.get("success"):
            raise TranslatorError(r.get("message", "轮换失败"),
                                  error_code=r.get("code") or r.get("error_code"), body=r)
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
