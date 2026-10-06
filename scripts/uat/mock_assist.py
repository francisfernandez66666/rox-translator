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
#
# ★ 0AR 第 4 波（2026-10-06）新增：**POST /uat/set 控制口**——按档点名 canned 出栈闸要拦的形态。
#   为什么要一个控制口，而不是多起几个桩、或者干脆指望真模型翻坏：
#   这道闸有六道拒绝档（空／行数／内部记号／装饰线／汉字残渣／凭空多出品牌名）＋两档纯观测
#   （脚本不纯／品牌名脱落），每一档的靶子都不同，而矩阵要锁的是**闸门在 HTTP 面上接没接线**，
#   不是「某一句提示词会不会翻成那样」。只有假上游能按档投出那一形态
#   （手法同 ⑮ 那批给 mock_chain.py 加的 /inject——桩必须能按判据点名，不能只会演一遍好戏）。
#   ⚠️ 这个口只活在 UAT 桩里，产品侧没有任何对应物；它测的是「模型真翻坏了，拦不拦、
#      以什么 reason 拦、缓存写不写」，不测「模型会不会翻坏」。
#   请求体＝可选键的 JSON：{"canned":"<档名>","gen":"<档名>"}，缺省的键保持原状，
#   复位＝{"canned":"clean","gen":"dirty"}。当前设置随 /uat/stats 一起回显，
#   所以矩阵每一条都能先确认「桩此刻确实在我要的那一档」（这一腿不是客气：
#   控制口没接上时所有档位腿会一起绿，而那正是「闸门全开」的形态）。
#
# canned 档位（`canned`）与它要顶出的那一档 reason，一一对应、不许混：
#   clean        每行一句纯拉丁文、行数与原文一致、品牌记号按语种档落名 ⇒ **闸门必须放行并写缓存**
#                （这是整段 X 的正向对照：没有它，"缓存没写"那几条负向锁全是空转）
#   lines        chips 多送一行 ⇒ canned_line_count
#   placeholder  正文留一个 <INFO> 尖括号记号 ⇒ canned_placeholder_residue
#   separator    尾巴多一行 ---            ⇒ canned_separator_residue
#   residue      句里留一个没翻的中文词      ⇒ canned_han_residue（同时把补翻那一枪改成原样吐回，
#                否则补翻顺手修好了，闸门就没得判——这一条与 094x 那台 echo 桩同一个道理）
#   brand        chips 每行前面挂上品牌名（原文根本没提）⇒ canned_brand_injected
#   brand_drop   欢迎词里一个品牌痕迹都不留   ⇒ canned_brand_dropped（**只观测、不拦**：正文照发、缓存照写）
#   repaired     日文欢迎词带 ⟨LangCross⟩ 装饰括号＋「能与」错形 ⇒ 修正腿就地改好，
#                库里那一行必须已是「能言」且没有括号残渣（reason=canned_repaired 是一行 INFO）
#   http500      canned 那一枪直接退 500    ⇒ 同步腿失败 → 后台腿失败 → **开退避窗口**（㊷）
#   ⚠️ 另一档纯观测 `canned_script_impure` **不需要档位**：拿 clean 档打泰文界面就行
#      ——那句纯拉丁的假译文正是"目标脚本占比过低"的形态，而它不含汉字、行数为 1，
#      其余判据一条都不命中，所以这一档只能由 clean＋th 观测到（另加一档反而多一个变量）。
# gen 档位（`gen`）：
#   dirty        093x 那一份逐字原文（默认，W 段用它）
#   fabricated   ★ ⑲ 编造承诺三档各一句（假存量／清单外交付物／自配词条译法）＋一句正当正文
#                ⇒ 前三句必须被整句丢掉、第四句一字不动
#   actions      一句英文正文＋末行【go:billing,chat】 ⇒ 顶出 ⑱ 按钮名本地化那一条腿
#
# 统计：GET /uat/stats → {"ok":true,"run":"…","mode":"seq|echo","calls":N,"gen":N,"rewrite":N,
#                        "canned":N,"set":{"canned":"…","gen":"…"},"canned_model":"…","gen_model":"…"}
#   canned_model／gen_model 记的是**最后一枪**请求体里的 model 名，㊷① 那条
#   「canned 用独立快模型、对话那一枪仍用主模型」只能这样读（两侧都在同一个桩上）。
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

