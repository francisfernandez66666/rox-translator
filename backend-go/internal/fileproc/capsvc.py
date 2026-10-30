#!/usr/bin/env python3
"""capsvc.py — 能力常驻服务（组 6 / 方案 A，2026-10-30 三事合一批）。

为什么需要它（方案二 §2.3 / §4）：
  OCR / ASR / TTS 每一次调用都新起一个 Python 进程，并在进程内重新
  `InferenceSession` 加载 det/cls/rec 三份 ONNX —— 冷启动是这条链上最大的固定成本，
  而同一台机器（1.6G）要反复付这笔钱。常驻进程把「模型加载」从**每次请求**摊到
  **每台机器一次**，其它一切都保持原样：识别逻辑、`ERR:<Variant>` 档位、资产检查
  全部仍是既有脚本那一份。

三条硬边界（改这个文件之前先读）：
  ① **不复制识别逻辑**：本服务 `import` 既有脚本，直接调它的 `cmd_probe()` / `cmd_image(path)`，
     用**线程局部缓冲**把 stdout 收进本次请求的回包。识别阈值、后处理、`ERR:<Variant>`
     全部自动同源；脚本的 `main()` 保留，子进程回落路径继续可用（AGENTS §一·12「桩只认协议」
     那一族：warm 与 subprocess 是同一套错误档位，不是两套）。
  ② **协议只有一份**：Unix domain socket，帧＝uint32 大端长度 + UTF-8 JSON，单帧上限 8 MiB；
     **一个连接只跑一个请求**（超时语义因此简单，不需要多路复用）。
  ③ **超时不杀进程**：ONNX 推理不可中断，Go 侧超时只关连接、服务端那次推理跑完结果丢弃
     （记 `late_result`）。为一次超时 `Kill` 整个守护进程会连带打掉其它并发请求。

环境变量（全部有默认值，生产由 unit/env 注入）：
  CAPSVC_SOCKET               socket 路径，默认 /run/langcross/capsvc.sock
  CAPSVC_IDLE_UNLOAD_SEC      空闲卸载秒数，默认 600
  CAPSVC_MAX_RSS_MB           内存预算（MB），默认 700
  CAPSVC_OCR_WORKERS          ocr 并发 worker 数，默认 2（asr/tts 固定 1）
  CAPSVC_OCR_EST_MB           ocr 已加载能力的估算占用（MB），默认 320

启动自检：注册表里每个能力的「依赖模块可导入 + 资产存在」不满足 ⇒ **非 0 退出、不拉起**
（Go 侧 socket 不存在即回落子进程，用户无感）。这一步刻意**不加载权重**——启动就吃几百 MB
只为一次可能永远不来的请求不划算，权重加载推迟到第一次调用（`cold=1`）。

自测：`python3 capsvc.py --selftest`（纯标准库，不需要 onnxruntime/权重）。
判绿只认末行 `CAPSVC_SELFTEST_ALL_OK=1`。

远端派发 `fpdexec`（fileproc_remote.go）**不接 capsvc**：那条腿在另一台机器上、协议不同，
保持子进程（AGENTS §一·12 的桩只认协议，不许被绕过）。
"""
import argparse
import base64
import importlib
import importlib.util  # noqa: F401 —— OcrCapability.probe 用 find_spec 判依赖是否装得上
import io
import json
import os
import random
import socket
import struct
import sys
import tempfile
import threading
import time
import traceback

# 帧头：uint32 **大端**长度 + JSON body。改字节序＝协议不兼容（Go 侧 capsvc_protocol_test.go
# 有一条把长度改成小端就必须红的反证腿——协议只有一份，两侧同源）。
FRAME_HEADER = struct.Struct(">I")
FRAME_MAX = 8 << 20  # 单帧上限 8 MiB（超限直接断开并记 oversize）

DEFAULT_SOCKET = "/run/langcross/capsvc.sock"
SELFTEST_OK = "CAPSVC_SELFTEST_ALL_OK=1"


# ---------------------------------------------------------------- 线程局部输出捕获
# ★ 2026-10-31 修正（组6 收口）：**禁止**用 contextlib.redirect_stdout 抓能力脚本的输出。
#   它改的是**进程级** sys.stdout，而 capsvc 是「一连接一线程」（handle_conn 起线程）：
#   两个 OCR 调用并发时，A 线程的 JSON 会写进 B 线程的缓冲（或两边各拿到半截），
#   回包解析失败并被归到 Internal 档——A5 的 mock 用例单人跑永远看不见这一形态。
#   这里改成线程局部缓冲：每个线程各写各的，未开启捕获的线程照常走原始 stdout。
_orig_stdout = sys.stdout
_orig_stderr = sys.stderr


class _ThreadCapture(io.TextIOBase):
    """把 write 路由到当前线程的缓冲；没有缓冲就落回原始流。"""

    def __init__(self, fallback):
        self._fallback = fallback
        self._local = threading.local()

    def write(self, s):
        buf = getattr(self._local, "buf", None)
        if buf is not None:
            return buf.write(s)
        return self._fallback.write(s)

    def flush(self):
        buf = getattr(self._local, "buf", None)
        if buf is not None:
            return None
        return self._fallback.flush()

    def isatty(self):
        return False


