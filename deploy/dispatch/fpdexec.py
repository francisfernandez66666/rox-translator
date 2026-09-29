#!/usr/bin/env python3
# ============ fpdexec.py · 职责说明 ============
# 体验机（文档转换派发远端，dispatch worker）侧**唯一**的执行入口。
# 主站通过 `ssh fpd@host` 拉起本文件，本文件再以子进程方式拉起真正的转换脚本
# （pdf_overlay.py / pdfwrite.py …），并把 stdout/stderr/退出码原样透传给主站。
#
# 设计红线（改动前必读，一条都不许破）：
#   ① 零密钥：本进程**不读**任何生产密钥、不连数据库、不访问主站任何端口。
#      需要的东西全在 stdin 里（脚本参数 + 译文映射 payload）。
#   ② 只碰工作目录：run 模式下 argv 里出现的每一个文件路径、put/get/stat 的每一个 rel
#      都必须落在 FPD_ROOT/w/ 下，否则直接拒绝——这拦住两类事故：主站路径写错被远端当真去读（读自己的系统文件），
#      以及映射层漏改导致产物写到工作目录外（清不掉、也永远回不到主站）。
#   ③ 到期即拒：过了 FPD_EXPIRE_DATE 一律拒绝服务（并落一个 EXPIRED 标记），
#      这样"厂商忘了回收机器"和"主站回归 timer 没跑成功"两种失败都不会泄漏客户文件。
#   ④ 脚本白名单：只允许跑 fileproc 的那几个 .py，不接受任意可执行文件路径。
#
# 协议（与主站 fileproc_remote.go 一对一，别自创）：
#   stdin 第一行 = base64(JSON header)，其余字节 = payload。
#   header: {"mode":"probe"|"selftest"|"sweep"|"run"|"put"|"stat"|"get",
#            "script":"pdf_overlay.py", "argv":[...], "rel":"<会话目录/文件名>",
#            "timeout_sec":615, "ttl_sec":3600}
#   四个模式各自的字节归属（★ 这是本协议最容易写错的一层）：
#     run  —— payload 进子进程 stdin；stdout 全归被透传的脚本；
#     put  —— payload 就是**客户文件本体**，落到 w/<rel>，stdout 回一行 JSON 确认；
#     stat —— 无 payload，stdout 一行 JSON（size + sha256）；主站先 stat 再 get，
#             这样 get 的字节流不需要任何自创分隔符（"读到 EOF 为止"配合声明的长度才是稳的）；
#     get  —— 无 payload，stdout **只有文件原始字节**，一个字都不许多写（写进去就等于产物损坏）。
#   退出码：0 正常；1 执行失败；2 依赖/环境缺失（判不就绪）；3 脚本崩溃（断言失败等）；
#          78 配置错误（已到期／路径越界／白名单外）。
#   probe / selftest 的 stdout 是 JSON，主站按 JSON 解析；run 的 stdout 是**被透传的脚本输出**，
#   本文件在 run 模式下绝不往 stdout 写任何东西（写进去就污染了 Go 侧要解析的脚本 JSON）。
import base64
import datetime as _dt
import glob
import json
import os
import re
import shutil
import subprocess
import sys
import time
from pathlib import Path

# 允许被派发的脚本白名单（与主站 backend-go/internal/fileproc/*.py 同名同内容，由 rsync 送上来）。
ALLOWED_SCRIPTS = {
    "pdf_overlay.py",
    "pdfwrite.py",
    "anydoc_md.py",
    "docx_translate.py",
    "docx_table_selftest.py",
}

ROOT = Path(os.environ.get("FPD_ROOT", "/opt/fpdispatch"))
BIN = ROOT / "bin"
WORK = ROOT / "w"
PYBIN = os.environ.get("FPD_PYBIN", str(ROOT / ".venv" / "bin" / "python3"))
EXPIRE = os.environ.get("FPD_EXPIRE_DATE", "").strip()          # YYYY-MM-DD，空＝不设到期（不应出现在生产）
MARKER = ROOT / "EXPIRED"
def _env_int(name, fallback):
    """安全取整型环境变量：**空串必须回落到默认值**。
    systemd 的 `Environment=FPD_TTL_SEC=` 会给出空串，直接 int("") 会在远端抛栈——
    表现为"每次派发都失败且原因在 shim 自己身上"，很难往配置上想。"""
    raw = (os.environ.get(name, "") or "").strip()
    try:
        return int(raw) if raw else fallback
    except ValueError:
        return fallback


