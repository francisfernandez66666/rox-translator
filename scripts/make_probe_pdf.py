#!/usr/bin/env python3
# ============ make_probe_pdf.py · 职责说明 ============
# 给现网派发探针（cmd/fpdprobe）造一份**够档**的测试件：
# 体积 ≥ FILEPROC_DISPATCH_MIN_MB（20MiB）且页数 ≥ FILEPROC_DISPATCH_MIN_PAGES（30），
# 同时**文本量刻意压到最小**——每页只有一句 20 来字的中文，恒等映射下不经模型。
#
# 为什么要自己造件：现网现存 PDF 里没有一份同时过这两条腿（实测 ≥3MB 且 ≥10 页的候选 0 个，
# 唯一 >20MB 的那份是 5 页 0 字符的纯图扫描件，页数与文本两条都不合）。
# 拿合成件跑的是**传输与转换两条腿**，不是模型，所以"文本少"不影响这一腿的效力。
#
# 用法：/opt/translator/.venv/bin/python3 make_probe_pdf.py <输出路径> [页数]
# 出参：stdout 一行读数（pages / size / chars），供留证与档位对照。
import hashlib
import os
import sys

import pymupdf

pages = int(sys.argv[2]) if len(sys.argv) > 2 else 32
out = sys.argv[1]

W, H = 595, 842  # A4
doc = pymupdf.open()  # 空文档：这版 pymupdf(1.28.2) 没有 new()，open() 无参即建空白件
total_chars = 0
for i in range(pages):
    page = doc.new_page(width=W, height=H)
    # ① 真实可提取文本（每页唯一一句，提取键必然逐页不同 ⇒ 32 个键）
    sent = "派发探针第%02d页：本句用于验证远端提取与写回两条腿是否同源。" % (i + 1)
    tw = pymupdf.TextWriter(page.rect)
    tw.append((56, 90), sent, font=pymupdf.Font("china-s"), fontsize=15)
    tw.write_text(page)  # 这版 pymupdf 的落页方法是 write_text（没有 update_page）
    total_chars += len(sent)
    # ② 噪声图把体积抬到档位：随机字节 Flate 压缩后几乎不缩水，每页约 0.7MB
    w, h = 640, 364
    samples = bytes(os.urandom(w * h * 3))
    pix = pymupdf.Pixmap(pymupdf.csRGB, w, h, samples, False)
    page.insert_image(pymupdf.Rect(56, 140, 56 + w * 0.75, 140 + h * 0.75), pixmap=pix)
doc.save(out, garbage=3, deflate=True)
size = os.path.getsize(out)
chk = pymupdf.open(out)
# 输入件自己的 sha256 一并打出：探针那侧算的是同一口径，两个数对上才说明"跑的就是这份件"。
h = hashlib.sha256()
with open(out, "rb") as f:
    for chunk in iter(lambda: f.read(1 << 20), b""):
        h.update(chunk)
print("@@PDF_MADE path=%s pages=%d size=%d chars=%d sha256=%s" % (
    out, chk.page_count, size, total_chars, h.hexdigest()))
chk.close()
