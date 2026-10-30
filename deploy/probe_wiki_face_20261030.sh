#!/usr/bin/env bash
# ============================================================================
# deploy/probe_wiki_face_20261030.sh —— 现网服务器上量「维基 langlinks 这一腿」到底通不通
#
# 起因：领域词表按租户开（tenant 1）后手拨判域，日志出
#   chain=instant reason=timeout（前台 600ms 预算，设计内）
#   chain=instant cache=warm reason=no_wiki latency_ms=1900（后台预热真拨了模型）
# ——no_wiki 在 domain.detect.go 里只有三种落点：一个目标语标题都没取回、
#    取回的条数不到该链门槛、或 codeOf 为空。三种都指向维基那一腿，
#    但**分不出**是「这台机器够不着 zh.wikipedia」「800ms 单词超时太紧」
#    还是「模型给的是界面用语、维基根本没有这个词条」。
#
# 只做三件事，全部只读、零写库、零客户数据：
#   W1 连通性与耗时（一个必有词条，量三次）
#   W2 判域样本里真实可能出现的词的**命中率**（有词条/有 en 标题）
#   W3 同词打 en.wikipedia 的对照（证明不是「词本身不存在」）
# 判绿只认末行 WIKIFACE_ALL_OK=1（它只表示「量到了」，不表示这一腿是好的——
# 好坏由 W2 的命中读数自己说）。
# ============================================================================
set -uo pipefail

API_ZH='https://zh.wikipedia.org/w/api.php'
# W1 的必有词条（金融核心词，中文维基一定有条目且有 interwiki）
BASE_TERM='证券'
# W2 的词表：左列＝① 判域样本里逐字出现的短语，右列＝② 同一份样本里更「像词条」的短词。
# 分成两列是为了把「模型挑词的口径」与「维基有没有条目」这两件事分开量——
# 只量一列就会把「挑了界面用语」读成「维基不可达」。
LONG_TERMS='总盈亏|成交笔数|买入额|卖出额|浮动盈亏|自动研究页'
SHORT_TERMS='证券|账户|盈亏|成交|仓位|因子|形态|信号|模拟盘|现金流'

ok=1
hits=0
misses=0

# zh_fetch 取一个词的 en 标题；输出 <rc> <http码> <耗时ms> <en标题数> <全部语种数> <体首20字>
# ★ 必须带 http 码与体首：只数 langlink 时「403 被挡」与「条目不存在」都是 0，读数分不开
#   （这正是 §三 那条「判据分不出两件事」）。
zh_fetch() {
  local term="$1"
  local t0 t1 body rc code secs n_en n_all snip
  t0=$(date +%s%N 2>/dev/null || echo 0)
  body=$(curl -s --max-time 3 -G "$API_ZH" \
    --data-urlencode 'action=query' --data-urlencode 'prop=langlinks' \
    --data-urlencode "titles=${term}" --data-urlencode 'lllimit=max' \
    --data-urlencode 'format=json' --data-urlencode 'redirects=1' \
    -A 'LangCrossOps/1.0 (https://langcross.lexicorn.cn; ops@lexicorn.cn)' \
    -w '\n__CODE__%{http_code}')
  rc=$?
  t1=$(date +%s%N 2>/dev/null || echo 0)
  secs=$(awk -v a="$t0" -v b="$t1" 'BEGIN{printf "%.0f",(b-a)/1000000}')
  code=$(printf '%s' "$body" | sed -n 's/^__CODE__//p' | tail -1)
  body=$(printf '%s' "$body" | sed '/^__CODE__/d')
  n_en=$(printf '%s' "$body" | grep -o '"lang":"en"' | wc -l | tr -d ' ')
  n_all=$(printf '%s' "$body" | grep -o '"lang":"' | wc -l | tr -d ' ')
  snip=$(printf '%s' "$body" | tr -d '\n\r' | cut -c1-120)
  echo "${rc} ${code:-000} $secs $n_en $n_all $snip"
}

echo "----- W1 连通性（词条「${BASE_TERM}」，量三次；3s 上限只是探针的，产品侧是 800ms）-----"
for i in 1 2 3; do
  read -r rc code ms nen nall snip <<EOF