DEFAULT_TIMEOUT = _env_int("FPD_TIMEOUT_SEC", 615)   # 主站预算 + 15s 富余
DEFAULT_TTL = _env_int("FPD_TTL_SEC", 3600)
# probe 里那次 selftest 的墙钟上限（实测 ≈1.4 秒；给 60 秒只为"机器被别的东西压住时不要拖死探测"，
# 到点就判不就绪——绝不因为"还没跑完"而回 0）。
PROBE_SELFTEST_SEC = _env_int("FPD_PROBE_SELFTEST_SEC", 60)


def _now_date():
    """今天的日期串（UTC 口径：机器时区不影响到期判定，宁可早一天关闸）。"""
    return _dt.datetime.now(_dt.timezone.utc).strftime("%Y-%m-%d")


def _env_int_or(val, fallback):
    """header 里的数值字段容错：非数字/空 → 用默认值（别让主站一个笔误就打爆远端）。"""
    try:
        n = int(val)
        return n if n > 0 else fallback
    except (TypeError, ValueError):
        return fallback


def _expired():
    """是否已到／超过到期日。日期串非法时**按已到期处理**——配置坏了就该停，不该继续接客户文件。"""
    if not EXPIRE:
        return False
    try:
        _dt.date.fromisoformat(EXPIRE)
    except ValueError:
        return True
    return _now_date() >= EXPIRE


def _die(code, msg):
    """报错走 stderr（run 模式的 stdout 必须留给被透传的脚本），并以约定退出码结束。"""
    sys.stderr.write("[fpdexec] %s\n" % msg)
    sys.stderr.flush()
    sys.exit(code)


def _sha256(path):
    """文件内容 sha256（主站拿它做"脚本指纹等值"判据，所以必须按字节读、不许归一）。"""
    h = __import__("hashlib").sha256()
    try:
        with open(path, "rb") as f:
            for chunk in iter(lambda: f.read(1 << 20), b""):
                h.update(chunk)
    except OSError:
        return ""
    return h.hexdigest()


def _import_ok(mod):
    """探测某 Python 模块能否导入（比 pip list 可信：装在别的解释器里不算数）。

    ⚠️ 必须把"解释器本身不存在"也判为 False：venv 没建时 subprocess.run 抛的是
    FileNotFoundError，不接住的话 probe 会直接崩栈——主站拿不到 JSON，
    症状从"远端不就绪"变成"派发面探针异常"，排障方向整个被带偏。
    """
    try:
        r = subprocess.run([PYBIN, "-c", "import " + mod], stdout=subprocess.DEVNULL,
                           stderr=subprocess.DEVNULL, timeout=60)
    except Exception:
        return False
    return r.returncode == 0


def _module_version(mod_display, mod_import):
    """现读库版本（不写死常量——版本串一旦进 git 就成了"一升级就双红"的假绿源）。

    ★ 实测 bug（2026-09-29）：importlib.metadata.version() 吃的是**发行包名**，
    不是 import 名。旧代码把 mod_import（fpdf / docx / PIL / anydoc）喂进去，
    version() 抛 PackageNotFoundError → 恒返回空串，于是 probe 的 libs 里
    fpdf2/python_docx/pillow/firecrawl_anydoc 全是空，G2 浅判据永远判不就绪。
    这里改用 mod_display（fpdf2 / python-docx / Pillow / firecrawl-anydoc 发行包名）。
    _import_ok 仍用 mod_import（它做的是 `import <名>`，那一步才吃 import 名）。
    """
    code = ("import sys;"
            "from importlib.metadata import version;"
            "sys.stdout.write(version(%r))" % mod_display)
    try:
        r = subprocess.run([PYBIN, "-c", code], capture_output=True, text=True, timeout=20)
        return r.stdout.strip() if r.returncode == 0 else ""
    except Exception:
        return ""


