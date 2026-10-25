#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# ============================================================================
# scripts/uat/mock_llm.py — UAT 专用 mock LLM 服务
# 提供 OpenAI 兼容 /v1/chat/completions 与 /v1/embeddings：
#   - chat：按行保留行号前缀、内容换成**纯拉丁**假译文载荷（latin_payload：
#     `TranslatedEN item <md8>`；源文本就全 ASCII 时原样透传）
#     ★ 载荷里**不许嵌源文汉字**：引擎出栈链的 `PostProcessTranslation`（en 腿）本来就要剥掉
#     残留汉字，旧写法 `TranslatedEN(源文前 20 字)` 会被剥成 `TranslatedEN(，。)` 空壳，
#     再被 DEF-01 的指令回显判据判"不可用"⇒ 写入点不落键 ⇒ 文件链 0 产物（A16/T34/T39 红过两次，
#     第一次是标记契约、第二次是这里的载荷纯度）。前缀字面 `TranslatedEN` 保留：
#     A7/B2/B3/B8/T34/T47 那几条锁认的就是这个 token；
#     ★ 批量腿（engine.BatchTranslate）的 `<sN>…</sN>` 标记**逐字保留**、只换标记体内的正文，
#     否则 parseBatchOutput 解析出 0 条，文件链会"每段都没译出"（见下方 BATCH_MARK 注释）
#   - chat 的两个纯度触发器（UATPURITYREV / UATPURITYINIT，见下方★段）按「提示词是哪条腿」
#     定点回包，用来在真 HTTP 出口上复现㊶「审校腿把整句回译覆盖掉正确初翻」的现场
#   - embeddings：确定性伪向量（1024 维，L2 归一化），用于 KB 检索链路
# 用法：python3 mock_llm.py [port]   # 默认 8901
# ============================================================================
import json, re, hashlib, math, sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

NUM = re.compile(r'^(\s*)(\d+)([.、\)])\s*(.*)$')
# 批量腿的编号契约（engine.BatchTranslate 用 <sN>...</sN> 包裹每一条，见 engine.go 的 runChunk）。
# 桩必须**按同一条契约**回包：旧实现把整行塞进 TranslatedEN(...) 里，标记被吃掉 ⇒
# parseBatchOutput 解析出 0 条，文件链每一段都判"未译出"，产物 0 个文件而工单照回完成
# （A16/T34/T39 三条红的真身）。这是 §三 那条「假上游按我以为的契约实现＝给缺陷发证件」的又一形态：
# 桩错了不是产品错了，但桩错了会让**产品侧正确的降级链**看起来像缺陷。
BATCH_MARK = re.compile(r'^(\s*)<s(\d+)>(.*)</s\2>\s*$')

# ---------------------------------------------------------------------------
# ★ 0AR 第 8 波（㊶）· 译文脚本纯度触发器（UAT T70 段的耗材，别的段一个都不碰）
#
# 为什么要按「提示词是哪条腿」分派，而不是像 UATPSEUDO 那样只看源文：
#   ㊶ 的两条腿在真引擎里是**两个不同的出口**——
#     ② 审校腿产物脚本不纯 ⇒ 整份丢弃、保留初翻（engine.ReviewTranslation 返 ""）；
#     ④ 初翻腿尾段回译     ⇒ 只剥尾段（PostProcessTranslation → stripTrailingForeignResidual）。
#   源文里同一个标记会同时出现在初翻/审校/重翻三种提示词里，若不分腿，
#   「审校腿把初翻覆盖掉」这一档在矩阵里根本复现不出来（三条腿回同一份文本＝没有覆盖发生）。
#   ⇒ 用提示词自身的锚点串判腿：审校＝「资深翻译审校」（单段与批量共用），
#     带反馈重翻＝「前一次翻译被」，其余＝初翻。
#
# 两个触发器各自要制造的现场（正文标记刻意不同名，判据才能分清"谁出的栈"）：
#   UATPURITYREV  初翻回干净的中文（含 UAT-INIT-KEPT）；审校腿回"中文＋整句英文回译"
#                 （含 UAT-REV-BODY 与尾巴）。修法在 ⇒ 出栈是 UAT-INIT-KEPT 且无尾巴；
#                 把 ② 摘掉 ⇒ 出栈变 UAT-REV-BODY＋尾巴（两条判据同时红，这才是有判别力的锁）。
#   UATPURITYINIT 初翻腿自己就在尾段拼一句回译（含 UAT-INIT-TAIL）；审校腿回**空**
#                 （＝"这一轮不用改"，初翻是唯一的出栈来源）。这样 ④ 被摘掉时尾巴一定留在
#                 出栈里——若让审校腿回一份干净的中文，它会替 ④ 把尾巴掩盖掉，那条锁就是空转。
#   尾巴刻意**不带数字**：④ 的第三道前置是"数字序列逐字不变"，尾段带数字会被判"可能在传信息"
#   而原样保留，那是正确的保守行为，但不是本段要观测的形态。
#   带反馈重翻那一腿（正常流程里够不到）也照"保留审校现场"的口径回，
#   免得将来闸门判据一变，本段在 ② 被摘掉的情况下反而被重翻腿洗绿。
# ---------------------------------------------------------------------------
REVIEW_MARK = '资深翻译审校'
FEEDBACK_MARK = '前一次翻译被'
PURITY_REV = 'UATPURITYREV'
PURITY_INIT = 'UATPURITYINIT'
# 尾段回译：16 个连续拉丁词（㊶ 现网三条读数是 13／10／10 词，阈值 8 词两侧都留了余量）
PURITY_TAIL_EN = ('The following English sentence is a back translation '
                  'appended after the Chinese result by the upstream model')