$(zh_fetch "${BASE_TERM}")
EOF
  if [ "${rc}" != "0" ]; then
    echo "BAD  第 $i 次 curl 退 ${rc}（链路/域名问题，不是词条问题）"
    ok=0
  elif [ "${nall}" -lt 1 ]; then
    echo "BAD  第 $i 次 http=${code} ${ms}ms 零 langlink；体首=${snip}"
    ok=0
  else
    echo "OK   第 $i 次 http=${code} ${ms}ms，langlink ${nall} 条（en $nen 条）"
  fi
done

echo "----- W2 模型候选词的维基命中率 -----"
for term in $(echo "$LONG_TERMS" | tr '|' ' '); do
  read -r rc code ms nen nall snip <<EOF
$(zh_fetch "${term}")
EOF
  if [ "${rc}" = "0" ] && [ "$nen" -ge 1 ]; then
    echo "HIT  「${term}」 http=${code} ${ms}ms → en 标题在位（langlink ${nall}）"
    hits=$((hits + 1))
  else
    echo "MISS 「${term}」 rc=${rc} http=${code} ${ms}ms en=$nen all=${nall} 体首=${snip}"
    misses=$((misses + 1))
  fi
done
echo "-- 短词档 --"
for term in $(echo "$SHORT_TERMS" | tr '|' ' '); do
  read -r rc code ms nen nall snip <<EOF
$(zh_fetch "${term}")
EOF
  if [ "${rc}" = "0" ] && [ "$nen" -ge 1 ]; then
    echo "HIT  「${term}」 http=${code} ${ms}ms → en 标题在位（langlink ${nall}）"
    hits=$((hits + 1))
  else
    echo "MISS 「${term}」 rc=${rc} http=${code} ${ms}ms en=$nen all=${nall} 体首=${snip}"
    misses=$((misses + 1))
  fi
done

echo "----- W3 对照：英文维基打同一个词（问「条目不存在」还是「跨语标题取不到」）-----"
for term in 总盈亏 成交笔数 浮动盈亏; do
  out=$(curl -s --max-time 3 -G 'https://en.wikipedia.org/w/api.php' \
    --data-urlencode 'action=query' --data-urlencode 'prop=langlinks' \
    --data-urlencode "titles=${term}" --data-urlencode 'format=json' --data-urlencode 'redirects=1' \
    -A 'LangCrossOps/1.0 (https://langcross.lexicorn.cn; ops@lexicorn.cn)' \
    -w '\n__CODE__%{http_code}')
  code=$(printf '%s' "$out" | sed -n 's/^__CODE__//p' | tail -1)
  body=$(printf '%s' "$out" | sed '/^__CODE__/d')
  n=$(printf '%s' "$body" | grep -o '"lang":"zh"' | wc -l | tr -d ' ')
  echo "  en 侧「${term}」http=${code:-000} zh-langlink 回=$n 体首=$(printf '%s' "$body" | tr -d '\n\r' | cut -c1-120)"
done

echo "----- W4 同一请求换 User-Agent（产品侧 domain/wiki.go 的 http.NewRequest 不带 UA ⇒ Go 默认 Go-http-client/1.1）-----"
for ua in 'Go-http-client/1.1' 'langcross-probe/1.0 (ops read-only diagnosis)' 'LangCrossOps/1.0 (https://langcross.lexicorn.cn; ops@lexicorn.cn)'; do
  out=$(curl -s --max-time 3 -G "$API_ZH" \
    --data-urlencode 'action=query' --data-urlencode 'prop=langlinks' \
    --data-urlencode "titles=${BASE_TERM}" --data-urlencode 'lllimit=max' \
    --data-urlencode 'format=json' --data-urlencode 'redirects=1' \
    -A "$ua" -w '\n__CODE__%{http_code}')
  code=$(printf '%s' "$out" | sed -n 's/^__CODE__//p' | tail -1)
  body=$(printf '%s' "$out" | sed '/^__CODE__/d')
  n_all=$(printf '%s' "$body" | grep -o '"lang":"' | wc -l | tr -d ' ')
  echo "  UA=[${ua}] http=${code:-000} langlink=$n_all 体首=$(printf '%s' "$body" | tr -d '\n\r' | cut -c1-90)"
done

echo "----- 读数汇总 -----"
echo "W2_HITS=$hits W2_MISS=$misses"
if [ "$ok" = "1" ]; then
  echo "WIKIFACE_ALL_OK=1"
  exit 0
fi
echo "WIKIFACE_BAD=$ok"
exit 1