def _zh_families():
    """系统里可用于中文的字体**族名集合**（排序去重）。

    主站与远端比的是"远端 ⊇ 主站"，不是比数量：多装几支无害，少一族就可能让译文变方框。
    ⚠️ 阿里普惠体在现网**不是 apt 包**，是随主站部署落在 /opt/translator/fonts/ 的单文件，
       所以它必须被 rsync 到远端的字体目录，否则这一族永远缺。
    """
    try:
        r = subprocess.run(["fc-list", ":lang=zh", "family"], capture_output=True,
                           text=True, timeout=30)
    except Exception:
        return []
    fams = set()
    for line in r.stdout.splitlines():
        # fc-list 行形如 "Noto Sans CJK SC:style=Regular"；一行可能挂多个 family（逗号分隔）
        head = line.split(":", 1)[0]
        for f in head.split(","):
            f = f.strip()
            if f:
                fams.add(f)
    return sorted(fams)


def _mem_total_gb():
    """物理内存 GB（主站据此现算远端并发档 D17：<3G→1 / 3-6G→2 / ≥6G→4，禁止写死默认值）。"""
    try:
        with open("/proc/meminfo") as f:
            for line in f:
                if line.startswith("MemTotal:"):
                    kb = int(re.sub(r"\D", "", line.split()[1]) or 0)
                    return round(kb / 1024.0 / 1024.0, 2)
    except Exception:
        pass
    return 0.0


def _pins():
    """主站随批送来的"权威尺子"：/etc/fpdispatch/pins.json（脚本 sha + 库版本 + 字体族）。

    本文件**只上报远端实测值，不在这里做等值判定**——等值与否由主站比，
    这样远端不需要知道主站的任何路径，也不需要保留一份会过期的常量。
    """
    return {}


def cmd_probe():
    """就绪探针：回一份 JSON，主站拿它和自身实测值做等值比较（§10 的 G1~G3 数据源）。"""
    scripts = {}
    for name in sorted(ALLOWED_SCRIPTS):
        p = BIN / name
        if p.exists():
            scripts[name] = _sha256(str(p))
    fb = BIN / "assets" / "fonts" / "DroidSansFallbackFull.ttf"
    out = {
        "ok": True,
        "expired": _expired(),
        "expire_date": EXPIRE,
        "today": _now_date(),
        "mem_gb": _mem_total_gb(),
        "cpu": (os.cpu_count() or 0),
        "python": sys.version.split()[0] if PYBIN == sys.executable else "",
        "pybin": os.path.basename(PYBIN),
        # venv 在不在要单独报：缺 venv 时上面所有 libs/imports 都会是空值，
        # 但"为什么空"必须一眼能看出来（是没装解释器，还是解释器缺库）
        "pybin_exists": os.path.exists(PYBIN) and os.access(PYBIN, os.X_OK),
        "python_ver": _py_version(),
        "libs": {
            "pymupdf": _module_version("PyMuPDF", "pymupdf"),
            "fpdf2": _module_version("fpdf2", "fpdf"),
            "pdf2docx": _module_version("pdf2docx", "pdf2docx"),
            "python_docx": _module_version("python-docx", "docx"),
            "pillow": _module_version("Pillow", "PIL"),
            "fonttools": _module_version("fonttools", "fontTools"),
            "firecrawl_anydoc": _module_version("firecrawl-anydoc", "anydoc"),
        },
        "imports": {
            "pymupdf": _import_ok("pymupdf"),
            "fpdf2": _import_ok("fpdf"),
            "anydoc": _import_ok("anydoc"),
        },
        "libreoffice": bool(shutil.which("libreoffice") or shutil.which("soffice")),
        "fonts": {
            "zh_families": _zh_families(),
            "fallback_ttf": fb.exists(),
            "fallback_ttf_sha": _sha256(str(fb)) if fb.exists() else "",
        },
        "script_sha": scripts,
        "work_writable": os.access(WORK, os.W_OK) if WORK.exists() else False,
        "disk_free_mb": _disk_free_mb(),
        # ★ 2026-09-29 补三条字段。三条都是同一形态：**判据写在 Go 侧、Python 侧从没吐过**，
        #   于是 json.Unmarshal 把缺失键留成零值，那道腿结构上恒通过（空转）。
        #   selftest —— 主站 fileproc_remote.go 的 DispatchProbe 里 `Selftest int` 一直在解这个键，
        #     就绪闸写的就是 `p.Selftest != 0` 判红；本函数此前从不吐该键 ⇒ "深判据"一次都没执行过。
        #     现在真跑一次 pdf_overlay.py selftest（实测 ≈1.4s / ≈89MB RSS；主站进程内 probe 只成功一次，
        #     成本落在启动期而非每一单），把**真实退出码**填进来。
        #   pymupdf —— 同族：Go 侧读的是**顶层** `pymupdf` 键，此前只在 libs 里嵌套 ⇒ 顶层永远为空。
        #   caps —— "内存帽到底吃没吃到"变成一条读数，见 _caps_report()。
        "selftest": _probe_selftest(),
        "pymupdf": _module_version("PyMuPDF", "pymupdf"),
        "caps": _caps_report(),
    }
    json.dump(out, sys.stdout, ensure_ascii=False)
    sys.stdout.write("\n")
    return 0


