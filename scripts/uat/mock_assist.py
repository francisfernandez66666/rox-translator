#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# ============================================================================
# scripts/uat/mock_assist.py — 挂件（assist）UAT 专用假上游
#
# 为什么要单独一个桩，而不复用 mock_llm.py：
#   mock_llm.py 是**翻译服务**的桩（把整段按行换成 TranslatedEN(...)），它服务的是主站翻译链；
#   挂件这条链要验的是「模型答完之后，出站那四道守卫有没有把现网那几个漏点收干净」，
#   判据全靠**回复内容具体长什么样**。所以要一个能逐字回放现网原文、
#   并按「这是生成请求／这是补翻重写请求／这是 canned 本地化请求」分流作答的桩。
#
# 三类请求的判据（按提示词里的稳定标记分流，**不按调用次序计数**）：
#   ① 补翻重写：请求体含「整段重写一遍」（见 backend-go/internal/assist/engine/han_residue.go
#      的补翻提示词）→ 回**第二稿**。
#   ② 对话生成：请求体含「直接回复用户」（buildSystemPrompt 的收尾标记，见 engine.go）
#      → 回**第一稿**＝2026-09-30 真机挂件复问从现网拿回的逐字原文。
#   ③ 其余（欢迎词／chips 的 canned 本地化）→ 回一句干净的短日文，不参与本段断言，
#      但**必须非空**：canned 空译文会把打开词打成空气泡。
#   ⚠ ①必须排在②前面判：补翻请求会把对话上下文一起带上，同一份请求体里两个标记都有，
#     先判②就会把重写请求当成生成、再回一遍脏稿，补翻永远不采用（四条腿一起假绿）。
#   ⚠ 为什么不按次序计数分流：greet 的本地化也打这个桩，"第 N 次调用"会被它多算一次，
#     于是「上游恰好两次」这条腿变成随机红灯——判据只能问请求内容。
#
# 第二稿的取舍（**刻意留着三样脏东西**，都是后三道守卫的活计，桩不许替它们预先洗干净）：
#   - 「2000×400+7.5 で約 150 ポイント」：留给报价守卫（quote_guard.go）整句换成不承诺总额那句；
#   - 「能与」：品牌名被翻坏的现网形态，留给品牌归一（brand_guard.go）还原成「能言」；
#   - 「（※日本語で回答するため…）」与裸方括号「[ pricing ページで…]」：留给末道卫生
#     （sanitizeVisitorText／unwrapBrokenLinkBrackets，见 reply_lang_check.go）。
#   第一稿里那五处**中文词形／简体字形**（选択／系数／扣费／プロfessional／入力语言）在第二稿
#   必须全部写成日文正字（選べます／係数／お支払い／プロフェッショナル／言語）——
#   补翻采纳的三条硬判据之一是「残片数量严格变少」，桩不认这条就会整条链回退成"保留脏稿"。
#
# 用法：python3 mock_assist.py [port]        # 默认 8796
# 统计：GET /uat/stats → {"calls":N,"gen":N,"rewrite":N,"canned":N}
# ============================================================================
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

# 第一稿：现网逐字原文（同一形态在单测里也钉了一份：backend-go/internal/assist/engine/
# han_residue_reply_test.go 的 leakReply093x；改任何一处都要想到另一处）。
DIRTY_FIRST_DRAFT = (
    "日本語から中国語への翻訳は、源文字数（源語の文字数）で計算されます。"
    "例えば1,000文字の日本語を翻訳する場合、**快速モードで150ポイント**か、"
    "**プロfessionalモードで400ポイント**か选択が必要です。ポイント数は原文の文字数（日本語は全角文字含む）× 系数で算出されますが、"
    "実際の扣费は原文の文字数と言語に応じて変動します。お手数ですが、原文を送信いただければ正確なポイント数をご案内できます。"
    "[ pricing ページで詳細を確認]  （※日本語で回答するため、必要に応じて「ポイント」を用い、"
    "入力语言が日本語であることを考慮して翻訳を実施。）"
)