_capture_out = None
_capture_err = None


def _install_capture():
    """一次性把 sys.stdout/stderr 换成线程局部代理（幂等）。"""
    global _capture_out, _capture_err
    if _capture_out is None:
        _capture_out = _ThreadCapture(_orig_stdout)
        _capture_err = _ThreadCapture(_orig_stderr)
        sys.stdout = _capture_out
        sys.stderr = _capture_err


class _Capture:
    """with _Capture() as (out, err): ——只捕获**本线程**的输出（可嵌套，退出时恢复上一层）。"""

    def __init__(self):
        _install_capture()
        self.out = io.StringIO()
        self.err = io.StringIO()

    def __enter__(self):
        self._prev_out = getattr(_capture_out._local, "buf", None)
        self._prev_err = getattr(_capture_err._local, "buf", None)
        _capture_out._local.buf = self.out
        _capture_err._local.buf = self.err
        return self.out, self.err

    def __exit__(self, *_exc):
        _capture_out._local.buf = self._prev_out
        _capture_err._local.buf = self._prev_err
        return False


# ---------------------------------------------------------------- 环境与小工具
def _env_int(name, default, lo=None, hi=None):
    """读整型 env：非数字/越界一律回落默认（脏值不许把服务打挂）。"""
    raw = (os.environ.get(name) or "").strip()
    if raw:
        try:
            v = int(raw)
            if lo is not None and v < lo:
                v = lo
            if hi is not None and v > hi:
                v = hi
            return v
        except ValueError:
            pass
    return default


def _now_ms():
    return int(time.time() * 1000)


# ---------------------------------------------------------------- 能力基类
class CapFailure(Exception):
    """能力层失败：variant 与既有脚本的 `ERR:<Variant>` 档位**逐字同源**。

    对外契约＝`Timeout`（预算到点）／`Asset`（权重/词典缺失）／`Import`（依赖模块缺失）／
    `Unsupported`（能力或子命令未实现）／`BadRequest`（入参不合法）／`Internal`（兜底）。
    前四个是既有脚本里就有的字面量（friendlyOcrError 逐个认），后两个只由本服务产生，
    因此**永远不会**出现在子进程路径上（Go 侧按 variant 直接渲染，不做二次翻译）。
    """

    def __init__(self, variant, message):
        super().__init__(message)
        self.variant = variant
        self.message = message


class Capability(object):
    """一个能力的生命周期：懒加载 + 加载锁 + 空闲卸载 + 内存估算。

    线程模型：调用方（每连接一个线程）先 `ensure_loaded()`（**加锁**，同能力只会加载一次），
    再在 `sem`（worker 数）内 `call()`。ONNX `InferenceSession.run` 本身线程安全，
    但 CPU 只有那么几核 ⇒ worker 数是**显存/内存与延迟**的旋钮，不是正确性旋钮。
    """

    name = "base"
    est_mb = 0

    def __init__(self, server, name):
        self.server = server
        self.name = name
        self.loaded = False
        self.last_used = 0.0
        self.busy = 0
        self._load_lock = threading.Lock()
        self._sem = threading.BoundedSemaphore(max(1, self.server.workers_for(name)))

    def probe(self):
        """启动自检：依赖可导入 + 资产存在。**不加载权重**。失败抛 CapFailure。"""
        return

    def _do_load(self):
        raise NotImplementedError

    def _do_unload(self):
        raise NotImplementedError

    def ensure_loaded(self):
        """返回 cold（本次是否触发了加载）。加载失败抛 CapFailure。"""
        with self._load_lock:
            if self.loaded:
                self.last_used = time.time()
                return False
            self._do_load()
            self.loaded = True
            self.last_used = time.time()
            self.server.bump("model_loads")
            return True

    def unload(self):
        """卸载（LRU 预算/空闲）：释放权重引用，进程内存交给 GC。"""
        with self._load_lock:
            if not self.loaded:
                return
            try:
                self._do_unload()
            finally:
                self.loaded = False

    def call(self, cmd, args, stdin):
        raise NotImplementedError