def _probe_selftest():
    """probe 内真跑一次 `pdf_overlay.py selftest`，回**真实退出码**（0＝原版式主链在这台机器上通）。

    ★ 为什么值得为它付这一次：主站那道就绪闸只有三条腿 `ok / expired / selftest`，
      前两条读的都是"配置里写了什么"，只有 selftest 是"这台机器真干得出这活"。
      浅判据（import pymupdf 成功）在主站已经放过一台转不出 PDF 的机器（改造方案 §12-D5）。
    ⚠️ 不许污染 stdout：主站把 probe 的 stdout 最后一行当 JSON 解，所以这里必须 capture_output；
      selftest 失败时的 tail 由 mode=selftest 单独取（别塞进 probe，JSON 会变长且难读）。
    返回码口径与 cmd_selftest 对齐：0 通过 / 2 脚本或资产缺失 / 3 断言失败或跑不动。
    """
    script = BIN / "pdf_overlay.py"
    if not script.exists():
        return 2
    try:
        r = subprocess.run([PYBIN, str(script), "selftest"],
                           capture_output=True, text=True, timeout=PROBE_SELFTEST_SEC)
        return int(r.returncode)
    except Exception:
        # 超时/起不来一律判不就绪——这里回 0 等于"探针瞎了就说病人健康"
        return 3


def _caps_report():
    """把"内存帽／墙钟帽到底吃没吃到"变成一条读数（而不是写在 README 里的设计意图）。

    ★ 为什么必须上报：ssh 拉起的会话**不落进 fpd.slice**（sshd 给每个连接开自己的 scope，
      非 root 也没法自选 slice），所以远端真正约束 PyMuPDF 的只有 cmd_run 里 preexec_fn 那一次
      setrlimit(RLIMIT_AS)——而它的值来自环境变量。09-29 实测 fpd-bridge 用 `. fpd.env` 加载却
      **没有 export**，三个 FPD_* 变量在 fpdexec 里全是空 ⇒ RLIMIT_AS_MB 读成 0 ⇒ 帽根本没戴上，
      且没有一条日志会喊。这个块就是"没人喊"那条的补位：报的是当前进程**真实读到**的值，
      读不到就是 0，配置断在哪一层一眼可见（bridge 没 export / env 文件没写 / 被系统硬上限压住）。
    """
    mb = _env_int("FPD_RLIMIT_AS_MB", 0)
    info = {
        "rlimit_as_mb": mb,
        "rlimit_as_set": mb > 0,
        "timeout_bin": bool(shutil.which("timeout")),
        "run_timeout_sec": DEFAULT_TIMEOUT,
        "ttl_sec": DEFAULT_TTL,
        "max_put_mb": MAX_PUT_MB,
    }
    try:
        import resource
        soft, hard = resource.getrlimit(resource.RLIMIT_AS)
        inf = resource.RLIM_INFINITY
        info["as_soft_mb"] = -1 if soft == inf else int(soft / 1024 / 1024)
        info["as_hard_mb"] = -1 if hard == inf else int(hard / 1024 / 1024)
        # 硬上限低于配置值 ⇒ setrlimit 会失败（cmd_run 里那次是吞掉的），帽实际吃不到配置的数
        info["clamped"] = bool(mb > 0 and hard != inf and hard < mb * 1024 * 1024)
    except Exception:
        info["as_hard_mb"] = 0
        info["clamped"] = False
    return info


def _py_version():
    """远端 venv 解释器版本串（主站与自己的 python3 -V 现读现比）。"""
    try:
        r = subprocess.run([PYBIN, "-c", "import platform;print(platform.python_version())"],
                           capture_output=True, text=True, timeout=20)
        return r.stdout.strip() if r.returncode == 0 else ""
    except Exception:
        return ""


