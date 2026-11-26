#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""chunk_translate.py — 工单**块级翻译**远端的协议胶水（2026-11-26，方案 §四·C-1）。

三条边界（改这个文件前必读）：
  1. **只做胶水**：切块/落文件/调 Go worker/打 zip；**不得含任何翻译判据或质量规则**。
     七条质量链在同源 Go worker + 主站拼回阶段，Python 重写一份必然静默降级。
  2. **Key 不落盘**：主站把当前生效 Key 随 payload stdin 临时传入，本脚本读完即
     以子进程 env 传给 worker（进程内），不写任何文件、不进 argv。
  3. **TMPDIR 隔离**：启动即把临时目录钉到会话目录的 tmp/，禁用系统 /tmp（§3.8）。

产物路径按会话约定固定（会话目录 = 输入 PDF 的父目录，即 w/<sid>/）：
  extract <in.pdf>     → 写 w/<sid>/texts.json
  translate <in.pdf>   → 读 texts.json，按 payload.bounds 翻译，写 w/<sid>/result.zip
  assemble <out.pdf>   → 超限接手：调 worker assemble 写回成品

payload（stdin JSON）：
  {"key","base","model","target_lang","bounds":[[lo,hi],...],"terms":{...}}
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import subprocess
import sys
import tempfile
import zipfile
from pathlib import Path

ROOT = Path(os.environ.get("FPD_ROOT", "/opt/fpdispatch"))
BIN = ROOT / "bin"
WORK = ROOT / "w"

EXIT_OK = 0
EXIT_REFUSED = 30


def _set_tmpdir(session_dir: Path):
    """把 TMPDIR 钉到会话目录（§3.8 双隔离：不写系统 /tmp）。"""
    d = session_dir / "tmp"
    d.mkdir(parents=True, exist_ok=True)
    os.environ["TMPDIR"] = str(d)
    tempfile.tempdir = str(d)


def _read_payload() -> dict:
    raw = sys.stdin.buffer.read()
    if not raw:
        return {}
    try:
        return json.loads(raw.decode("utf-8"))
    except Exception as e:
        print("CHUNK_PAYLOAD_BAD reason=%s" % type(e).__name__, file=sys.stderr)
        return {}


def _worker_env(payload: dict) -> dict:
    env = dict(os.environ)
    if payload.get("key"):
        env["CHUNKWORKER_LLM_KEY"] = payload["key"]
    return env


def _run_worker(payload: dict, timeout: int) -> subprocess.CompletedProcess:
    worker = BIN / "chunkworker"
    if not worker.exists() or not os.access(str(worker), os.X_OK):
        print("CHUNK_WORKER_MISSING", file=sys.stderr)
        raise RuntimeError("worker_missing")
    return subprocess.run(
        [str(worker)], input=json.dumps(payload, ensure_ascii=False).encode("utf-8"),
        capture_output=True, env=_worker_env(payload), timeout=timeout,
    )


def cmd_extract(in_pdf: str) -> int:
    """pymupdf 提取文本段 → texts.json（不走 stdout，避开 4MiB 吞尾）。"""
    try:
        import pymupdf
    except ImportError:
        print("CHUNK_EXTRACT ok=0 reason=pymupdf_missing", file=sys.stderr)
        return EXIT_REFUSED
    src = Path(in_pdf)
    if not src.exists():
        print("CHUNK_EXTRACT ok=0 reason=input_missing", file=sys.stderr)
        return EXIT_REFUSED
    session = src.parent
    _set_tmpdir(session)
    try:
        doc = pymupdf.open(str(src))
        texts = []
        for page in doc:
            t = page.get_text("text")
            if t and t.strip():
                texts.append(t.strip())
        doc.close()
        out = session / "texts.json"
        out.write_text(json.dumps({"success": True, "texts": texts}, ensure_ascii=False),
                       encoding="utf-8")
        print("CHUNK_EXTRACT ok=1 texts=%d out=%s" % (len(texts), out.name))
        return EXIT_OK
    except Exception as e:
        print("CHUNK_EXTRACT ok=0 reason=%s" % type(e).__name__, file=sys.stderr)
        return EXIT_REFUSED


