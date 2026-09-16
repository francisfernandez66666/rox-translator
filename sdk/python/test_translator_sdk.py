# ============================================================================
# test_translator_sdk.py — Python SDK 行为级单测（纯 stdlib：unittest + mock）
# 2026-09-16 测试盲区补全（对应《核实与修复_测试盲区补全_20260916.md》三.3）：
#   此前 Python SDK 零测试，请求头/错误映射/multipart 报文/轮询逻辑全靠线上验证。
# 运行：python3 -m unittest -v test_translator_sdk
# 策略：mock.patch("urllib.request.urlopen") 捕获 Request（url/headers/data）并按
#   URL 谓词返回预设响应；multipart 用例对请求体做字节级断言。
# ============================================================================
import contextlib
import io
import json
import os
import sys
import tempfile
import unittest
import urllib.error
from unittest import mock

sys.path.insert(0, os.path.dirname(__file__))
from translator_sdk import TranslatorClient, TranslatorError, __version__  # noqa: E402


class _FakeResp(io.BytesIO):
    """最小 urlopen 返回体：支持 read() 与 context manager。"""

    def __enter__(self):
        return self

    def __exit__(self, *a):
        self.close()


class _Recorder:
    """urlopen 替身：记录请求（url/headers/method/body）、按谓词返回响应。"""

    def __init__(self, responses):
        self.responses = responses  # list[(predicate(url, headers_dict, body), status, body)]
        self.calls = []

    def __call__(self, req, timeout=None):
        body = req.data or b""
        headers = {k.lower(): v for k, v in req.header_items()}
        self.calls.append({"url": req.full_url, "headers": headers,
                           "method": req.get_method(), "body": body})
        for pred, status, payload in self.responses:
            if pred(req.full_url, headers, body):
                if status >= 400:
                    raise urllib.error.HTTPError(req.full_url, status, "err",
                                                 {}, _FakeResp(payload))
                if isinstance(payload, bytes):
                    return _FakeResp(payload)
                return _FakeResp(json.dumps(payload).encode())
        raise urllib.error.HTTPError(req.full_url, 404, "not found", {}, _FakeResp(b"{}"))

    def bearer(self):
        return self.calls[-1]["headers"].get("authorization", "")


@contextlib.contextmanager
def sdk_client(responses):
    """返回 (TranslatorClient, _Recorder)，urlopen 已被 mock 替换（绝不打真网络）。"""
    rec = _Recorder(responses)
    with mock.patch("urllib.request.urlopen", rec):
        yield TranslatorClient("https://api.example.com/", "rk_test_key"), rec


class AuthAndBase(unittest.TestCase):
    def test_base_url_stripped_and_bearer_header(self):
        with sdk_client([((lambda u, h, b: u.endswith("/openapi/v1/balance")),
                          200, {"balance_tokens": 321})]) as (cli, rec):
            bal = cli.balance()
        self.assertEqual(bal["balance_tokens"], 321)
        self.assertEqual(rec.calls[0]["url"], "https://api.example.com/openapi/v1/balance")
        self.assertEqual(rec.bearer(), "Bearer rk_test_key")

    def test_version_declared(self):
        self.assertTrue(__version__)


class CreateTask(unittest.TestCase):
    def test_create_task_json_body(self):
        with sdk_client([((lambda u, h, b: u.endswith("/openapi/v1/tasks")),
                          202, {"task_id": 7, "mode": "pro", "type": "text", "status": "queued"})]) as (cli, rec):
            r = cli.create_task("你好", ["en", "ja"], "fast", "标题")
        self.assertEqual(r["task_id"], 7)
        body = json.loads(rec.calls[0]["body"])
        self.assertEqual(body, {"text": "你好", "mode": "fast",
                                "target_langs": ["en", "ja"], "title": "标题"})
        self.assertEqual(rec.calls[0]["method"], "POST")

    def test_create_task_business_error(self):
        # 200 + success:false（无 task_id）→ 按 error_code 抛 TranslatorError
        with sdk_client([((lambda u, h, b: True), 200,
                          {"success": False, "error_code": "forbidden", "message": "无权限"})]) as (cli, _):
            with self.assertRaises(TranslatorError) as cm:
                cli.create_task("x")
        self.assertEqual(cm.exception.error_code, "forbidden")

    def test_http_error_maps_status_and_code(self):
        payload = json.dumps({"error_code": "insufficient_balance", "message": "余额不足"}).encode()
        with sdk_client([((lambda u, h, b: True), 402, payload)]) as (cli, _):
            with self.assertRaises(TranslatorError) as cm:
                cli.create_task("x")
        self.assertEqual(cm.exception.status, 402)
        self.assertEqual(cm.exception.error_code, "insufficient_balance")

    def test_url_error_wrapped(self):
        with mock.patch("urllib.request.urlopen", side_effect=urllib.error.URLError("dns fail")):
            cli = TranslatorClient("https://api.example.com", "rk")
            with self.assertRaises(TranslatorError) as cm:
                cli.balance()
        self.assertIn("连接失败", str(cm.exception))