def _disk_free_mb():
    """工作目录剩余空间 MB（40MB × 并发 × 语种数会吃磁盘，太小就该判不就绪）。"""
    try:
        st = os.statvfs(str(WORK if WORK.exists() else ROOT))
        return int(st.f_bavail * st.f_frsize / 1024 / 1024)
    except Exception:
        return -1


def cmd_selftest():
    """**深判据**：真跑一次 pdf_overlay.py selftest（合成表格 → extract → 原版式 apply）。

    ★ 为什么不许用"import pymupdf 成功"当就绪判据：现网的 PyMuPDF 只靠 pdf2docx 传递依赖存在，
      浅判据会放过"装了包却跑不了主链"的机器。而 selftest 会把整条链——含那支**随包兜底字体**
      （`pymupdf.Font(fontfile=FALLBACK_FONT)`）——真走一遍，缺资产当场暴露。
      （主站正是因为没这个资产，PDF 原版式写回 100% 静默失败，见改造方案 §12-D5。）
    退出码：0 通过 / 2 依赖缺失 / 3 selftest 断言失败。
    """
    script = BIN / "pdf_overlay.py"
    if not script.exists():
        _die(2, "pdf_overlay.py 未同步（先跑主站 scripts/dispatch_sync.sh）")
    r = subprocess.run([PYBIN, str(script), "selftest"], capture_output=True, text=True)
    ok = r.returncode == 0
    out = {
        "selftest_exit": r.returncode,
        "pymupdf_overlay_ok": ok,
        "tail": (r.stdout + "\n" + r.stderr).strip().splitlines()[-12:],
    }
    json.dump(out, sys.stdout, ensure_ascii=False)
    sys.stdout.write("\n")
    if not ok:
        return 2 if r.returncode == 2 else 3
    # fpdf2 降级链也验一次：它写的是"版式重建"那条腿，主站兜底全靠它
    if not _import_ok("fpdf"):
        return 2
    return 0


# 各脚本的**子命令词**（不是路径，不参与越界判定）：pdf_overlay/docx_translate/anydoc 的第一个
# 参数是动词，pdfwrite 的第一个参数直接就是输出路径——所以判据只能"含 / 的才当路径看"。
_SUBCMD_WORDS = {"extract", "apply", "selftest", "legacy", "convert", "detect"}


def _looks_like_path(tok):
    """判定一个 argv 元素是否该当路径看：含分隔符或绝对路径才算。

    为什么不能"逐个都当路径"：`apply` 这类子命令词、`en` 这类语种码都会被误判成越界，
    结果是**每一条合法派发都被自己的守卫判死**（自伤形态，本项目踩过多次）。
    """
    return tok.startswith("/") or ("/" in tok and not tok.startswith("-"))


def _within(tok, base):
    """tok 是否落在 base 目录之下（realpath 比较，挡 `..` 与软链绕路）。"""
    try:
        rp = os.path.realpath(tok)
        rb = os.path.realpath(str(base))
    except OSError:
        return False
    return rp == rb or rp.startswith(rb + os.sep)


def _check_paths(argv, outputs):
    """远端文件边界：读可以读 bin/（脚本与字体资产）与 w/，**写只能写 w/**。

    ★ 为什么读侧要放行 bin/：pdfwrite.py 的 argv[2] 是字体路径（在 bin/assets/fonts 下），
      一律只准 w/ 会把降级链也判死。
    ★ 为什么写侧必须锁死在 w/：这就是"产物必须回主站"的远端半边——远端写不到 w/ 之外，
      就不可能出现"库里记着别人机器上的路径"，也不可能顺手覆盖远端系统文件。
    """
    allowed_read = (WORK, BIN)
    for a in argv:
        if not a or a.startswith("-") or a in _SUBCMD_WORDS:
            continue
        if not _looks_like_path(a):
            continue            # 语种码、字号等裸词
        if not any(_within(a, b) for b in allowed_read):
            return "路径越界（只允许 %s 或 %s 之下）: %r" % (WORK, BIN, a)
    for o in (outputs or []):
        if not _within(o, WORK):
            return "产物必须落在工作目录 %s 之下: %r" % (WORK, o)
    return None