class ScriptCapability(Capability):
    """以「import 既有脚本、调它的 cmd_<子命令>」实现的能力。

    这是本文件的核心取向：识别/转写/合成的**逻辑只有一份**（脚本本体），
    常驻进程只负责「别每次都重新 import 与重新建会话」。
    """

    module_name = ""

    def __init__(self, server, name, module_name=None):
        super().__init__(server, name)
        if module_name:
            self.module_name = module_name
        self._mod = None

    # -- 加载 / 卸载 ------------------------------------------------------
    def _import_script(self):
        try:
            return importlib.import_module(self.module_name)
        except Exception as e:  # 脚本缺失/语法错：与子进程路径同档（脚本自己的 Import 档）
            raise CapFailure("Import", "能力脚本 %s 导入失败: %s" % (self.module_name, e))

    def probe(self):
        mod = self._import_script()
        with _Capture() as (buf, _err):
            rc = mod.cmd_probe()
        if rc != 0:
            detail = buf.getvalue().strip().replace("\n", " | ")
            raise CapFailure("Asset", "能力自检未通过（%s）: %s" % (self.module_name, detail))
        self._mod = mod

    def _do_load(self):
        # 启动自检已 import 过一次；这里再取一次是幂等的（sys.modules 命中）。
        if self._mod is None:
            self._mod = self._import_script()
        self._on_loaded(self._mod)

    def _on_loaded(self, mod):
        """子类钩子：装载期副作用（例如给 OCR 装上 InferenceSession 缓存）。"""

    def _do_unload(self):
        self._on_unloaded()
        self._mod = None

    def _on_unloaded(self):
        """子类钩子：卸载期清理。"""

    # -- 调用 --------------------------------------------------------------
    def call(self, cmd, args, stdin):
        if self._mod is None:
            raise CapFailure("Internal", "能力未加载")
        fn = getattr(self._mod, "cmd_" + cmd, None)
        if fn is None:
            # 「未实现」与「参数错」必须分开：前者是能力档位（二期才有），后者是调用方 bug。
            raise CapFailure("Unsupported", "能力 %s 未实现子命令 %s" % (self.name, cmd))
        out, err = io.StringIO(), io.StringIO()
        try:
            with _Capture() as (out, err):
                rc = fn(*args)
        except TypeError as e:
            raise CapFailure("BadRequest", "%s %s 参数不合法: %s" % (self.name, cmd, e))
        except CapFailure:
            raise
        except Exception as e:  # 脚本内未捕获异常：与子进程路径同档，绝不把栈吐给客户端
            raise CapFailure("Internal", "%s %s 执行异常: %s" % (self.name, cmd, e))
        return int(rc or 0), out.getvalue(), err.getvalue()


class OcrCapability(ScriptCapability):
    """OCR：复用 ocr_recognize.py（cmd_probe / cmd_image 与子进程路径同一份）。

    常驻收益全部来自这里的一件小事：**把 `onnxruntime.InferenceSession` 的构造缓存起来**。
    `cmd_image` 每次都会 `ort.InferenceSession(str(DET_MODEL))` 建三份会话；在常驻进程里
    第一次建好之后复用，后面每次调用就只剩推理。

    为什么用「包装构造器」而不是改 ocr_recognize：
      ① 识别逻辑（阈值/后处理/框粒度）一字不动——那一份归 OCR 质量链批次所有，
         本批不碰它，避免两批同时改同一个文件；
      ② 需要缓存的只有「构造 InferenceSession 这一跳」，它是常驻进程唯一新增的东西；
      ③ `cmd_image` 里是 `import onnxruntime as ort`（函数内导入），拿到的是 sys.modules
         里同一个模块对象，所以改模块属性对它是可见的。
    卸载时把原构造器换回去并清缓存——LRU 卸载必须**真能释放**，否则预算读数就是假的。
    """

    name = "ocr"
    module_name = "ocr_recognize"

    def __init__(self, server, name):
        super().__init__(server, name, self.module_name)
        self.est_mb = server.est_mb_for(name, 320)
        self._sess_cache = None
        self._sess_lock = threading.Lock()
        self._ort_restore = None

    def probe(self):
        super().probe()
        # 依赖模块清单由脚本自己维护（MODULES），这里只做「装不装得上」的预检，
        # 与子进程路径的 Import 档同源；真实资产判定交给脚本的 cmd_probe。
        mod = self._mod
        for m in getattr(mod, "MODULES", []):
            try:
                if importlib.util.find_spec(m) is None:
                    raise CapFailure("Import", "缺少依赖模块: %s" % m)
            except (ImportError, ValueError) as e:
                raise CapFailure("Import", "依赖模块 %s 探测失败: %s" % (m, e))

    def _on_loaded(self, mod):
        # 只有**声明了 onnxruntime 依赖**的脚本才有会话可缓存（真实 ocr_recognize 一定在
        # MODULES 里列出它）。协议级夹具模块（MODULES=[]）因此不需要装 onnxruntime 就能跑，
        # 而"真脚本却少了 onnxruntime"仍然在 probe 阶段就被 Import 档拦下——两件事不许混。
        mods = getattr(mod, "MODULES", None) or []
        if "onnxruntime" not in mods:
            return
        self._install_session_cache()

    def _on_unloaded(self):
        self._sess_cache = None
        if self._ort_restore is not None:
            try:
                ort, orig = self._ort_restore
                ort.InferenceSession = orig
            except Exception:
                pass
            self._ort_restore = None

    def _install_session_cache(self):
        if self._sess_cache is not None:
            return
        try:
            import onnxruntime as ort
        except Exception as e:
            raise CapFailure("Import", "onnxruntime 不可用: %s" % e)
        cache = {}
        lock = threading.Lock()
        orig = ort.InferenceSession

        def cached_session(path, *a, **kw):
            key = str(path)
            with lock:
                s = cache.get(key)
            if s is not None:
                return s
            s = orig(path, *a, **kw)
            with lock:
                cache[key] = s
            return s

        ort.InferenceSession = cached_session
        self._sess_cache = cache
        self._ort_restore = (ort, orig)


