#!/usr/bin/env python3
# ============================================================================
# scripts/uat/mock_chain.py — USDT 链上 mock（TronGrid 兼容 + 测试控制口）
# 职责：为 UAT T42 提供确定性「链上环境」：
#   GET  /v1/blocks/latest                   → {"block_header":{"number": TIP}}（链头）
#        ★ 2026-10-05 ⑮ 修法：真 TronGrid 的裸 GET /v1/blocks 是 **404**（该路径必须带
#          limit/order_by 等参数），链头取 /v1/blocks/latest。旧 mock 把裸 /v1/blocks
#          做成有应答，于是产品侧那条死腿（打错端点、恒取不到高度）在 UAT 里**自己给自己
#          发证**——T42 全绿但现网一行链头日志都在报 404。现在裸 /v1/blocks 照真上游回 404，
#          端点写错就当轮扫描失败，判据才有牙齿。
#   GET  /v1/accounts/{addr}/transactions?... → {"transfers":[{tx_id,block_number,from,to,value}]}
#        （to=addr、block_number>=from_block；value 为 6 位小数 micro 字符串）
#   POST /inject  {to,from,value,block?}     → 注入一笔转入（默认当前块），返回 tx_id
#   POST /advance {n}                        → 链头 +n（模拟确认数增长）
#   GET  /state                              → 全量状态（断言辅助，run_uat 就绪探针打这里）
# 服务端经 env USDT_TRON_BASE 指向本进程；仅 UAT 使用，绝不公网暴露。
# ============================================================================
import json
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse, parse_qs

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 8902
LOCK = threading.Lock()
STATE = {"tip": 1_000_000, "transfers": [], "seq": 0}


class H(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def _send(self, obj, code=200):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        # GET 路由：链头查询 / 指定地址转入列表（按 to+from_block 过滤）/ 全量状态
        u = urlparse(self.path)
        q = parse_qs(u.query)
        if u.path == "/v1/blocks/latest":
            # 链头官方形态：{"block_header":{"number": TIP}}（产品侧 tronNewestBlock 的第一优先腿）
            with LOCK:
                self._send({"block_header": {"number": STATE["tip"]}})
            return
        if u.path == "/v1/blocks" and (q.get("limit") or q.get("order_by")):
            # 带参数的列表形态才回 data[]，与真 TronGrid 一致（备用腿，非默认取数路径）
            with LOCK:
                self._send({"data": [{"block": STATE["tip"]}]})
            return
        # ★ 裸 /v1/blocks＝真上游的 404（缺 limit/order_by），这里刻意不兜活：
        #   旧 mock 让它有应答＝给产品侧那条打错端点的死腿发绿证，T42 因此测不到现网故障。
        if u.path.startswith("/v1/accounts/") and u.path.endswith("/transactions"):
            addr = u.path.split("/")[3]
            frm = int((q.get("from_block") or ["0"])[0])
            with LOCK:
                out = [t for t in STATE["transfers"] if t["to"] == addr and t["block_number"] >= frm]
            self._send({"transfers": out, "ret": [{"resultcode": "SUCCESS"}]})
            return
        if u.path == "/state":
            with LOCK:
                self._send(dict(STATE))
            return
        self._send({"error": "not found"}, 404)

    def do_POST(self):
        # POST 路由：/inject 注入一笔转入（可指定 tx_id/block，供 T42 幂等与尾数对单用例）
        #           /advance 链头 +n（配合确认数阈值用例）
        n = int(self.headers.get("Content-Length") or 0)
        try:
            body = json.loads(self.rfile.read(n) or b"{}")
        except Exception:
            self._send({"error": "bad json"}, 400)
            return
        u = urlparse(self.path)
        with LOCK:
            if u.path == "/inject":
                STATE["seq"] += 1
                tx = {
                    "tx_id": str(body.get("tx_id") or ("%064x" % (0xd000 + STATE["seq"]))),
                    "block_number": int(body.get("block") or STATE["tip"]),
                    "from": str(body.get("from") or "Tfrom000000000000000000000000000X"),
                    "to": str(body.get("to") or ""),
                    "value": str(int(body.get("value") or 0)),
                }
                if not tx["to"] or int(tx["value"]) <= 0:
                    self._send({"error": "to/value required"}, 400)
                    return
                STATE["transfers"].append(tx)
                self._send({"ok": True, **tx})
            elif u.path == "/advance":
                STATE["tip"] += max(1, int(body.get("n") or 1))
                self._send({"ok": True, "tip": STATE["tip"]})
            else:
                self._send({"error": "not found"}, 404)


if __name__ == "__main__":
    print(f"[mock_chain] listening :{PORT}", flush=True)
    ThreadingHTTPServer(("127.0.0.1", PORT), H).serve_forever()