def cmd_run(hdr, payload):
    """执行一条转换命令：资源帽（timeout / nice / oom_score_adj）→ exec → 透传。"""
    if _expired():
        _mark_expired()
        _die(78, "已到期，拒绝执行")
    script_name = hdr.get("script") or ""
    if script_name not in ALLOWED_SCRIPTS:
        _die(78, "脚本不在白名单: %r" % script_name)
    script = BIN / script_name
    if not script.exists():
        _die(2, "脚本缺失: %s" % script_name)
    argv = hdr.get("argv") or []
    # argv[0] 约定是脚本名（与主站本地调用同形），从 argv[1:] 起做文件边界检查
    why = _check_paths(argv[1:], hdr.get("outputs") or [])
    if why:
        _die(78, why)
    timeout_sec = _env_int_or(hdr.get("timeout_sec"), DEFAULT_TIMEOUT)
    # `timeout` 缺失必须**明确报错**而不是悄悄不加帽跑：没有墙钟兜底的话，
    # 主站取消后远端会留下永久孤儿进程（§4.5 那条"最多多跑 预算+15s"的前提就是它）。
    tmo = shutil.which("timeout")
    if not tmo:
        _die(2, "缺 coreutils timeout ⇒ 无法加墙钟帽，拒绝执行（apt-get install coreutils）")
    cmd = [tmo, "-k", "10", str(timeout_sec), PYBIN, str(script)] + argv[1:]

    def _caps():
        """子进程侧资源帽：地址空间上限 + 低 OOM 优先级 + nice。

        ★ 为什么三样都要在**远端**加：主站那层 wrapNice 若只套在 `ssh` 命令上，
          受帽的是本地的 ssh 客户端进程，远端的 PyMuPDF 一点帽都没戴。
        ★ RLIMIT_AS 是这里真正兜住内存的一招：ssh 拉起的会话不会落进 fpd.slice
          （sshd 会话有自己的 scope），非 root 也没法自选系统 slice，
          所以"远端被打满也伤不到主站"这条是靠 setrlimit 实现的，slice 只管 timer 那两个服务。
        """
        try:
            with open("/proc/self/oom_score_adj", "w") as f:
                f.write("1000")   # 内存吃紧时先杀转换进程，别让它拖垮 sshd / 系统
        except Exception:
            pass
        try:
            os.nice(10)           # 批处理负载，永远让位给交互
        except Exception:
            pass
        mb = _env_int("FPD_RLIMIT_AS_MB", 0)
        if mb <= 0:
            # 帽读不到值＝配置在某一环断了（最典型是 fpd-bridge 没 export env，见该文件头）。
            # 这里**不拒绝执行**（派发是增益，别把能干活的机器判死），但必须在 stderr 喊一声，
            # 因为 probe 上报的 caps.rlimit_as_set=false 只有配合这条日志才定位得准。
            sys.stderr.write("[fpdexec] 警告：FPD_RLIMIT_AS_MB 读到 0 ⇒ 转换子进程没有内存帽"
                             "（核对 fpd-bridge 是否 export 了 etc/fpd.env）\n")
        try:
            import resource
            if mb > 0:
                lim = mb * 1024 * 1024
                resource.setrlimit(resource.RLIMIT_AS, (lim, lim))
        except Exception:
            # 设不上就照原样跑，但一定喊出来：probe 的 caps.rlimit_as_set / clamped 会把它点红
            sys.stderr.write("[fpdexec] 警告：setrlimit(RLIMIT_AS) 未生效 ⇒ 帽实际吃不到配置值\n")

    # 用临时文件承接 payload：直接 pipe 也行，但 PDF/译文映射可达数十 MB，
    # 落盘（就在 w/ 里，随会话一起清）比在管道里堆缓冲更稳，也便于超时后不留半截输入。
    stdin_data = payload if payload else None
    try:
        p = subprocess.Popen(cmd, stdin=subprocess.PIPE if stdin_data is not None else subprocess.DEVNULL,
                             stdout=sys.stdout.fileno(), stderr=sys.stderr.fileno(),
                             preexec_fn=_caps, cwd=str(WORK))
    except Exception as e:
        _die(1, "启动失败: %s" % e)
    try:
        if stdin_data is not None:
            p.stdin.write(stdin_data)
            p.stdin.close()
    except BrokenPipeError:
        pass  # 子进程已经退出（多半是脚本自身报错），交给 returncode 判定
    sys.stdout.flush()
    sys.exit(p.wait())


