#!/usr/bin/env python3
"""扫描 Go 文件中缺 doc 注释的顶层声明（func/type/var/const 块）。
用法: python3 missing_comments.py [--detail] <file.go>...
输出: 每文件一行 `<missing_count> <path>`，仅列出有缺失的文件；--detail 时输出缺失行号+声明行。

★ 判据口径（2026-09-28 〇-X 收尾批补，别把这里的豁免当漏洞）：
  本脚本是**逐行文本扫描**，不认识 Go 的词法，所以内嵌 HTML/JS 原始字符串（backtick）里
  以列 0 开头的 JS 声明会被当成 Go 顶层声明——office.go 的 taskpane 脚本 `var cfgKey=...`
  就是这一形态。给这类点位补注释＝改后端直出面字节、必须换 translator-server
  （AGENTS.md §一·5「五类渲染面」口径），收益为零、风险是发版面扩大，故判为**射程外**，
  用下面的 RAW_STRING_EXEMPT 显式登记，而不是让闸门恒久挂着 1 处噪声。
  ⚠ 为什么不改成「自动跳过原始字符串」：本仓大量中文注释里带反引号（`xxx.go` 这种写法），
     按反引号奇偶推断字符串态会把注释行误判为进入原始串，进而**吞掉后面真缺注释的声明**——
     那是比 1 处假阳更坏的恒空假绿。豁免表是显式的、可 grep 的、加一条就少一条的。
  ⚠ 新增豁免必须同时给「剥掉一条真注释立刻报缺」的反证（见文件末尾的自检说明），
     禁止用豁免表把真缺口一并抹平。
"""
import re, sys

detail = "--detail" in sys.argv
files = [a for a in sys.argv[1:] if not a.startswith("--")]
decl = re.compile(r"^(func|type|var|const)\b")

# 射程外登记：(文件路径后缀, 声明行去空白后的前缀)。只登记「内嵌原始字符串里的 JS 声明」。
RAW_STRING_EXEMPT = [
    # 后端直出 /office/taskpane.html 的内嵌 JS（office.go 的 backtick 模板串内部）
    ("internal/api/office.go", "var cfgKey="),
]


def exempted(path: str, line: str) -> bool:
    s = line.lstrip()
    return any(path.endswith(p) and s.startswith(k) for p, k in RAW_STRING_EXEMPT)


out = []
for path in files:
    try:
        lines = open(path, encoding="utf-8").read().splitlines()
    except Exception:
        continue
    missing = []
    for i, ln in enumerate(lines):
        if decl.match(ln) and not exempted(path, ln):
            j = i - 1
            while j >= 0 and lines[j].strip() == "":
                j -= 1
            # 认三种「上面就是注释」的形态：// 行注释、/* 起头的块注释、
            # 以及块注释的收尾行（缩进正文型 /* ... */ 的最后一行既不以 /* 也不以 * 开头，
            # 旧判据会把它当代码，把已经写了块注释的声明误报成缺注释）
            prev = lines[j].lstrip() if j >= 0 else ""
            documented = prev.startswith("//") or prev.startswith("/*") or prev.startswith("*") or prev.rstrip().endswith("*/")
            if j < 0 or not documented:
                missing.append(i + 1)
    if missing:
        out.append((len(missing), path))
        if detail:
            for n in missing:
                print(f"  {path}:{n}: {lines[n-1].rstrip()[:90]}")
for n, p in sorted(out, reverse=True):
    print(f"{n:4d} {p}")
print(f"TOTAL {sum(n for n, _ in out)} declarations in {len(out)} files")

# —— 反证口径（每次改动判据或新增豁免后必须实跑一遍，三条都要成立）——
#  ① 豁免只吃掉登记的那一行：
#       python3 scripts/missing_comments.py --detail backend-go/internal/api/office.go
#     期望 TOTAL 0（office.go 里唯一的 Go 顶层声明都有注释，被豁免的只有内嵌 JS 那一行）。
#  ② 真缺口照样报：把任一 Go 文件复制到 /tmp 后**剥掉某个顶层声明上方的注释行**，
#     对副本跑本脚本必须报 1（报 0 即说明判据被抹平，禁止合入）。
#  ③ 喂目录＝0 文件＝恒空假绿（历史踩过）：必须用 find … -print0 | xargs -0 传**实文件列表**，
#     并核对输出里的文件数与 TOTAL 同行，不能只看退出码（本脚本恒 exit 0）。