# ★ 0AR 第 4 波：闸门按档点名用的那份「干净底稿」＋六个坏形态。
#
# 为什么这里的译文是拉丁文而不是目标语原文：出栈闸的六道判据里，**行数／装饰线／内部记号／
# 汉字残渣／品牌注入**全都只读物本身的形态，与语种无关；拿一句假造的拉丁文当底稿，
# 每条档位腿只需要改动"要顶出的那一处"，其余判据保证不命中——
# 这比逐语种写十四份译文可靠得多（写十四份真译文就是一份没人核过的翻译语料，
# 下一次它自己踩过残渣判据，整段档位腿会一起变成查不出原因的红灯）。
# ⚠️ 底稿里不许出现「能言／LangCross」以外的品牌写法，也不许出现任何汉字：
#    那两类一出现，clean 档就会同时顶出 brand 注入与汉字残渣两档，正向对照当场失效。
CLEAN_WELCOME = "Welcome! I can translate your files, conversations and company term base."
CLEAN_WELCOME_BRAND = "Welcome from LangCross! I can translate your files, conversations and term base."
CLEAN_CHIP = "How does it work here?"

# ⑲ 编造承诺的靶子：三档各一句（假存量／清单外的交付物／自配的词条译法）＋一句正当正文。
# 第四句是**正向对照**——没有它，"三句被删"这条判据在"整段回复被吃掉"那种坏实现下照样绿。
# ⚠️ 三句的写法逐条对着 reply_fabrication.go 的判据形态挑的，别"顺手改措辞"：
#   - 「327 个标准词」＝现网取证那一句（① 的正序档，量词＋存量名词都在名单里）；
#   - 「sandbox」② 的拉丁名单词，且 seed 全量文本里 0 命中（本轮实测），
#     所以它一定问得出"库里没有"；换成 excel／webhook 就变合法（库里真有）。
#   - 第三句不带「为」：现网取证正是「固定译 "ignition plug"」那种裸「译」形态，
#     只测带「为」的写法会让 ③ 整条腿对现网缺陷无感（该文件 216-223 行那条注释）。
#   - 三句都不许出现「没有／不支持／无法」这一族拒绝豁免标记：它们会让 ②③ 整句被豁免，
#     于是这一档测的只剩 ①（豁免腿本身另有一批单测锁，见 TestGuardReplyFabricationKeepsRefusal）。
FABRICATED_DRAFT = (
    "我们已经建好了 327 个标准词，直接就能用。"
    "可以给您开一个 API sandbox 用来联调。"
    "「火花塞」固定译 ignition plug，后面都按这一份走。"
    "您可以先在编辑器里试一段，觉得合适再充值。"
)

# ⑱ 按钮名的靶子：正文一句英文，末行给两个功能入口 key。
# 【go:…】控制序列会被 postProcess 摘成 actions，而摘出来的 name 是库里那句中文
# （「充值与账单」「对话翻译」）——过去它**原样**塞进英文回复，界面就是"一段英文＋三个中文按钮"。
# 这一档要证明的是 localizeActionNames 真挂在 Respond 那条咽喉上，
# 所以正文语言与界面语言都得是非中文档（语种吃的是"本轮作答语言"那一个答案）。
ACTIONS_DRAFT = (
    "Sure - here is the fastest way to try it: send one paragraph and I will show the result. "
    "You can also check the credits first.\n【go:billing,chat】"
)

# canned 各坏形态相对 clean 底稿的**增量**（只改要顶出的那一处，其余一字不动）。
SEP_TAIL = "\n---"
PLACEHOLDER_TAIL = " <INFO>"
RESIDUE_INFIX = " 文件翻译"
BRAND_DROP_REPLACEMENT = "Welcome! I can translate your files, conversations and term base."

STATE = {"calls": 0, "gen": 0, "rewrite": 0, "canned": 0,
         # 最后一枪请求体里的 model 名（㊷① 那条"专用快模型"只能这样读，见文件头）
         "canned_model": "", "gen_model": ""}