def _mark_expired():
    """落一条到期标记（不写内容，只留存在性给运维与 sweep 看）。"""
    try:
        MARKER.parent.mkdir(parents=True, exist_ok=True)
        MARKER.write_text("expired by fpdexec at %s\n" % _dt.datetime.now(_dt.timezone.utc).isoformat(timespec="seconds"))
    except OSError:
        pass


def cmd_sweep(hdr):
    """会话目录闲置回收 + 孤儿进程收拾，由 fpd-sweep.timer 定时调（也供人工排障）。"""
    ttl = _env_int_or(hdr.get("ttl_sec"), DEFAULT_TTL)
    removed, kept = [], []
    now = time.time()
    if WORK.exists():
        for d in sorted(glob.glob(str(WORK / "*"))):
            if not os.path.isdir(d):
                continue
            if now - os.path.getmtime(d) > ttl:
                shutil.rmtree(d, ignore_errors=True)
                removed.append(os.path.basename(d))
            else:
                kept.append(os.path.basename(d))
    if _expired():
        _mark_expired()
        for d in glob.glob(str(WORK / "*")):
            shutil.rmtree(d, ignore_errors=True) if os.path.isdir(d) else os.remove(d)
        removed.append("*ALL(expired)*")
    json.dump({"removed": removed, "kept": kept, "ttl_sec": ttl, "expired": _expired()},
              sys.stdout, ensure_ascii=False)
    sys.stdout.write("\n")
    return 0


def _resolve_rel(hdr):
    """把 header 里的 `rel` 解析成**必然位于 w/ 之下**的绝对路径。

    为什么单独立一个函数、而不是复用 _check_paths：
    run 模式的越界检查针对"argv 里出现的路径字符串"，而 put/get/stat 的路径是我自己拼的，
    风险点完全不同——`rel` 是外部可控字符串，必须防 `..` 上跳、防绝对路径、防目录本身是软链。
    任何一条漏掉，"远端只碰工作目录"这条红线就没了（最坏情况：主站密钥文件被 get 走）。
    """
    rel = (hdr.get("rel") or "").strip()
    if not rel:
        _die(78, "缺少 rel")
    if os.path.isabs(rel) or rel.startswith("~"):
        _die(78, "rel 必须是相对路径: %r" % rel)
    norm = os.path.normpath(rel)
    if norm.startswith("..") or os.path.isabs(norm):
        _die(78, "rel 越界（禁止 .. 上跳）: %r" % rel)
    p = os.path.join(str(WORK), norm)
    rp = os.path.realpath(p)
    if not _within(rp, WORK):
        _die(78, "rel 解析后不在工作目录下: %r -> %s" % (rel, rp))
    # 路径上任何一段是软链都拒绝：get 到 /etc/shadow 这类事只能靠"根本不存在软链"来保证
    cur = str(WORK)
    for seg in norm.split(os.sep):
        cur = os.path.join(cur, seg)
        if os.path.islink(cur):
            _die(78, "rel 路径上有软链，拒绝: %r" % rel)
    return p


# 单个客户文件体积上限（MB）。上传闸在主站是 40MB，这里留一倍余量只为防"主站闸失效"，
# 不是把它当第二个闸来用——真超了说明主站那侧漏了，宁可拒绝落盘也别把 50G 盘写满。
MAX_PUT_MB = _env_int("FPD_MAX_PUT_MB", 80)


def cmd_put(hdr, payload):
    """收下客户文件本体，落到 w/<rel>（原子：先 .part 再 replace）。"""
    if _expired():
        _mark_expired()
        _die(78, "已到期，拒绝接收文件")
    if not payload:
        _die(78, "put 模式 payload 为空")
    if len(payload) > MAX_PUT_MB * 1024 * 1024:
        _die(78, "payload %d 字节超过上限 %dMB" % (len(payload), MAX_PUT_MB))
    if _disk_free_mb() < (len(payload) // (1024 * 1024)) * 2 + 512:
        _die(2, "磁盘余量不足（剩 %dMB），拒绝落盘" % _disk_free_mb())
    p = _resolve_rel(hdr)
    os.makedirs(os.path.dirname(p), exist_ok=True)
    tmp = p + ".part"
    try:
        with open(tmp, "wb") as f:
            f.write(payload)
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, p)
    except OSError as e:
        try:
            os.remove(tmp)
        except OSError:
            pass
        _die(1, "落盘失败: %s" % e)
    size = os.path.getsize(p)
    if size != len(payload):
        _die(1, "落盘字节数与 payload 不符 %d != %d" % (size, len(payload)))
    json.dump({"ok": True, "path": p, "size": size, "sha256": _sha256(p)}, sys.stdout)
    sys.stdout.write("\n")
    return 0


