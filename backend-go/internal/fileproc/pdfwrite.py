#!/usr/bin/env python3
"""pdfwrite.py — 用 fpdf2 生成翻译结果 PDF（支持 CJK 字体嵌入）"""
import sys, json, os
from fpdf import FPDF

def main():
    if len(sys.argv) < 3:
        print("Usage: pdfwrite.py <out_path> <font_path>", file=sys.stderr)
        sys.exit(1)
    out_path = sys.argv[1]
    font_path = sys.argv[2]

    # 从 stdin 读 JSON: {"srcTexts": [...], "translations": {...}}
    raw = sys.stdin.read()
    try:
        data = json.loads(raw)
    except json.JSONDecodeError as e:
        print(f"JSON error: {e}", file=sys.stderr)
        sys.exit(1)

    src_texts = data.get("srcTexts", [])
    translations = data.get("translations", {})

    pdf = FPDF(orientation="P", unit="mm", format="A4")
    pdf.set_auto_page_break(auto=True, margin=20)
    pdf.add_font("cjk", "", font_path)
    pdf.add_page()

    title_size = 15
    body_size = 10.5
    line_h = 6.2

    first = True
    for src in src_texts:
        src = src.strip()
        if not src:
            continue
        text = translations.get(src, src)

        if first and len(src) <= 60:
            pdf.set_font("cjk", "", title_size)
            pdf.multi_cell(0, title_size * 0.62, text)
            pdf.ln(3.5)
            first = False
            continue

        first = False
        pdf.set_font("cjk", "", body_size)
        pdf.multi_cell(0, line_h, text)
        pdf.ln(1.6)

    pdf.output(out_path)
    print(f"OK: {out_path}")

if __name__ == "__main__":
    main()