# ---------------------------------------------------------------- 内置能力（svc 控制面）
def _svc_status(server):
    """控制面读数：**只出计数与能力状态，绝不出路径/主机名/权重文件名**。

    与 /api/health 的 dispatch 那一族同口径（AGENTS §一·12）：这个面会被匿名健康
    检查读，任何可定位坐标都是泄漏。

    ★ 注册表里**声明了但还没被调用过**的能力也要出现在 caps 里（loaded=false）：
      否则「服务刚起、还没跑过任何一单」与「注册表是空的」在健康面上同形，
      而 face 探针要判断的正是"这台到底有没有常驻能力"。
    """
    now = time.time()
    caps = {}
    declared = [name for name, f in server.factories.items() if f is not None]
    with server.caps_lock:
        instantiated = dict(server.caps)
    for name in declared:
        cap = instantiated.get(name)
        if cap is None:
            caps[name] = {"loaded": False, "busy": 0, "idle_s": 0, "est_mb": 0}
            continue
        caps[name] = {
            "loaded": bool(cap.loaded),
            "busy": int(cap.busy),
            "idle_s": int(max(0, now - cap.last_used)) if cap.loaded else 0,
            "est_mb": int(cap.est_mb),
        }
    payload = {
        "version": 1,
        "model_loads": int(server.counters["model_loads"]),
        "cold_count": int(server.counters["cold_count"]),
        "late_result": int(server.counters["late_result"]),
        "oversize": int(server.counters["oversize"]),
        "evicted": int(server.counters["evicted"]),
        "base_rss_mb": int(server.base_rss_mb),
        "caps": caps,
    }
    return 0, json.dumps(payload, ensure_ascii=False), ""


