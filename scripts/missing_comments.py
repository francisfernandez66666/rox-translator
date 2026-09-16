#!/usr/bin/env python3
"""扫描 Go 文件中缺 doc 注释的顶层声明（func/type/var/const 块）。
用法: python3 missing_comments.py <file.go>...
输出: 每文件一行 `<missing_count> <path>`，仅列出有缺失的文件；--detail 时输出缺失行号+声明行。
"""
import re, sys

detail = "--detail" in sys.argv
files = [a for a in sys.argv[1:] if not a.startswith("--")]
decl = re.compile(r"^(func|type|var|const)\b")
out = []
for path in files:
    try:
        lines = open(path, encoding="utf-8").read().splitlines()
    except Exception:
        continue
    missing = []
    for i, ln in enumerate(lines):
        if decl.match(ln):
            j = i - 1
            while j >= 0 and lines[j].strip() == "":
                j -= 1
            if j < 0 or not lines[j].lstrip().startswith("//"):
                missing.append(i + 1)
    if missing:
        out.append((len(missing), path))
        if detail:
            for n in missing:
                print(f"  {path}:{n}: {lines[n-1].rstrip()[:90]}")
for n, p in sorted(out, reverse=True):
    print(f"{n:4d} {p}")
print(f"TOTAL {sum(n for n, _ in out)} declarations in {len(out)} files")
