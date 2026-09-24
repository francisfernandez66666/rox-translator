#!/usr/bin/env python3
"""check_doc_comments.py — 导出面中文文档注释棘轮门禁（★ 2026-09-24 立门）

口径（刻意保守，避免误报）：
- Go：`func Foo(` / `func (r R) Foo(` / `type Foo struct|interface`，且首字母大写（导出），
  要求**紧邻上一行**以 `//` 开头（Go doc comment 语义就是紧邻，空行即不算——与 go doc 行为一致）。
- 前端 TS/TSX：`export function|export async function|export default function|export const|
  export interface|export type|export class`，要求紧邻上一行以 `//`、`/*` 或 `*` 开头。
- 跳过生成物与第三方：*.d.ts、vendor/、node_modules/、dist/、*_test.go（测试注释由评审把关，不入棘轮）。

模式：
  (默认)               超基线则 exit 1（棘轮：只降不升）
  --list               打印缺口清单（文件 → 符号名），不设退出码（无缺口 exit 0）
  --update-baseline    把当前缺口数写入基线（仅当下降时才该用）
  --selftest           自证：注入探针必须被命中、有注释的必须被放行（防脚本写歪静默放行）

基线文件 .doc_comments_baseline（入库），形如 `go=3` / `fe=0`；缺失按 0 处理（最严）。
"""
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
BASELINE_FILE = ROOT / ".doc_comments_baseline"

GO_SKIP_DIRS = {"vendor", "node_modules", "dist", "testdata"}
FE_SKIP_DIRS = {"node_modules", "dist", "build", "coverage"}
GO_SKIP_FILE_PARTS = ("_test.go", ".pb.go", "_gen.go")
FE_SKIP_FILE_PARTS = (".d.ts", ".test.ts", ".test.tsx", ".spec.ts", ".spec.tsx")

GO_DECL = re.compile(r"^func\s+(?:\([^)]+\)\s+)?([A-Z]\w*)\s*\(|^type\s+([A-Z]\w*)\s+(?:struct|interface)\b")
FE_DECL = re.compile(
    r"^export\s+(?:default\s+)?(?:async\s+)?(function|const|interface|type|class)\s+([A-Za-z_$][\w$]*)"
)

def iter_files(sub: str, suffixes, skip_dirs, skip_parts):
    base = ROOT / sub
    if not base.exists():
        return
    for p in base.rglob("*"):
        if not p.is_file() or p.suffix not in suffixes:
            continue
        if any(d in p.parts for d in skip_dirs):
            continue
        if any(part in p.name for part in skip_parts):
            continue
        yield p

def scan_go():
    found = {}
    for p in iter_files("backend-go", {".go"}, GO_SKIP_DIRS, GO_SKIP_FILE_PARTS):
        rel = p.relative_to(ROOT).as_posix()
        lines = p.read_text(encoding="utf-8", errors="replace").splitlines()
        for i, line in enumerate(lines):
            m = GO_DECL.match(line)
            if not m:
                continue
            name = m.group(1) or m.group(2)
            prev = lines[i - 1].strip() if i > 0 else ""
            if not prev.startswith("//"):
                found.setdefault(rel, []).append(name)
    return found

def scan_fe():
    found = {}
    for sub in ("frontend-react/src", "extension/src"):
        for p in iter_files(sub, {".ts", ".tsx"}, FE_SKIP_DIRS, FE_SKIP_FILE_PARTS):
            rel = p.relative_to(ROOT).as_posix()
            lines = p.read_text(encoding="utf-8", errors="replace").splitlines()
            for i, line in enumerate(lines):
                m = FE_DECL.match(line)
                if not m:
                    continue
                name = m.group(2)
                prev = lines[i - 1].strip() if i > 0 else ""
                if not (prev.startswith("//") or prev.startswith("/*") or prev.startswith("*")):
                    found.setdefault(rel, []).append(name)
    return found

def load_baseline():
    base = {"go": 0, "fe": 0}
    if BASELINE_FILE.exists():
        for line in BASELINE_FILE.read_text().splitlines():
            line = line.strip()
            if "=" in line and line.split("=")[0] in base:
                k, v = line.split("=", 1)
                base[k] = int(v)
    return base

def selftest(tmp: Path):
    # 探针 1：无注释导出必须命中；探针 2：有注释导出必须放行；探针 3：紧邻上一行为空行必须命中
    probe = tmp / "probe.go"
    probe.write_text(
        "package probe\n\nfunc Bare() {}\n\n// 有注释\nfunc Documented() {}\n\nfunc (p Probe) BlankAbove() {}\n",
        encoding="utf-8",
    )
    found = scan_dir_go(probe)
    assert "Bare" in found, "selftest 失败：无注释探针未被命中（正则写歪？）"
    assert "BlankAbove" in found, "selftest 失败：上一行为空行应判缺失（Go doc 语义）"
    assert "Documented" not in found, "selftest 失败：有注释探针被误报"
    probe_ts = tmp / "probe.ts"
    probe_ts.write_text(
        "export function bareFn() {}\n// documented\nexport function docFn() {}\n",
        encoding="utf-8",
    )
    ts_found = scan_dir_fe(probe_ts)
    assert "bareFn" in ts_found, "selftest 失败：FE 无注释探针未被命中"
    assert "docFn" not in ts_found, "selftest 失败：FE 有注释探针被误报"

def scan_dir_go(p: Path):
    lines = p.read_text().splitlines()
    out = []
    for i, line in enumerate(lines):
        m = GO_DECL.match(line)
        if m and not (i > 0 and lines[i - 1].strip().startswith("//")):
            out.append(m.group(1) or m.group(2))
    return out

def scan_dir_fe(p: Path):
    lines = p.read_text().splitlines()
    out = []
    for i, line in enumerate(lines):
        m = FE_DECL.match(line)
        if m and not (i > 0 and lines[i - 1].strip().startswith(("//", "/*", "*"))):
            out.append(m.group(2))
    return out

def main():
    args = sys.argv[1:]
    import tempfile
    with tempfile.TemporaryDirectory() as td:  # 探针写临时目录，不在仓库留残骸
        selftest(Path(td))  # selftest 常开：脚本写歪直接炸，不给静默放行机会
    go, fe = scan_go(), scan_fe()
    if "--list" in args:
        for kind, found in (("go", go), ("fe", fe)):
            total = sum(len(v) for v in found.values())
            print(f"== {kind} 缺口 {total} 处，涉及 {len(found)} 文件 ==")
            for f in sorted(found):
                print(f"  {f}: {', '.join(found[f])}")
        return 0
    base = load_baseline()
    actual = {"go": sum(len(v) for v in go.values()), "fe": sum(len(v) for v in fe.values())}
    if "--update-baseline" in args:
        BASELINE_FILE.write_text(f"go={actual['go']}\nfe={actual['fe']}\n", encoding="utf-8")
        print(f"基线已更新：go={actual['go']} fe={actual['fe']}")
        return 0
    bad = False
    for k in ("go", "fe"):
        mark = "OK" if actual[k] <= base[k] else "FAIL"
        if actual[k] > base[k]:
            bad = True
        print(f"[{mark}] {k}: 实际 {actual[k]} / 基线 {base[k]}")
        if actual[k] > base[k]:
            found = go if k == "go" else fe
            for f in sorted(found):
                print(f"  {f}: {', '.join(found[f])}")
    return 1 if bad else 0

if __name__ == "__main__":
    sys.exit(main())