# ---------------------------------------------------------------- 服务本体
class Server(object):
    """单进程、多能力注册、每连接一个线程、每连接只处理一个请求。"""

    def __init__(self, socket_path, factories, selftest=False,
                 max_rss_mb=None, idle_unload_sec=None):
        self.socket_path = socket_path
        self.factories = factories          # name -> callable(server, name) -> Capability
        self.selftest = selftest
        self.max_rss_mb = max_rss_mb if max_rss_mb is not None else _env_int("CAPSVC_MAX_RSS_MB", 700, 64, 65536)
        self.idle_unload_sec = idle_unload_sec if idle_unload_sec is not None else _env_int("CAPSVC_IDLE_UNLOAD_SEC", 600, 0, 7 * 86400)
        self.caps = {}
        self.caps_lock = threading.Lock()
        self.counters = {
            "model_loads": 0, "cold_count": 0, "late_result": 0, "oversize": 0, "evicted": 0,
        }
        self.counter_lock = threading.Lock()
        self.base_rss_mb = 0.0
        self._sock = None
        self._stop = threading.Event()
        self._thread = None
        self._idle_thread = None

    # -- 计数 --------------------------------------------------------------
    def bump(self, key, delta=1):
        with self.counter_lock:
            self.counters[key] = int(self.counters.get(key, 0)) + delta

    def log(self, **fields):
        parts = ["[capsvc]"]
        for k in sorted(fields):
            parts.append("%s=%s" % (k, fields[k]))
        print(" ".join(parts), file=sys.stderr, flush=True)

    # -- 容量/预算 ---------------------------------------------------------
    def workers_for(self, name):
        if name == "ocr":
            return _env_int("CAPSVC_OCR_WORKERS", 2, 1, 16)
        return 1

    def est_mb_for(self, name, default):
        if name == "ocr":
            return _env_int("CAPSVC_OCR_EST_MB", default, 16, 65536)
        return default

    def _used_mb(self):
        """已用内存估算 = max(进程 RSS, 基线 + 各能力估算)。

        为什么两把尺子取大：RSS 在 macOS 没有 /proc、且 Python 释放后不归还 OS（maxrss 单调），
        单靠它会永远"超预算"；单靠估算又会漏掉脚本自己申请的大缓冲。取大者＝既不漏也不误报。
        """
        est = self.base_rss_mb
        with self.caps_lock:
            for cap in self.caps.values():
                if cap.loaded:
                    est += cap.est_mb
        return max(self._rss_mb(), est)

    @staticmethod
    def _rss_mb():
        try:
            with open("/proc/self/statm", "r") as f:
                pages = int(f.read().split()[1])
            return pages * os.sysconf("SC_PAGE_SIZE") / (1024.0 * 1024.0)
        except Exception:
            pass
        try:
            import resource
            v = float(resource.getrusage(resource.RUSAGE_SELF).ru_maxrss)
            # macOS 单位是字节，Linux 是 KB
            return v / (1024.0 * 1024.0) if sys.platform == "darwin" else v / 1024.0
        except Exception:
            return 0.0

    def _evict_for(self, target_name, need_mb):
        """按 LRU 卸载已加载能力，直到装得下目标能力。

        只卸载「已加载 且 不是目标 且 不忙」的能力；一个都卸不动就照常加载并出一行 warn
        ——**宁可短暂超预算，不许让一个本该能干的活永远干不了**（fail-open，同 §一·10）。
        """
        while self._used_mb() + need_mb > self.max_rss_mb:
            with self.caps_lock:
                victims = [c for c in self.caps.values()
                           if c.loaded and c.name != target_name and c.busy == 0]
            if not victims:
                self.log(event="budget_over", used_mb=int(self._used_mb()),
                         need_mb=int(need_mb), max_mb=int(self.max_rss_mb))
                return
            victim = min(victims, key=lambda c: c.last_used)
            victim.unload()
            self.bump("evicted")
            self.log(event="evict_lru", cap=victim.name, used_mb=int(self._used_mb()))

    # -- 能力生命周期 ------------------------------------------------------
    def _cap(self, name):
        with self.caps_lock:
            cap = self.caps.get(name)
            if cap is not None:
                return cap
            factory = self.factories.get(name)
            if factory is None:
                return None
            cap = factory(self, name)
            self.caps[name] = cap
            return cap

    # -- 帧读写 ------------------------------------------------------------
    @staticmethod
    def _recv_exact(conn, n):
        chunks = []
        got = 0
        while got < n:
            b = conn.recv(n - got)
            if not b:
                return None
            chunks.append(b)
            got += len(b)
        return b"".join(chunks)

    @staticmethod
    def _send_frame(conn, obj):
        body = json.dumps(obj, ensure_ascii=False).encode("utf-8")
        if len(body) > FRAME_MAX:
            body = json.dumps({
                "id": obj.get("id", ""), "ok": False, "stdout": "", "stderr": "",
                "variant": "Oversize", "elapsed_ms": 0, "cold": 0,
            }, ensure_ascii=False).encode("utf-8")
        conn.sendall(FRAME_HEADER.pack(len(body)) + body)

    # -- 单连接处理 --------------------------------------------------------
    def handle_conn(self, conn):
        conn.settimeout(300.0)
        try:
            hdr = self._recv_exact(conn, FRAME_HEADER.size)
            if hdr is None:
                # 半包（对端在发完头之前断开）：不 panic、不计数，直接收摊。
                return
            (n,) = FRAME_HEADER.unpack(hdr)
            if n <= 0:
                self._send_frame(conn, {"id": "", "ok": False, "stdout": "", "stderr": "",
                                        "variant": "BadRequest", "elapsed_ms": 0, "cold": 0})
                return
            if n > FRAME_MAX:
                # 超长帧：**直接断开**（spec 口径），只记一行 oversize 读数。
                self.bump("oversize")
                self.log(event="oversize", bytes=n, max=FRAME_MAX)
                return
            body = self._recv_exact(conn, n)
            if body is None:
                return
            self._dispatch(conn, body)
        except (socket.timeout, ConnectionError, OSError):
            return
        except Exception:
            self.log(event="handle_error", detail=traceback.format_exc(limit=2).replace("\n", " | "))
        finally:
            try:
                conn.close()
            except Exception:
                pass

    def _dispatch(self, conn, body):
        try:
            req = json.loads(body.decode("utf-8"))
            if not isinstance(req, dict):
                raise ValueError("请求必须是 JSON 对象")
            rid = str(req.get("id") or "")
            cap_name = str(req.get("cap") or "")
            cmd = str(req.get("cmd") or "")
            args = req.get("args") or []
            if not isinstance(args, list) or any(not isinstance(a, str) for a in args):
                raise ValueError("args 必须是字符串数组")
            stdin = b""
            raw_stdin = req.get("stdin_b64") or ""
            if raw_stdin:
                stdin = base64.b64decode(raw_stdin, validate=True)
            timeout_ms = int(req.get("timeout_ms") or 60000)
        except Exception as e:
            self._send_frame(conn, {"id": "", "ok": False, "stdout": "", "stderr": "",
                                    "variant": "BadRequest", "elapsed_ms": 0, "cold": 0})
            self.log(event="bad_request", detail=str(e)[:200])
            return
        timeout_ms = max(100, min(timeout_ms, 600000))
        started = time.time()

        # 控制面：不加载任何能力，只回计数（/api/health.capsvc 用它）。
        if cap_name == "svc":
            if cmd == "status":
                rc, out, err = _svc_status(self)
                self._send_frame(conn, {"id": rid, "ok": True, "stdout": out, "stderr": err,
                                        "variant": "", "elapsed_ms": int((time.time() - started) * 1000),
                                        "cold": 0})
            else:
                self._send_frame(conn, {"id": rid, "ok": False, "stdout": "", "stderr": "",
                                        "variant": "Unsupported", "elapsed_ms": 0, "cold": 0})
            return

        cap = self._cap(cap_name)
        if cap is None:
            # 二期能力（asr/tts）在注册表里**留位**（值为 None 的工厂），落在这里：
            # 明确回 Unsupported，而不是「未知能力」那种含糊话——运维看得懂该等哪一批。
            self._send_frame(conn, {"id": rid, "ok": False, "stdout": "", "stderr": "",
                                    "variant": "Unsupported", "elapsed_ms": 0, "cold": 0})
            self.log(event="unsupported", cap=cap_name, cmd=cmd)
            return

        result = {}
        done = threading.Event()

        def worker():
            cold = False
            wait_ms = 0
            try:
                self._evict_for(cap_name, cap.est_mb)
                cold = cap.ensure_loaded()
                if cold:
                    self.bump("cold_count")
                with cap._sem:
                    cap.busy += 1
                    try:
                        rc, out, err = cap.call(cmd, args, stdin)
                    finally:
                        cap.busy -= 1
                        cap.last_used = time.time()
                variant = ""
                if rc != 0:
                    for line in err.splitlines():
                        if line.startswith("ERR:"):
                            variant = line[4:].split(":", 1)[0]
                            break
                    if not variant:
                        variant = "Internal"
                result.update({"ok": rc == 0, "stdout": out, "stderr": err,
                               "variant": variant, "cold": 1 if cold else 0, "wait_ms": wait_ms})
            except CapFailure as f:
                detail = "ERR:%s:%s\n" % (f.variant, f.message)
                result.update({"ok": False, "stdout": "", "stderr": detail,
                               "variant": f.variant, "cold": 1 if cold else 0, "wait_ms": 0})
            except Exception as e:  # 兜底：绝不把栈吐给客户端
                detail = "ERR:Internal:%s\n" % e
                result.update({"ok": False, "stdout": "", "stderr": detail,
                               "variant": "Internal", "cold": 1 if cold else 0, "wait_ms": 0})
            finally:
                result["elapsed_ms"] = int((time.time() - started) * 1000)
                done.set()

        th = threading.Thread(target=worker, daemon=True)
        th.start()
        finished = done.wait(timeout_ms / 1000.0)
        if not finished:
            # 超时：回一个**协议内**的 Timeout 响应（与脚本自己的 ERR:Timeout 同档），
            # 推理继续跑、结果丢弃；跑完只记 late_result（不杀进程，见文件头 ③）。
            self._send_frame(conn, {"id": rid, "ok": False, "stdout": "", "stderr": "",
                                    "variant": "Timeout", "elapsed_ms": int((time.time() - started) * 1000),
                                    "cold": 0})
            self.log(event="timeout", cap=cap_name, cmd=cmd, timeout_ms=timeout_ms)

            def watch():
                done.wait()
                self.bump("late_result")
                self.log(event="late_result", cap=cap_name, cmd=cmd,
                         elapsed_ms=result.get("elapsed_ms", 0))
            threading.Thread(target=watch, daemon=True).start()
            return

        self._send_frame(conn, {"id": rid, "ok": bool(result.get("ok")),
                                "stdout": result.get("stdout", ""),
                                "stderr": result.get("stderr", ""),
                                "variant": result.get("variant", ""),
                                "elapsed_ms": int(result.get("elapsed_ms", 0)),
                                "cold": int(result.get("cold", 0))})
        self.log(cap=cap_name, cmd=cmd, ok=1 if result.get("ok") else 0,
                 cold=result.get("cold", 0), wait_ms=result.get("wait_ms", 0),
                 elapsed_ms=result.get("elapsed_ms", 0),
                 variant=result.get("variant", "") or "-")

    # -- 生命周期 ----------------------------------------------------------
    def start(self):
        """绑 socket 并进入 accept 循环（阻塞）。"""
        parent = os.path.dirname(self.socket_path)
        if parent and not os.path.isdir(parent):
            os.makedirs(parent, exist_ok=True)
        if os.path.exists(self.socket_path):
            try:
                os.unlink(self.socket_path)
            except OSError:
                pass
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.bind(self.socket_path)
        os.chmod(self.socket_path, 0o660)
        s.listen(64)
        self._sock = s
        self.base_rss_mb = self._rss_mb()
        self.log(event="listening", caps=",".join(sorted(self.factories.keys())),
                 max_rss_mb=self.max_rss_mb, idle_unload_sec=self.idle_unload_sec)
        if self.idle_unload_sec > 0:
            self._idle_thread = threading.Thread(target=self._idle_loop, daemon=True)
            self._idle_thread.start()
        try:
            while not self._stop.is_set():
                try:
                    conn, _ = s.accept()
                except OSError:
                    break
                threading.Thread(target=self.handle_conn, args=(conn,), daemon=True).start()
        finally:
            try:
                s.close()
            except Exception:
                pass
            if os.path.exists(self.socket_path):
                try:
                    os.unlink(self.socket_path)
                except OSError:
                    pass

    def _idle_loop(self):
        while not self._stop.wait(5.0):
            now = time.time()
            with self.caps_lock:
                caps = list(self.caps.values())
            for cap in caps:
                if cap.loaded and cap.busy == 0 and now - cap.last_used >= self.idle_unload_sec:
                    cap.unload()
                    self.log(event="idle_unload", cap=cap.name, idle_s=int(now - cap.last_used))

    def stop(self):
        self._stop.set()
        try:
            if self._sock is not None:
                self._sock.close()
        except Exception:
            pass


