#!/usr/bin/env python3
"""扫描 TS/TSX 文件中缺 doc 注释的顶层声明（function/const/type/interface/export）。
用法: python3 missing_comments_ts.py <file.ts...>
输出: `<missing_count> <path>`（仅列出有缺失的文件），--detail 输出缺失行。
"""
import re, sys

detail = "--detail" in sys.argv
files = [a for a in sys.argv[1:] if not a.startswith("--")]
# 顶层声明：非缩进的 function/const/type/interface（排除 import）
decl = re.compile(r"^(export\s+)?(default\s+)?(async\s+)?(function|const|type|interface|class)\b")
skip = re.compile(r"^(import\b|export\s+\*|export\s+\{|export\s+default\s+\w+$|export\s+type\s+\{)")
out = []
for path in files:
    try:
        lines = open(path, encoding="utf-8").read().splitlines()
    except Exception:
        continue
    missing = []
    for i, ln in enumerate(lines):
        if decl.match(ln) and not skip.match(ln):
            # const 简单常量（全大写/短值）不算缺
            j = i - 1
            while j >= 0 and lines[j].strip() == "":
                j -= 1
            if j < 0 or not (lines[j].lstrip().startswith("//") or lines[j].lstrip().startswith("/*") or lines[j].lstrip().startswith("*")):
                # 忽略一眼即懂的字面常量
                m = re.match(r"^const\s+([A-Z_0-9]+)\s*=\s*[\d'\"]", ln)
                if m:
                    continue
                missing.append(i + 1)
    if missing:
        out.append((len(missing), path))
        if detail:
            for n in missing:
                print(f"  {path}:{n}: {lines[n-1].rstrip()[:90]}")
for n, p in sorted(out, reverse=True):
    print(f"{n:4d} {p}")
print(f"TOTAL {sum(n for n, _ in out)} declarations in {len(out)} files")