PURITY_INIT_ZH = 'UAT-INIT-KEPT 本句为初翻保留版本，审校腿不该覆盖它。'
PURITY_REV_ZH = 'UAT-REV-BODY 审校腿改写后的正文，尾巴是模型自己补的回译。'
PURITY_TAIL_INIT_ZH = 'UAT-INIT-TAIL 初翻正文，尾段是它自己拼上去的回译。'


def prompt_kind(prompt):
    """按提示词锚点串判「这条回复是哪条腿要的」（㊶ 分腿判据的唯一依据）。
    参数 prompt 上游收到的最后一条 message 内容。返回 'review' / 'feedback' / 'initial'。"""
    if REVIEW_MARK in prompt:
        return 'review'
    if FEEDBACK_MARK in prompt:
        return 'feedback'
    return 'initial'


def purity_response(kind, prompt):
    """㊶ 触发器命中时的定点回包；不命中返回 None（走通用 TranslatedEN 形态）。
    参数 kind prompt_kind() 的判腿结果；prompt 原始提示词文本。"""
    if PURITY_REV in prompt:
        # 初翻腿＝干净中文；审校腿与重翻腿＝"中文＋整句回译"的不纯形态
        if kind == 'initial':
            return PURITY_INIT_ZH
        return PURITY_REV_ZH + ' ' + PURITY_TAIL_EN
    if PURITY_INIT in prompt:
        # 审校腿回空＝"这一轮没改"，出栈只能来自初翻 ⇒ ④ 是唯一能剥尾段的腿
        if kind == 'review':
            return ''
        return PURITY_TAIL_INIT_ZH + ' ' + PURITY_TAIL_EN
    return None


def latin_payload(src):
    """把源文折成一条**纯拉丁**的假译文载荷（为什么不回抄原文：见函数内注释）。

    ★ 这一条是 A16/T34/T39 三条文件链红的真身，也是 §三「假上游按我以为的契约实现＝给缺陷发证件」
      的第二个形态（第一个是不保留 <sN> 标记）：旧载荷写的是 `TranslatedEN(源文前 20 字)`，
      而引擎的出栈链里 `PostProcessTranslation` 的 en 腿**本来就要把残留汉字清洗掉**
      ——于是真译文变成 `TranslatedEN(，。)` 这样一个空壳，接着被 DEF-01 的指令回显判据
      (`isProseInstructionEcho`) 判"不可用"⇒ 写入点不落键 ⇒ 该语言一个产物都没有，
      完成话术却照写「文件翻译完成」（0 个文件）。
      ⇒ 桩必须回**目标语脚本**的正文，才不会替产品制造一个它自己治不了的形态。
      前缀字面 "TranslatedEN" 保留：A7/B2/B3/B8/T34/T47 那几条锁认的就是这个 token。
    · 源文里没有任何非 ASCII 字母时**原样透传**：那是产品的正确交付形态
      （translation_guard.go 判据 3′——纯代码/编号/符号段原样回吐不记「未译出」），
      桩在这里硬造一句拉丁话，反而会让那些段被判成"改写掉了"。
    """
    t = (src or '').strip()
    if t and all(ord(c) < 128 for c in t):
        return t
    return 'TranslatedEN item ' + hashlib.md5(t.encode('utf-8')).hexdigest()[:8]


def han_payload(src):
    """汉字系目标语（zh/zh_hant/ja/ko）沿用的**改造前逐字形态**：`TranslatedEN(源文前 20 字)`。
    为什么留一份而不是全站换成拉丁载荷：这些目标语的译文**本来就该带汉字**，
    桩给纯拉丁等于替产品造一个"审校腿脚本不纯"的现场——T70-c 那条交叉负向
    （无触发器的正常请求不许动纯度计数）就是这么被顶红的（run7 实测 delta=2）。
    非汉字符目标语走 latin_payload，两边各按**目标语自己的脚本**回包。"""
    t = (src or '').strip()
    return f'TranslatedEN({t[:20]})'