# ---------------------------------------------------------------- 启动自检
def startup_check(factories):
    """依赖可导入 + 资产存在，任一不满足 ⇒ 抛 CapFailure（main 非 0 退出、不拉起）。

    刻意**不加载权重**：见文件头。`cmd_probe` 是脚本自己的就绪判据（与子进程路径同源）。
    """
    if not factories:
        raise CapFailure("BadRequest", "能力注册表为空")
    dummy = Server("", {}, selftest=True)
    for name, factory in factories.items():
        if factory is None:
            continue  # 二期留位
        cap = factory(dummy, name)
        cap.probe()


# ---------------------------------------------------------------- 自测
class _MockCap(Capability):
    """自测用假能力：不依赖任何第三方库，专治协议/冷启动/预算三件。

    `est_mb` 由名字决定（mockA/mockB 各 120MB）——内存预算那条腿就靠它们俩；
    真实分配只给 8MB（装配读数用），因为**判据吃的是估算口径**，不是真吃满 240MB。
    """

    def __init__(self, server, name):
        super().__init__(server, name)
        self.est_mb = 120 if name in ("mockA", "mockB") else 16
        self._blob = None

    def probe(self):
        return

    def _do_load(self):
        self._blob = bytearray(min(self.est_mb, 8) << 20)

    def _do_unload(self):
        self._blob = None

    def call(self, cmd, args, stdin):
        if cmd == "echo":
            return 0, json.dumps({"echo": args, "stdin_len": len(stdin)}, ensure_ascii=False), ""
        if cmd == "sleep":
            time.sleep(float(args[0]) if args else 0.5)
            return 0, json.dumps({"slept": True}), ""
        if cmd == "fail":
            print("ERR:Asset:mock 资产缺失", file=sys.stderr)
            return 2, "", "ERR:Asset:mock 资产缺失\n"
        raise CapFailure("Unsupported", "mock 未实现子命令 %s" % cmd)


