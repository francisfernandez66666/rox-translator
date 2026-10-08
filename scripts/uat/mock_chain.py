#!/usr/bin/env python3
# ============================================================================
# scripts/uat/mock_chain.py — USDT 链上 mock（TronGrid 兼容 + 测试控制口）
# 职责：为 UAT T42 提供确定性「链上环境」：
#   POST /wallet/getnowblock                 → {"block_header":{"raw_data":{"number": TIP}}}（链头）
#        ★ 2026-10-08 ㊾ 定档：桩的形态**必须来自真上游的实测读数**，不能来自"我以为的契约"。
#          本轮用本机独立网络把上游问了一遍：`GET /v1/blocks/latest`、`GET /v1/blocks?limit=1`、
#          `GET /v1/blocks/1000`、`GET /v1/statistics` **四条全 404**，能通的只有
#          `POST /wallet/getnowblock`（200，高度在嵌套的 block_header.raw_data.number）。
#          于是这版做了三件事：
#            ① 链头改服务 POST /wallet/getnowblock，回**真上游那一层的嵌套结构**（不再回平铺的 number）；
#            ② 把 /v1/blocks 那一族（含 /latest、含带参数的列表形态）**一并改成照真上游回 404**——
#              留着应答等于给"谁把端点写回 /v1/blocks/latest"那条回归发证，
#              与 ⑮ 当年"旧 mock 服务裸 /v1/blocks"是同一个死法，只是换了一层；
#            ③ 同一批退役路径只保留在**产品侧解析**里（tronNewestBlock 的四腿，兼容上游改回来），
#              桩不再服务它们 ⇒ 现网如果只用新端点，UAT 与现网打的是同一条路。
#        ★ 历史（保留给排障的人看，别照着实现）：2026-10-05 ⑮ 曾把链头做成 GET /v1/blocks/latest，
#          理由是"裸 /v1/blocks 恒 404"——那个推理只证到"裸路径不行"，没证到"带 /latest 就行"，
#          于是 T42 又一次全绿而现网继续 404（八天⇒㊾）。教训同 AGENTS §一·12「桩只认协议」。
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
        # ★ ㊾：GET 链头这一族**一律照真上游回 404**（含 /wallet/getnowblock——上游那族只认 POST，
        #   所以"路径写对、方法写错"也必须当轮失败；这正是旧覆盖口只改路径不改方法时运营踩到的形态）。
        #   刻意不给 /v1/blocks/latest 与带参数的 /v1/blocks 留应答：留了就是给回归发证（见文件头 ②）。
        if u.path.startswith("/v1/blocks") or u.path == "/wallet/getnowblock":
            self._send({"status": 404, "error": "Not Found"}, 404)
            return
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
            if u.path == "/wallet/getnowblock":
                # ★ 真上游嵌套形态（㊾ 实测）：高度在 block_header.raw_data.number，
                #   且该族字段在 protobuf→JSON 的转换里**可能带引号**——这里按现网读到的裸数字回，
                #   带引号那一档由产品侧单测 TestTronHeadAcceptsFourShapes 覆盖（桩不制造第二种形态）。
                self._send({"block_header": {"raw_data": {"number": STATE["tip"], "timestamp": 1762000000000}}})
                return
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
