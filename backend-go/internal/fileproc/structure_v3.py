#!/usr/bin/env python3
"""structure_v3.py — 版面/表格结构识别子命令壳（S2 骨架）。

边界（主方案 §6 与《接入方案_结构保真》）：
  **只取结构，不取识别**。输出里**没有任何 text 字段**；文本仍然只听我们自己的 OCR。
  两条子命令：
    probe              自检 paddleocr 与 assets/structure 权重；OK: version=… models=…
    page <img>         单页版面/表格几何；stdout 一行 JSON

⚠️ 本批只交付**骨架**：真实推理要等 spike S0–S5 全绿（部署机上跑）。
   在那之前 page 一律按协议明确报 Unavailable——**不许静默返回空结构**
   （空结构会被上层当成"这页没版面"，那是把"没做完"伪装成"没问题"）。
"""
import importlib.util
import json
import os
import sys
from pathlib import Path

ASSETS_DIR = Path(__file__).resolve().parent / "assets" / "structure"
# 权重名清单以 spike S4 定稿为准；骨架阶段只要求"至少一个 .onnx"。
MODEL_GLOB = "*.onnx"


def _err(variant: str, msg: str) -> int:
    """按统一协议向 stderr 打 ERR:Structure:<variant>:<msg>（Go 侧解析成档位）。"""
    print("ERR:Structure:%s:%s" % (variant, msg), file=sys.stderr)
    return 2


def _assets():
    return sorted(ASSETS_DIR.glob(MODEL_GLOB))


def cmd_probe() -> int:
    """两态 + 版本读数：版本变了但 probe 仍 OK ⇒ 上层按"读数不可比"降级（见 Go 侧）。"""
    if importlib.util.find_spec("paddleocr") is None:
        print("MISSING: paddleocr 未安装（结构腿可选，缺失不影响 OCR/翻译）")
        return 3
    try:
        import paddleocr  # noqa: F401 —— 只取版本，不做推理
        ver = str(getattr(paddleocr, "__version__", "") or "-")
    except Exception as e:  # noqa: BLE001 —— 导入失败即"没就绪"，不是崩溃
        print("MISSING: paddleocr 导入失败: %s" % e)
        return 3
    models = _assets()
    if not models:
        print("MISSING: assets/structure/*.onnx 缺失（取权重见 assets/structure/README.md）")
        return 3
    print("OK: version=%s models=%d" % (ver, len(models)))
    return 0


def cmd_page(path: str) -> int:
    """单页结构。当前恒回 Unavailable（骨架），并明确点名缺哪一端。"""
    if not os.path.exists(path):
        return _err("BadInput", "文件不存在: %s" % path)
    if importlib.util.find_spec("paddleocr") is None:
        return _err("Unavailable", "paddleocr 未安装")
    if not _assets():
        return _err("Unavailable", "assets/structure 权重缺失")
    # 到这里说明依赖与权重都在，但本批尚未接入真实推理（spike S0–S5 未全绿）。
    # 回 Unavailable 而不是空结构：让上层走几何重建，并且**看得见**这一档。
    return _err("Unavailable", "结构腿尚未启用（spike S0–S5 未全绿；本批只交付骨架）")


def main() -> int:
    if len(sys.argv) < 2:
        print("ERR:Structure:Usage:probe|page <img>", file=sys.stderr)
        return 2
    cmd = sys.argv[1]
    if cmd == "probe":
        return cmd_probe()
    if cmd == "page":
        if len(sys.argv) < 3:
            return _err("Usage", "缺少图片路径")
        return cmd_page(sys.argv[2])
    return _err("Usage", "未知子命令: %s" % cmd)


if __name__ == "__main__":
    sys.exit(main())