class _SelftestClient(object):
    """自测客户端：与 Go 侧 capsvc_protocol_test.go 同一帧口径（大端 uint32 + JSON）。"""

    def __init__(self, path):
        self.path = path

    def conn(self, timeout=5.0):
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.settimeout(timeout)
        s.connect(self.path)
        return s

    @staticmethod
    def send(s, obj):
        body = json.dumps(obj, ensure_ascii=False).encode("utf-8") if not isinstance(obj, bytes) else obj
        s.sendall(FRAME_HEADER.pack(len(body)) + body)

    @staticmethod
    def recv(s):
        hdr = b""
        while len(hdr) < FRAME_HEADER.size:
            b = s.recv(FRAME_HEADER.size - len(hdr))
            if not b:
                raise EOFError("连接被对端关闭")
            hdr += b
        (n,) = FRAME_HEADER.unpack(hdr)
        body = b""
        while len(body) < n:
            b = s.recv(n - len(body))
            if not b:
                raise EOFError("连接被对端关闭（body 半截）")
            body += b
        return json.loads(body.decode("utf-8"))

    def call(self, cap, cmd, args=None, timeout_ms=5000):
        s = self.conn()
        try:
            self.send(s, {"id": "t-%d" % random.randint(1000, 9999), "cap": cap, "cmd": cmd,
                          "args": args or [], "stdin_b64": "", "timeout_ms": timeout_ms})
            return self.recv(s)
        finally:
            s.close()