# ★ 0AR 第 4 波的档位选择（只由 /uat/set 改，进程内一份，见文件头那张档位表）。
# 默认值让 W 段（093x／094x 那批腿）行为一字不变：canned 走原来那句干净日文、gen 走逐字原文。
SET = {"canned": "legacy", "gen": "dirty"}

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


def extract_src(prompt):
    """把 canned 那一枪的**中文原文**抠出来（translateOnce 的收尾是「\\n---\\n<原文>\\n---」）。

    为什么要抠原文而不是让矩阵把条数告诉我：桩要能**自己数行数**，
    这样 `lines` 档才是"在契约要求的行数上多加一行"，而不是"桩随口回五行"——
    前者顶出的是 `canned_line_count`，后者万一原文本来就该有五行，判据就空转了。
    取**最后一个** `---`：口径段里也可能出现分隔符，只有最后那一对是输入围栏
    （与 extract_draft 用 rfind 的理由同源）。抠不到就回空串，调用方按 clean 底稿出，
    **不许**回一句空文本——空文本会被 translateOnce 判成"译文为空"，档位腿整个变成"上游没通"。
    """
    t = prompt.rstrip()
    if not t.endswith("---"):
        return ""
    t = t[:-3]
    i = t.rfind("---")
    if i < 0:
        return ""
    return t[i + 3:].strip()


def build_canned(defect, src):
    """按档位把 canned 那一枪的产物拼出来（档位表见文件头）。"""
    lines = [l.strip() for l in src.split("\n") if l.strip()]
    multi = len(lines) > 1          # ≥2 行＝chips（一次翻整串、按行拆回）；单行＝欢迎词／按钮名
    has_brand = ("⟦BRAND⟧" in src) or ("能言" in src) or ("LangCross" in src)
    if defect == "legacy":
        # W 段（093x／094x）沿用的那一句：它们只要求"canned 这一路非空"，不看形态
        return CANNED_REPLY
    if defect == "brand_drop":
        # 原文提了品牌名、译文一个痕迹都不留 ⇒ canned_brand_dropped（★ 只观测：正文照发、缓存照写）
        return BRAND_DROP_REPLACEMENT
    if multi:
        body = [CLEAN_CHIP for _ in lines]
        if defect == "lines":
            body = body + ["One more line than the source asks for."]
        elif defect == "brand":
            # chips 原文根本没提品牌名，译文却每行前面挂着它（现网 ru 四条各前挂 "LangCross: " 的形态）
            body = ["LangCross: " + b for b in body]
        elif defect == "residue":
            body = [b + RESIDUE_INFIX for b in body]
        elif defect == "separator":
            body = body + ["---"]
        elif defect == "placeholder":
            body[0] = body[0] + PLACEHOLDER_TAIL
        return "\n".join(body)
    # 单行档：欢迎词／按钮名（wantLines 为 0 或 1，行数这条判据都过得去）
    if defect == "repaired":
        # ★ 0AR 第 4 波修正腿的两个现网实证形态一起放进来（现网 ko 那行是括号、ar/ja 那类是错形）：
        # 「⟨LangCross⟩」＝紧贴品牌名的装饰括号（现网 ko 落库时就是这一串），
        # 「能与」＝日文档的品牌名错形（093x 红腿三同一个词形，只是这一次出现在 canned 那一路上）。
        # 两条都属于"我们已知的形态、且确定性地能改回去"，所以闸门先就地修、修完再判——
        # 这一档**必须**放行（正文照发、缓存照写），且库里那一行得是清洗后的字节。
        return "⟨LangCross⟩へようこそ。翻訳のご相談は能与まで、いつでもどうぞ。"
    out = CLEAN_WELCOME_BRAND if has_brand else CLEAN_WELCOME
    if defect == "brand_drop":
        out = BRAND_DROP_REPLACEMENT
    elif defect == "separator":
        out = out + SEP_TAIL
    elif defect == "placeholder":
        out = out + PLACEHOLDER_TAIL
    elif defect == "residue":
        out = out + RESIDUE_INFIX
    elif defect == "lines":
        out = out + "\n" + CLEAN_CHIP
    return out


