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
# 用法：python3 mock_assist.py [port] [run-tag] [mode]   # 默认 8796、mode=seq
#   mode=seq   补翻那一枪回**第二稿**（残片变少 ⇒ 补翻被采用）——093x 四条腿的默认档；
#   mode=echo  补翻那一枪**原样吐回**收到的上一稿（残片一个没少 ⇒ 三条硬判据必然拒用），
#              这是 ★ 094x「补翻被拒之后的确定性正字表」那条腿的夹具：
#              只有让补翻真的失败，才能证明就地改写那条二线防线接上了，而不是被补翻顺手修掉的。
#   ⚠️ echo 档的 gen 会**额外挂一句** ECHO_EXTRA（见其定义）：094x 的第二类现网词形「费用」「什么」
#      不在逐字原文里，而原文里带这两个字的那半句在 ※旁白括号内、出栈前会被末道卫生整段剥掉
#      ⇒ HTTP 面观测不到。只锚「言語」会被脏稿里那句合法日文「文字数**と言語**に応じて」子串命中，
#      实测成一条恒绿假锁（把出站腿拆掉它照样绿）——所以补一句括号外的形态，而不是放宽判据。
# 统计：GET /uat/stats → {"ok":true,"run":"…","mode":"seq|echo","calls":N,"gen":N,"rewrite":N,"canned":N}
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

# echo 档（W9 那台"补翻必拒"桩）专用的**追加句**。为什么要有它，而不是直接改上面的逐字原文：
#   094x 的现网实证词形一共两类——第一类（选択／系数）已经在 DIRTY_FIRST_DRAFT 里，
#   第二类「费用」「什么」是**换件当天复问**才抓到的（正字表里那两条 ★现网实证 注释即其来源）。
#   DIRTY_FIRST_DRAFT 与单测 leakReply093x 是同一份"逐字录音"，动它＝把 093x 的 W1～W8 六条腿一起改了口径；
#   所以第二类按**同一形态**拼成一句挂在 echo 档的尾巴上：判残与正字表要的是**词的射程**，不是第二份录音。
#   ⚠️ 这句刻意不带括号、不带算式、不带品牌，免得踩到末道卫生／报价守卫／品牌归一任何一道
#      ——被它们吃掉的话，W9c 就变成"永远达不到"的假红（本轮就在「入力语言」上真踩过一次：
#      那个词只出现在 ※旁白括号里，出栈前整段被剥，HTTP 面根本观测不到）。
ECHO_EXTRA = "実際の费用はどのくらいですか。为什么価格が変動するのでしょうか。"

# canned（欢迎词／chips）用的干净短句：不含中文词形，免得打开词那半边被本段断言误伤。
CANNED_REPLY = "LangCross へようこそ。ご質問があればお気軽にどうぞ。"

STATE = {"calls": 0, "gen": 0, "rewrite": 0, "canned": 0}
# 本次运行的标识（argv[2]）：就绪探针靠它认「应答的是我刚起的这一台桩」。
# 不这么做的后果是真踩过的：端口被上一轮残留的假上游占着时，新起的桩 bind 失败即退出，
# 而 curl 照样拿到 200 ——那台旧桩的 gen/rewrite 计数是上一轮攒下的，
# W7「上游恰好两次」那条硬账就跟着失真（端口抢占既能造出假红也能造出假绿）。
RUN_TAG = ""
# 补翻那一枪的回法（argv[3]）：见文件头 mode 说明。默认 seq＝回第二稿（补翻被采用）。
MODE = "seq"


def last_user_content(payload):
    """取请求体里最后一条 user 消息的正文（补翻提示词就在这一条里，见 han_residue.go）。"""
    for msg in reversed(payload.get("messages") or []):
        if msg.get("role") == "user" and isinstance(msg.get("content"), str):
            return msg["content"]
    return ""


def extract_draft(prompt):
    """从补翻提示词里把「上一版译文」那段抠出来（mode=echo 用它原样回吐）。

    提示词的固定收尾是「…【上一版日文译文】\\n<草稿>\\n---」（见 repairHanResidueBase）。
    取**最后一次**出现的锚点：translateContract 那段口径文本里也带【】，只有最后一个是译文块。
    抠不到（提示词改了收尾）就返回 None，调用方回干净第二稿——**宁可退回默认档，
    也不许让桩返回一句空文本把整段判据打成"上游没通"**。
    """
    i = prompt.rfind("【上一版")
    if i < 0:
        return None
    j = prompt.find("】", i)
    if j < 0:
        return None
    tail = prompt[j + 1:]
    if tail.startswith("\n"):
        tail = tail[1:]
    if tail.endswith("\n---"):
        tail = tail[: -len("\n---")]
    return tail


def classify(payload):
    """按提示词里的稳定标记把请求分成三类，返回 (类别, 该回的内容)。"""
    blob = json.dumps(payload, ensure_ascii=False)
    if "整段重写一遍" in blob:
        if MODE == "echo":
            draft = extract_draft(last_user_content(payload))
            if draft:
                return "rewrite", draft
        return "rewrite", CLEAN_SECOND_DRAFT
    if "直接回复用户" in blob:
        # echo 档多加一句 094x 的第二类现网词形（见 ECHO_EXTRA 的来处与"别放进括号"那条约束）
        if MODE == "echo":
            return "gen", DIRTY_FIRST_DRAFT + ECHO_EXTRA
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
            self._send({"ok": True, "run": RUN_TAG, "mode": MODE, **STATE})
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
    global RUN_TAG, MODE
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8796
    RUN_TAG = sys.argv[2] if len(sys.argv) > 2 else ""
    MODE = sys.argv[3] if len(sys.argv) > 3 else "seq"
    ThreadingHTTPServer(("127.0.0.1", port), H).serve_forever()


if __name__ == "__main__":
    main()