def cmd_translate(in_pdf: str) -> int:
    """按 payload.bounds 调 worker 翻译；块译文落 chunk_<n>.json；打 result.zip。"""
    payload = _read_payload()
    src = Path(in_pdf)
    session = src.parent
    _set_tmpdir(session)
    bounds = payload.get("bounds") or []
    if not bounds:
        print("CHUNK_TRANSLATE ok=0 reason=bounds_missing", file=sys.stderr)
        return EXIT_REFUSED
    texts_path = session / "texts.json"
    if not texts_path.exists():
        print("CHUNK_TRANSLATE ok=0 reason=texts_missing", file=sys.stderr)
        return EXIT_REFUSED
    try:
        texts = json.loads(texts_path.read_text(encoding="utf-8"))["texts"]
    except Exception:
        print("CHUNK_TRANSLATE ok=0 reason=texts_bad", file=sys.stderr)
        return EXIT_REFUSED

    # Key 只在进程内传给 worker（env），不落盘、不进 argv
    wpayload = dict(payload)
    wpayload["texts"] = texts
    timeout = int(os.environ.get("FPD_TIMEOUT_SEC", "1200"))
    try:
        proc = _run_worker(wpayload, timeout)
    except Exception as e:
        print("CHUNK_TRANSLATE ok=0 reason=%s" % e, file=sys.stderr)
        return EXIT_REFUSED
    if proc.returncode != 0:
        print("CHUNK_TRANSLATE ok=0 reason=worker_exit detail=%s" %
              proc.stderr.decode("utf-8", "replace")[-200:], file=sys.stderr)
        return EXIT_REFUSED
    try:
        result = json.loads(proc.stdout.decode("utf-8"))
    except Exception:
        print("CHUNK_TRANSLATE ok=0 reason=worker_output_bad", file=sys.stderr)
        return EXIT_REFUSED

    chunks = result.get("chunks") or []
    manifest = {
        "kind": "langcross_chunks",
        "bounds": bounds,
        "chunk_count": len(chunks),
        "chunks": [],
        "tokens_used": result.get("tokens_used", 0),
        "quality_hits": result.get("quality_hits", []),
    }
    for i, c in enumerate(chunks):
        name = "chunk_%d.json" % i
        body = json.dumps(c, ensure_ascii=False).encode("utf-8")
        (session / name).write_bytes(body)
        manifest["chunks"].append({"name": name, "sha256": hashlib.sha256(body).hexdigest()})
    (session / "manifest.json").write_text(json.dumps(manifest, ensure_ascii=False), encoding="utf-8")

    zpath = session / "result.zip"
    with zipfile.ZipFile(str(zpath), "w", zipfile.ZIP_DEFLATED) as z:
        z.write(str(session / "manifest.json"), "manifest.json")
        for c in manifest["chunks"]:
            z.write(str(session / c["name"]), c["name"])
        z.write(str(texts_path), "texts.json")
    print("CHUNK_TRANSLATE ok=1 chunks=%d tokens=%d zip=%d" %
          (len(chunks), manifest["tokens_used"], zpath.stat().st_size))
    return EXIT_OK


def cmd_assemble(out_pdf: str) -> int:
    """超限接手：调 worker assemble 在广州本地写回成品（仅派发单）。"""
    payload = _read_payload()
    payload["op"] = "assemble"
    payload["out_pdf"] = out_pdf
    try:
        proc = _run_worker(payload, int(os.environ.get("FPD_TIMEOUT_SEC", "1200")))
    except Exception as e:
        print("CHUNK_ASSEMBLE ok=0 reason=%s" % e, file=sys.stderr)
        return EXIT_REFUSED
    if proc.returncode != 0:
        print("CHUNK_ASSEMBLE ok=0 reason=worker_exit", file=sys.stderr)
        return EXIT_REFUSED
    sys.stdout.buffer.write(proc.stdout)
    return EXIT_OK


def main():
    ap = argparse.ArgumentParser(description="langcross chunk translate glue (no translation rules)")
    sub = ap.add_subparsers(dest="op", required=True)
    e = sub.add_parser("extract"); e.add_argument("in_pdf")
    t = sub.add_parser("translate"); t.add_argument("in_pdf")
    a = sub.add_parser("assemble"); a.add_argument("out_pdf")
    args = ap.parse_args()
    if args.op == "extract":
        return cmd_extract(args.in_pdf)
    if args.op == "translate":
        return cmd_translate(args.in_pdf)
    return cmd_assemble(args.out_pdf)


if __name__ == "__main__":
    sys.exit(main())