# 目标语脚本判据：提示词里的目标语名（中文界面用中文名、英文界面用英文名，两族都认）。
#   判据来源＝engine.translateInstructionCore 的两条分支（「翻译为简体中文」/「into Simplified Chinese」），
#   与 §一·12 那条「桩的形态来源必须从产品侧抄、不按字段名直觉拼」同一口径。
CJK_TARGET_MARKS = ('简体中文', '繁体中文', '日语', '韩语',
                    'Simplified Chinese', 'Traditional Chinese', 'Japanese', 'Korean')


def pick_payload(prompt_all):
    """按**目标语脚本**选载荷形态（whole-request 判一次，不逐行判）。
    参数 prompt_all 本轮全部 message 内容拼成的文本（含 system 指令，目标语名只在那里）。"""
    return han_payload if any(m in prompt_all for m in CJK_TARGET_MARKS) else latin_payload


def fake_translate(text, prompt_all=''):
    kind = prompt_kind(text)
    pinned = purity_response(kind, text)
    if pinned is not None:
        return pinned
    # ★ 载荷形态按目标语脚本分派（判据只看提示词，不看源文——源文脚本与目标语无关）
    payload = pick_payload(prompt_all)
    out = []
    for line in text.split('\n'):
        b = BATCH_MARK.match(line)
        if b:
            # 批量腿：标记必须原样保留，只换标记体内的正文（契约见上方 BATCH_MARK 注释）
            body = b.group(3)
            inner = ('<target>UAT-PSEUDO-CLEANSSED></target>' if 'UATPSEUDO' in body
                     else payload(body))
            out.append(f'{b.group(1)}<s{b.group(2)}>{inner}</s{b.group(2)}>')
            continue
        m = NUM.match(line)
        if m:
            if 'UATPSEUDO' in m.group(4):
                # UAT T48 触发器：模拟模型在无上下文短串上回显指令词元的「走形伪标签」形态
                # （实测交付 PDF 里的 `<target>#></target>`），必须被 stripPseudoTags 拆掉
                # 且保留标签体正文——若清洗链回退，T48 断言会立即抓到现场残留。
                out.append(f"{m.group(1)}{m.group(2)}{m.group(3)} <target>UAT-PSEUDO-CLEANSSED></target>")
            else:
                out.append(f"{m.group(1)}{m.group(2)}{m.group(3)} {payload(m.group(4))}")
        elif line.strip():
            if 'UATPSEUDO' in line:
                out.append("<target>UAT-PSEUDO-CLEANSSED></target>")
            else:
                out.append(payload(line))
        else:
            out.append(line)
    return '\n'.join(out)

class H(BaseHTTPRequestHandler):
    def log_message(self, *a): pass

    def _send(self, obj, code=200):
        b = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def do_POST(self):
        n = int(self.headers.get('Content-Length', 0))
        raw = self.rfile.read(n)
        try:
            req = json.loads(raw)
        except Exception:
            return self._send({'error': 'bad json'}, 400)

        if self.path.endswith('/chat/completions'):
            msgs = req.get('messages', [])
            last = next((m.get('content', '') for m in reversed(msgs) if isinstance(m, dict)), '')
            # ★ 目标语名只出现在 **system 指令**里（批量链把指令骨架上移为 system 消息，
            #   见 engine.BatchTranslate 的 B4 前缀重组），所以载荷分派必须看得见全部消息；
            #   而 prompt_kind/purity_response 两条判腿逻辑**照旧只看 last**，
            #   免得把分腿锚点的射程悄悄扩进系统提示词、改变定点触发腿的判定结果。
            all_text = '\n'.join(str(m.get('content', '')) for m in msgs if isinstance(m, dict))
            content = fake_translate(last, all_text)
            pt = max(1, len(last) // 4 + 50)
            ct = max(1, len(content) // 4)
            return self._send({
                'id': 'mock-1', 'object': 'chat.completion',
                'choices': [{'index': 0, 'message': {'role': 'assistant', 'content': content},
                             'finish_reason': 'stop'}],
                'usage': {'prompt_tokens': pt, 'completion_tokens': ct, 'total_tokens': pt + ct},
            })

        if self.path.endswith('/embeddings'):
            inp = req.get('input', [])
            if isinstance(inp, str):
                inp = [inp]
            data = []
            for i, t in enumerate(inp):
                h = hashlib.sha256(str(t).encode()).digest()
                v = [((x % 200) - 100) / 100.0 for x in h[:512]]
                v = v + [0.0] * (1024 - len(v))
                norm = math.sqrt(sum(x * x for x in v)) or 1.0
                data.append({'object': 'embedding', 'index': i,
                             'embedding': [x / norm for x in v]})
            return self._send({'object': 'list', 'data': data,
                               'usage': {'prompt_tokens': 10, 'total_tokens': 10}})

        self._send({'error': 'not found'}, 404)

if __name__ == '__main__':
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8901
    ThreadingHTTPServer(('127.0.0.1', port), H).serve_forever()