class Multipart(unittest.TestCase):
    def _task_resp(self):
        return (lambda u, h, b: u.endswith("/openapi/v1/tasks")), 202, {
            "task_id": 5, "type": "files", "status": "queued", "file_count": 1}

    def test_create_file_task_byte_level(self):
        with sdk_client([self._task_resp()]) as (cli, rec):
            d = tempfile.mkdtemp()
            fp = os.path.join(d, "样例.txt")
            raw = "文件内容字节".encode()
            with open(fp, "wb") as fh:
                fh.write(raw)
            r = cli.create_file_task([fp], ["en"], "fast")
        self.assertEqual(r["task_id"], 5)
        body = rec.calls[0]["body"]
        ctype = rec.calls[0]["headers"].get("content-type", "")
        self.assertTrue(ctype.startswith("multipart/form-data; boundary=----TranslatorSDKBoundary"),
                        ctype)
        boundary = ctype.split("boundary=")[1]
        # boundary 闭环 + 表单字段 + 文件名 + 二进制透传（字节级）
        self.assertIn(f"--{boundary}\r\n".encode(), body)
        self.assertIn(f"--{boundary}--".encode(), body)
        self.assertIn(b'name="target_langs"', body)
        self.assertIn(b'name="mode"', body)
        self.assertIn('filename="样例.txt"'.encode(), body)
        self.assertIn(raw, body)

    def test_create_file_task_business_error(self):
        with sdk_client([((lambda u, h, b: True), 200,
                          {"success": False, "error_code": "insufficient_balance",
                           "message": "余额不足"})]) as (cli, _):
            d = tempfile.mkdtemp()
            fp = os.path.join(d, "a.txt")
            with open(fp, "w") as fh:
                fh.write("hi")
            with self.assertRaises(TranslatorError) as cm:
                cli.create_file_task([fp], ["en"])
        self.assertEqual(cm.exception.error_code, "insufficient_balance")


class Polling(unittest.TestCase):
    def test_wait_task_polls_until_completed(self):
        seq = iter([{"task_id": 1, "status": "queued", "type": "text"},
                    {"task_id": 1, "status": "processing"},
                    {"task_id": 1, "status": "completed", "translations": {"en": "hello"}}])
        cli = TranslatorClient("https://api.example.com", "rk")
        with mock.patch.object(cli, "get_task", side_effect=lambda tid: next(seq)):
            with mock.patch("time.sleep"):
                r = cli.wait_task(1, interval=0)
        self.assertEqual(r["status"], "completed")

    def test_wait_task_failed_raises_with_error_code(self):
        cli = TranslatorClient("https://api.example.com", "rk")
        with mock.patch.object(cli, "get_task",
                               return_value={"task_id": 9, "status": "failed",
                                             "error_code": "task_failed", "message": "失败"}):
            with self.assertRaises(TranslatorError) as cm:
                cli.wait_task(9, interval=0)
        self.assertEqual(cm.exception.error_code, "task_failed")

    def test_wait_task_timeout(self):
        cli = TranslatorClient("https://api.example.com", "rk")
        with mock.patch.object(cli, "get_task", return_value={"task_id": 1, "status": "queued"}):
            with mock.patch("time.sleep"):
                with self.assertRaises(TranslatorError) as cm:
                    cli.wait_task(1, interval=0, timeout=-1)
        self.assertIn("轮询超时", str(cm.exception))


class DownloadGuard(unittest.TestCase):
    def test_download_json_error_body_rejected(self):
        # R-L2：JSON 错误体（no_result）不得被当二进制产物写入磁盘
        with sdk_client([((lambda u, h, b: "/download" in u), 200,
                          json.dumps({"success": False, "error_code": "no_result",
                                      "message": "无可下载"}).encode())]) as (cli, _):
            out = os.path.join(tempfile.mkdtemp(), "out.zip")
            with self.assertRaises(TranslatorError) as cm:
                cli.download_file(1, out)
        self.assertEqual(cm.exception.error_code, "no_result")
        self.assertFalse(os.path.exists(out))

    def test_download_binary_saved(self):
        payload = b"PK\x03\x04fakezip"
        with sdk_client([((lambda u, h, b: "/download" in u), 200, payload)]) as (cli, _):
            out = os.path.join(tempfile.mkdtemp(), "out.zip")
            self.assertEqual(cli.download_file(1, out), out)
        with open(out, "rb") as fh:
            self.assertEqual(fh.read(), payload)


if __name__ == "__main__":
    unittest.main(verbosity=2)