def classify(payload):
    """按提示词里的稳定标记把请求分成三类，返回 (类别, 该回的内容)。"""
    blob = json.dumps(payload, ensure_ascii=False)
    if "整段重写一遍" in blob:
        # ★ 0AR 第 4 波：档位腿一律原样吐回上一稿——补翻若把坏形态顺手修好，
        # 出栈闸就没得判了（这与 094x 那台 echo 桩是同一个道理，只是这里由档位控制口决定）。
        if MODE == "echo" or SET.get("canned", "legacy") != "legacy":
            draft = extract_draft(last_user_content(payload))
            if draft:
                return "rewrite", draft
        return "rewrite", CLEAN_SECOND_DRAFT
    if "直接回复用户" in blob:
        g = SET.get("gen", "dirty")
        if g == "fabricated":
            return "gen", FABRICATED_DRAFT
        if g == "actions":
            return "gen", ACTIONS_DRAFT
        # echo 档多加一句 094x 的第二类现网词形（见 ECHO_EXTRA 的来处与"别放进括号"那条约束）
        if MODE == "echo":
            return "gen", DIRTY_FIRST_DRAFT + ECHO_EXTRA
        return "gen", DIRTY_FIRST_DRAFT
    # ★ 必须先把**中文原文**从提示词里抠出来再交给 build_canned：整段提示词本身有好几行，
    # 直接把 prompt 传进去，`build_canned` 会按"多行＝chips"分支回一排 chip 句，
    # 于是**欢迎词那一枪拿到的是 chips 的译文**（本轮 X1 首跑就是这么红的：
    # i18n:welcome:en 里躺着七行 "How does it work here?"，而闸门按 wantLines=0 放行它）。
    # 抠不到原文（提示词收尾格式变了）时 extract_src 回空串，build_canned 走单行档的干净底稿，
    # 仍然是"一句话译文"——比按整段提示词的行数乱猜安全。
    return "canned", build_canned(SET.get("canned", "legacy"), extract_src(last_user_content(payload)))



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
        # 只读统计：矩阵用它证明「补翻只打了一次，没变成反复重写」，
        # 也证明「canned 那一枪此刻真按我要的档位作答」（set 回显就是这一腿）。
        # run 回的是本进程启动时拿到的标识（见 RUN_TAG）——就绪探针要认「应答的是本次起的这台桩」，
        # 只看"有人应答"会把上一轮残留的旧桩当自己起的那个，计数口径就错了（端口抢占形态真踩过）。
        if self.path.startswith("/uat/stats"):
            self._send({"ok": True, "run": RUN_TAG, "mode": MODE, "set": dict(SET), **STATE})
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
        # ★ 0AR 第 4 波控制口：只接受 canned／gen 两个键，其余键忽略（不许把这里做成万能后门——
        # 桩能改的东西越多，"绿的是桩不是产品"的风险越大）。
        if self.path.startswith("/uat/set"):
            for k in ("canned", "gen"):
                v = payload.get(k)
                if isinstance(v, str) and v.strip():
                    SET[k] = v.strip()
            self._send({"ok": True, "run": RUN_TAG, "set": dict(SET)})
            return
        kind, content = classify(payload)
        STATE["calls"] += 1
        STATE[kind] += 1
        # 最后一枪的 model 名（㊷①「canned 用独立快模型」那条腿的唯一读法：两侧共用一个桩，
        # 只能各记各的——按调用次序推是推不出来的，greet 会把两枪一起打满）。
        if kind in ("canned", "gen"):
            STATE[kind + "_model"] = str(payload.get("model", ""))
        if not self.path.startswith("/v1/chat/completions"):
            self._send({"error": "unsupported path: " + self.path}, 404)
            return
        # 档位 http500：canned 那一枪直接退 500——这是 ㊷ 退避窗口那条腿的扳机
        # （同步腿失败 → 请求链之外补一枪 → 后台腿也失败 ⇒ 开窗口；下一位访客在窗口内**一票都不许打**）。
        if kind == "canned" and SET.get("canned") == "http500":
            self._send({"error": "uat forced upstream failure"}, 500)
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