# 第二稿：五处词形按日文正字写；算式／品牌错形／旁白括号／裸方括号照抄留着（见文件头取舍）。
CLEAN_SECOND_DRAFT = (
    "日本語から中国語への翻訳は、源文字数で計算されます。"
    "例えば1,000文字の日本語を翻訳する場合、快速モードとプロフェッショナルモードのどちらかを選べます。"
    "ポイント数は原文の文字数（日本語は全角文字含む）で決まりますが、実際のお支払いは原文の文字数と言語に応じて変動します。"
    "お手数ですが、原文を送信いただければ正確なポイント数をご案内できます。"
    "プロモードは 2000×400+7.5 で約 150 ポイントです。"
    "能与へのご相談も歓迎です。[ pricing ページで詳細を確認]  （※日本語で回答するため、翻訳を実施。）"
)

# canned（欢迎词／chips）用的干净短句：不含中文词形，免得打开词那半边被本段断言误伤。
CANNED_REPLY = "LangCross へようこそ。ご質問があればお気軽にどうぞ。"

STATE = {"calls": 0, "gen": 0, "rewrite": 0, "canned": 0}
# 本次运行的标识（argv[2]）：就绪探针靠它认「应答的是我刚起的这一台桩」。
# 不这么做的后果是真踩过的：端口被上一轮残留的假上游占着时，新起的桩 bind 失败即退出，
# 而 curl 照样拿到 200 ——那台旧桩的 gen/rewrite 计数是上一轮攒下的，
# W7「上游恰好两次」那条硬账就跟着失真（端口抢占既能造出假红也能造出假绿）。
RUN_TAG = ""


def classify(payload):
    """按提示词里的稳定标记把请求分成三类，返回 (类别, 该回的内容)。"""
    blob = json.dumps(payload, ensure_ascii=False)
    if "整段重写一遍" in blob:
        return "rewrite", CLEAN_SECOND_DRAFT
    if "直接回复用户" in blob:
        return "gen", DIRTY_FIRST_DRAFT
    return "canned", CANNED_REPLY


class H(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def _send(self, obj, code=200):
        # 分隔符钉死成紧凑形态（冒号后不带空格）：本矩阵其它段的判据一律按后端 Go 的
        # `"ok":true` 字面量写，桩这边若回 `"ok": true` 会让"链路明明通着"的判据恒假。
        # ⚠️ W0 首跑就是栽在这里（假上游活着、探针按 `"ok":true` 找，读不到就整段判不来）。
        # 与下方就绪探针的容错写法（`: *true`）配对，任一侧改了格式都不至于再红一次。
        body = json.dumps(obj, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        # 只读统计：矩阵用它证明「补翻只打了一次，没变成反复重写」。
        # run 回的是本进程启动时拿到的标识（见 RUN_TAG）——就绪探针要认「应答的是本次起的这台桩」，
        # 只看"有人应答"会把上一轮残留的旧桩当自己起的那个，计数口径就错了（端口抢占形态真踩过）。
        if self.path.startswith("/uat/stats"):
            self._send({"ok": True, "run": RUN_TAG, **STATE})
        else:
            self._send({"ok": False, "error": "not found"}, 404)

    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        raw = self.rfile.read(n)
        try:
            payload = json.loads(raw.decode("utf-8") or "{}")
        except Exception:
            self._send({"error": "bad json"}, 400)
            return
        kind, content = classify(payload)
        STATE["calls"] += 1
        STATE[kind] += 1
        if not self.path.startswith("/v1/chat/completions"):
            self._send({"error": "unsupported path: " + self.path}, 404)
            return
        self._send({
            "id": "uat-assist-%d" % STATE["calls"],
            "object": "chat.completion",
            "model": payload.get("model", "uat-assist-model"),
            # finish_reason 必须是 stop：补翻采纳的三条硬判据里「未被 max_tokens 截断」是第一条，
            # 桩若回 length，补翻永远不被采用，四条腿一起变成假绿（见 localize.go 文件头）。
            "choices": [{"index": 0,
                         "message": {"role": "assistant", "content": content},
                         "finish_reason": "stop"}],
            "usage": {"prompt_tokens": 100, "completion_tokens": 100, "total_tokens": 200},
        })


def main():
    global RUN_TAG
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8796
    RUN_TAG = sys.argv[2] if len(sys.argv) > 2 else ""
    ThreadingHTTPServer(("127.0.0.1", port), H).serve_forever()


if __name__ == "__main__":
    main()
