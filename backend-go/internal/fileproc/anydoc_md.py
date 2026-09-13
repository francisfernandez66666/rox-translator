#!/usr/bin/env python3
"""anydoc_md.py — firecrawl/anydoc（Rust 核心）任意文档→Markdown 子命令壳。

子命令：
  convert <path>   转换文档为 GFM Markdown 写 stdout；
                   失败时 stderr 单行 "ERR:<Variant>:<原因>" 退出码 2
  detect <path>    按文件内容魔数探测格式，stdout 输出格式名（探测不出输出空行）

依赖：pip install firecrawl-anydoc（导入名 anydoc）。
安全边界：anydoc 核心纯本地转换、零网络调用；hosted OCR（ocr="hosted"）会把文档
发送到 Firecrawl 商业云，私有化部署一律不启用——本脚本刻意不传 ocr 参数。
"""
import sys


def main() -> int:
    if len(sys.argv) < 3:
        print("ERR:Usage:convert|detect <path>", file=sys.stderr)
        return 2
    cmd, path = sys.argv[1], sys.argv[2]
    try:
        import anydoc
    except ImportError:
        print("ERR:Import:anydoc 库未安装（pip install firecrawl-anydoc）", file=sys.stderr)
        return 2
    try:
        if cmd == "detect":
            with open(path, "rb") as f:
                print(anydoc.format_from_bytes(f.read()) or "")
            return 0
        md = anydoc.to_markdown(path)
    except anydoc.ConvertError as e:
        print("ERR:%s:%s" % (type(e).__name__, str(e).replace("\n", " ")[:200]), file=sys.stderr)
        return 2
    except OSError as e:
        print("ERR:Io:%s" % str(e).replace("\n", " ")[:200], file=sys.stderr)
        return 2
    sys.stdout.write(md)
    return 0


if __name__ == "__main__":
    sys.exit(main())