def run_selftest():
    failures = []
    checks = []

    def check(name, cond, detail=""):
        checks.append(name)
        if not cond:
            failures.append("%s %s" % (name, detail))
            print("FAIL %s %s" % (name, detail), file=sys.stderr)
        else:
            print("ok   %s" % name, file=sys.stderr)

    tmp = tempfile.mkdtemp(prefix="capsvc-selftest.")
    sock_path = os.path.join(tmp, "capsvc.sock")
    factories = {
        "mockA": lambda srv, name: _MockCap(srv, name),
        "mockB": lambda srv, name: _MockCap(srv, name),
        # T6 用一份**从没加载过**的能力：T1 已经把 mockA 装载了，拿它数 cold 会恒 0（自伤）。
        "mockC": lambda srv, name: _MockCap(srv, name),
    }
    # 预算 100MB：mockA(120) 加载后即超，加载 mockB 时必须把 mockA 卸掉。
    srv = Server(sock_path, factories, selftest=True, max_rss_mb=100, idle_unload_sec=0)
    th = threading.Thread(target=srv.start, daemon=True)
    th.start()
    deadline = time.time() + 5
    while not os.path.exists(sock_path) and time.time() < deadline:
        time.sleep(0.02)
    if not os.path.exists(sock_path):
        print("FAIL selftest 服务没能起来（socket 未出现）", file=sys.stderr)
        return 1

    cli = _SelftestClient(sock_path)
    try:
        # T1 往返：id 回显、UTF-8 参数与 stdin 原样到脚本侧
        r = cli.call("mockA", "echo", ["中文", "x"])
        echo = json.loads(r.get("stdout") or "{}")
        check("T1 roundtrip", r.get("ok") is True and echo.get("echo") == ["中文", "x"],
              "resp=%r" % (r,))

        # T2 坏 JSON → BadRequest（不是断开、不是 500 形态）
        s = cli.conn()
        try:
            cli.send(s, b"{not json")
            r = cli.recv(s)
        finally:
            s.close()
        check("T2 bad_json", r.get("variant") == "BadRequest", "resp=%r" % (r,))

        # T3 未知能力 → Unsupported（二期 asr/tts 也走这一档）
        r = cli.call("nope", "echo")
        check("T3 unknown_cap", r.get("variant") == "Unsupported", "resp=%r" % (r,))

        # T4 超长帧 → 直接断开 + oversize 计数
        before = srv.counters["oversize"]
        s = cli.conn()
        try:
            s.sendall(FRAME_HEADER.pack(FRAME_MAX + 1))
            closed = False
            try:
                if s.recv(1) == b"":
                    closed = True
            except (ConnectionError, socket.timeout, OSError):
                closed = True
        finally:
            s.close()
        time.sleep(0.05)
        check("T4 oversize", closed and srv.counters["oversize"] == before + 1,
              "closed=%s oversize=%d" % (closed, srv.counters["oversize"]))

        # T5 半包：发 2 字节就断，服务不许 panic，下一条连接照常
        s = cli.conn()
        try:
            s.sendall(b"\x00\x00")
        finally:
            s.close()
        time.sleep(0.05)
        r = cli.call("mockA", "echo", ["after-half"])
        check("T5 half_packet", r.get("ok") is True, "resp=%r" % (r,))

        # T6 cold=1 恰好一次（连打三次）——A6 的进程内形态
        colds = [cli.call("mockC", "echo", ["c%d" % i]).get("cold") for i in range(3)]
        check("T6 cold_once", colds == [1, 0, 0], "colds=%r" % (colds,))
        check("T6 model_loads", srv.counters["model_loads"] >= 1 and srv.counters["cold_count"] >= 1,
              "loads=%d cold=%d" % (srv.counters["model_loads"], srv.counters["cold_count"]))

        # T7 内存预算：mockB 加载前必须按 LRU 卸掉 mockA（同一份 selftest 进程内真跑）
        r = cli.call("mockB", "echo", ["b"])
        loaded = {k: v["loaded"] for k, v in json.loads(_svc_status(srv)[1])["caps"].items()}
        check("T7 budget_lru", r.get("ok") is True and loaded.get("mockA") is False
              and loaded.get("mockB") is True and srv.counters["evicted"] >= 1,
              "resp=%r loaded=%r evicted=%d" % (r, loaded, srv.counters["evicted"]))

        # T8 服务端超时只回 Timeout、进程仍在（下一条请求照常）
        t0 = time.time()
        r = cli.call("mockA", "sleep", ["1.5"], timeout_ms=200)
        spent = time.time() - t0
        r2 = cli.call("mockA", "echo", ["alive"])
        check("T8 timeout_alive", r.get("variant") == "Timeout" and spent < 1.2 and r2.get("ok") is True,
              "resp=%r spent=%.2f next=%r" % (r, spent, r2))

        # T9 状态面不出现路径（本机 socket 路径就在 tmpdir 里，泄漏了必然被抓）
        status = json.loads(_svc_status(srv)[1])
        blob = json.dumps(status, ensure_ascii=False)
        check("T9 status_no_path", sock_path not in blob and "/tmp" not in blob and "/run" not in blob,
              "status=%s" % blob)
    finally:
        srv.stop()
        try:
            os.unlink(sock_path)
        except OSError:
            pass
        try:
            os.rmdir(tmp)
        except OSError:
            pass

    if failures:
        print("CAPSVC_SELFTEST_FAIL=%d" % len(failures), file=sys.stderr)
        return 1
    print(SELFTEST_OK)
    return 0


# ---------------------------------------------------------------- 入口
def build_default_factories():
    """生产注册表：ocr 首批；asr/tts 二期留位（值为 None 的工厂＝已知但未接入）。"""
    return {
        "ocr": lambda srv, name: OcrCapability(srv, name),
        # 二期：asr / tts。两者协议与 ocr 完全同构（同一个 ScriptCapability 基类、
        # 同一个 ERR:<Variant> 档位），接入时只需在这里换成真工厂 + 在
        # internal/fileproc/warm_caps.go 的注册表加一条；**不许**另写一套 socket/帧。
        "asr": None,
        "tts": None,
    }


def main(argv=None):
    ap = argparse.ArgumentParser(description="LangCross 能力常驻服务（capsvc）")
    ap.add_argument("--socket", default=os.environ.get("CAPSVC_SOCKET") or DEFAULT_SOCKET)
    ap.add_argument("--selftest", action="store_true", help="跑自测（纯标准库，不需要权重）")
    args = ap.parse_args(argv)

    if args.selftest:
        return run_selftest()

    factories = build_default_factories()
    try:
        startup_check(factories)
    except CapFailure as f:
        # 启动自检失败：非 0 退出、**不拉起**。Go 侧 socket 不存在 ⇒ 回落子进程，用户无感。
        print("[capsvc] startup_check_failed variant=%s %s" % (f.variant, f.message), file=sys.stderr)
        return 2

    srv = Server(args.socket, factories)
    try:
        srv.start()
    except KeyboardInterrupt:
        return 0
    except OSError as e:
        print("[capsvc] bind_failed: %s" % e, file=sys.stderr)
        return 3
    return 0


if __name__ == "__main__":
    sys.exit(main())