def cmd_stat(hdr):
    """回一个 w/ 之下文件的大小与 sha256（主站据此决定 get 读多少字节、并校验取回是否完整）。"""
    p = _resolve_rel(hdr)
    if not os.path.isfile(p):
        _die(2, "文件不存在: %s" % p)
    json.dump({"ok": True, "path": p, "size": os.path.getsize(p), "sha256": _sha256(p)}, sys.stdout)
    sys.stdout.write("\n")
    return 0


def cmd_get(hdr):
    """把产物原样写到 stdout —— 这一条就是"结果必回主站"的落地腿。

    ★ stdout 只放文件字节：任何一句日志/换行都会让主站拿到的 PDF 变成坏文件。
      报错一律走 stderr + 非零退出码（与 run 模式同口径）。
    """
    p = _resolve_rel(hdr)
    if not os.path.isfile(p):
        _die(2, "文件不存在: %s" % p)
    try:
        with open(p, "rb") as f:
            while True:
                chunk = f.read(1 << 20)
                if not chunk:
                    break
                sys.stdout.buffer.write(chunk)
        sys.stdout.buffer.flush()
    except OSError as e:
        _die(1, "读取失败: %s" % e)
    return 0


def main():
    # 两种拉起方式（都要支持）：
    #   ① 主站经 ssh 调起：stdin 第一行是 base64(JSON header)——生产路径，别改协议；
    #   ② systemd timer / 人工排障：`fpdexec.py --mode sweep`，不必手搓 base64（搓错的代价是
    #      "timer 天天静默失败，没人发现清理从来没跑过"）。
    if len(sys.argv) > 1 and sys.argv[1] == "--mode":
        mode = sys.argv[2]
        hdr = {"mode": mode}
        payload = b""
        if mode == "probe":
            return cmd_probe()
        if mode == "selftest":
            return cmd_selftest()
        if mode == "sweep":
            return cmd_sweep(hdr)
        _die(78, "--mode 只支持 probe/selftest/sweep，收到 %r" % mode)
    # ★ stdin 必须**全程只用 sys.stdin.buffer**：混用文本层与 buffer 层是隐蔽的致命坑——
    #   `sys.stdin.readline()` 会按 8KB 预读把 header 之后的 payload 一并吸进 TextIOWrapper 内部缓冲，
    #   随后 `sys.stdin.buffer.read()` 只能拿到 0 字节。表现是"远端脚本正常退出、
    #   但译文映射是空的"⇒ 产物一个字都没换、工单却显示成功（正是最难查的那一类静默失败）。
    raw = sys.stdin.buffer
    head_line = raw.readline()
    if not head_line.strip():
        _die(78, "stdin 缺 header 行")
    try:
        hdr = json.loads(base64.b64decode(head_line.strip()).decode("utf-8"))
    except Exception as e:
        _die(78, "header 解析失败: %s" % e)
    # ★ payload **只在 run/put 读**：stat/get/probe/sweep 都没有 payload，
    #   无条件 `raw.read()` 会把它们变成"等主站关 stdin 才返回"——主站若忘了 shutdown(WR)
    #   就一路挂到墙钟超时，症状是"远端没报错但派发死活不返回"，比直接失败难定位得多。
    mode = hdr.get("mode", "probe")
    payload = raw.read() if mode in ("run", "put") else b""
    if mode == "probe":
        return cmd_probe()
    if mode == "selftest":
        return cmd_selftest()
    if mode == "sweep":
        return cmd_sweep(hdr)
    if mode == "run":
        return cmd_run(hdr, payload)   # 内部 sys.exit
    # 文件搬运三态：payload 的归属与 run 完全不同，别合并处理
    if mode == "put":
        return cmd_put(hdr, payload)
    if mode == "stat":
        return cmd_stat(hdr)
    if mode == "get":
        return cmd_get(hdr)
    _die(78, "未知 mode: %r" % mode)


if __name__ == "__main__":
    sys.exit(main())
