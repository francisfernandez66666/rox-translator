#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# ============================================================================
# scripts/uat/mock_llm.py — UAT 专用 mock LLM 服务
# 提供 OpenAI 兼容 /v1/chat/completions 与 /v1/embeddings：
#   - chat：按行保留行号前缀、内容替换为 "TranslatedEN(...)" 译文字样
#     （规避引擎「回显检测」把原样返回判为未翻译）
#   - embeddings：确定性伪向量（1024 维，L2 归一化），用于 KB 检索链路
# 用法：python3 mock_llm.py [port]   # 默认 8901
# ============================================================================
import json, re, hashlib, math, sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

NUM = re.compile(r'^(\s*)(\d+)([.、\)])\s*(.*)$')

def fake_translate(text):
    out = []
    for line in text.split('\n'):
        m = NUM.match(line)
        if m:
            out.append(f"{m.group(1)}{m.group(2)}{m.group(3)} TranslatedEN({m.group(4)[:20]})")
        elif line.strip():
            out.append(f"TranslatedEN({line.strip()[:30]})")
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
            content = fake_translate(last)
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
