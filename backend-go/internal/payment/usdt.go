// ============ 本文件职责中文说明 ============
// USDT（Tether）收款纯逻辑层：
//   - 金额换算：订单人民币分（amount_money 派生）→ USDT 最小单位 micro（1e-6），
//     含 6 位小数「尾数」（tail）分配语义——无 memo 的链上转账靠金额唯一性对单；
//   - 格式校验：各链收款地址与交易哈希的入口级校验（txid 仅展示与审计，
//     到账判定只认服务端链上查询结果）；
//   - 链上入账拉取器（fetcher）：TRON（TRC20，TronGrid 兼容 API）与
//     EVM（ERC20/BEP20，JSON-RPC eth_getLogs）两类适配器，归一化为 Deposit 结构。
//     BaseURL 全部可注入（mock_chain 测试/自建网关），超时 10s。
//
// ★ 生产接入注意：TronGrid/Etherscan 生产响应结构以 M2 上线前真链冒烟为准
//
//	（自动对账默认关闭 usdt_auto_settle=0，冒烟通过前不会产生任何自动入账）。
//
// =============================================
package payment

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// USDTMicroPerUnit 1 USDT = 1e6 micro（TRC20 固定 6 位小数；EVM 18 位在此归一为 6 位）。
const USDTMicroPerUnit = 1_000_000

// TailMin/TailMax 尾数范围（micro）：0.000001–0.009999 USDT，用于同址 pending 单金额唯一化。
const (
	TailMin = 1
	TailMax = 9999
)

// tronTransferDecimal 以太坊 USDT 合约常见精度 18 位（10^12 wei = 1 micro USDT）。
var evmWeiPerMicro = big.NewInt(1_000_000_000_000)

// usdtTransferTopic0 ERC20 Transfer(address,address,uint256) 事件主题哈希。
const usdtTransferTopic0 = "ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

// CalcBaseMicro 人民币分 → USDT micro 基础额（不含尾数）：micro = fen×1e6 / rateFenPerUSDT，
// 四舍五入。rateFenPerUSDT 为「分/USDT」整型汇率（如 ¥7.2 = 720）。
func CalcBaseMicro(fen, rateFenPerUSDT int64) (int64, error) {
	if fen <= 0 {
		return 0, fmt.Errorf("订单金额非法: %d 分", fen)
	}
	if rateFenPerUSDT <= 0 {
		return 0, fmt.Errorf("USDT 汇率未配置（usdt_rate_fen_per_usdt，分/USDT）")
	}
	q := new(big.Int).SetInt64(fen * USDTMicroPerUnit)
	r := new(big.Int).SetInt64(rateFenPerUSDT)
	// 四舍五入：(2q + r) / 2r
	q.Mul(q, big.NewInt(2)).Add(q, r).Div(q, new(big.Int).Mul(r, big.NewInt(2)))
	if !q.IsInt64() || q.Sign() <= 0 {
		return 0, fmt.Errorf("USDT 金额换算溢出或为零")
	}
	return q.Int64(), nil
}

// FormatUSDTMicro micro → 6 位小数字符串（去尾零，供前端展示与复制）。
func FormatUSDTMicro(micro int64) string {
	sign := ""
	if micro < 0 {
		sign, micro = "-", -micro
	}
	s := fmt.Sprintf("%d.%06d", micro/USDTMicroPerUnit, micro%USDTMicroPerUnit)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" {
		s = "0"
	}
	return sign + s
}

// ============ 格式校验 ============

var (
	reTronAddr = regexp.MustCompile(`^T[1-9A-HJ-NP-Za-km-z]{33}$`) // TRON base58 地址（34 位，无 0/O/I/l）
	reEVMAddr  = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)         // EVM 地址
	reTronTx   = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)           // TRON 交易哈希（无 0x 前缀）
	reEVMTx    = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)         // EVM 交易哈希
	reLogIdx   = regexp.MustCompile(`^[0-9a-fA-F]{1,16}$`)
)

// ValidUSDTAddress 按链校验收款地址格式。chain: trc20|erc20|bep20。
func ValidUSDTAddress(chain, addr string) bool {
	switch chain {
	case "trc20":
		return reTronAddr.MatchString(addr)
	case "erc20", "bep20":
		return reEVMAddr.MatchString(addr)
	}
	return false
}

// ValidUSDTTxHash 按链校验交易哈希格式（客户声明/后台确认入口级校验，不代表链上真实）。
func ValidUSDTTxHash(chain, hash string) bool {
	switch chain {
	case "trc20":
		return reTronTx.MatchString(hash)
	case "erc20", "bep20":
		return reEVMTx.MatchString(hash)
	}
	return false
}

// NormalizeChain 链名归一（tron→trc20，eth/ethereum→erc20，bsc/bnb→bep20）；未识别返回空。
func NormalizeChain(c string) string {
	switch strings.ToLower(strings.TrimSpace(c)) {
	case "trc20", "tron":
		return "trc20"
	case "erc20", "eth", "ethereum":
		return "erc20"
	case "bep20", "bsc", "bnb":
		return "bep20"
	}
	return ""
}

// ExplorerURL 交易哈希的公开浏览器外链（人工核对与前端展示用）。
func ExplorerURL(chain, hash string) string {
	switch chain {
	case "trc20":
		return "https://tronscan.org/#/transaction/" + hash
	case "erc20":
		return "https://etherscan.io/tx/" + hash
	case "bep20":
		return "https://bscscan.com/tx/" + hash
	}
	return ""
}

// ============ 链上入账拉取（fetcher） ============

// Deposit 归一化的链上 USDT 转入事件（reconciler 落库单元）。
type Deposit struct {
	Chain         string // trc20|erc20|bep20
	TxHash        string
	LogIndex      int // EVM 事件序号；TRON 恒 0
	FromAddr      string
	AmountMicro   int64 // 已归一到 1e-6 单位
	BlockNo       int64 // 所在区块高度
	NewestBlockNo int64 // 拉取时的链头高度（confirmations = Newest - Block + 1）
}

// DepositFetcher 链上入账拉取器接口（TRON/EVM 适配器实现；mock_chain 经 BaseURL 注入）。
type DepositFetcher interface {
	FetchDeposits(ctx context.Context, addr string, sinceBlock int64) ([]Deposit, int64, error)
}

// Confirmations 已确认数（至少 1：入块即 1）。
func (d Deposit) Confirmations() int64 {
	c := d.NewestBlockNo - d.BlockNo + 1
	if c < 1 {
		c = 1
	}
	return c
}

// httpDo JSON 请求工具（GET 或 POST，10s 超时，Bearer/头由调用方拼）。
func httpJSON(ctx context.Context, method, url string, body []byte, headers map[string]string, out interface{}) error {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(cctx, method, url, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("上游 %s 返回 %d: %s", url, resp.StatusCode, string(b))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// TronFetcher TRC20 入账拉取器（TronGrid 兼容 API；BaseURL 可注入 mock/自建网关）。
type TronFetcher struct {
	Base   string // 如 https://api.trongrid.io（env USDT_TRON_BASE 覆盖）
	APIKey string // env TRONGRID_API_KEY
}

// tronTxResp TronGrid /v1/accounts/{addr}/transactions 归一解析结构。
// 兼容两种响应形态：官方（transactions[].ret/token_transfer_info）与 mock_chain（transfers[]）。
type tronTxResp struct {
	Transactions []struct {
		TxID         string `json:"tx_id"`
		TxID2        string `json:"txID"`
		BlockNumber  int64  `json:"block_number"`
		TokenTranser struct {
			Value string `json:"value"`
			To    string `json:"to"`
			From  string `json:"from"`
		} `json:"token_transfer_info"`
	} `json:"transactions"`
	Transfers []struct {
		TxID        string `json:"tx_id"`
		BlockNumber int64  `json:"block_number"`
		From        string `json:"from"`
		To          string `json:"to"`
		Value       string `json:"value"`
	} `json:"transfers"`
}

// jsonInt64 兼容「数字」与「带引号的字符串」两种 JSON 形态的整数读法。
// ★ ㊾（2026-10-08）为什么需要它：真上游 `POST /wallet/getnowblock` 的高度在嵌套的
//
//	`block_header.raw_data.number` 那一层，而 wallet 这一族是从 protobuf 直接转 JSON 的，
//	数字字段**可能是 86875xxxx、也可能是 "86875xxxx"**（同族字段如 raw_data.timestamp 就有带引号的形态）。
//	若按 `int64` 硬解，带引号那档会在 Unmarshal 阶段整条报类型错 ⇒ 链头读失败，
//	表现和端点写死成一条不存在的路一模一样：监听恒 failing、钱到了也不入账，而日志只说"查询失败"。
//	读不出来时保持 0——**取不到值与取到 0 由 tronNewestBlock 之上那条"三腿皆空即显式报错"统一处理**，
//	这里不吞错误也不造假值。
type jsonInt64 int64

// UnmarshalJSON 实现按需容错：合法数字／带引号数字都收，其余形态留 0。
func (j *jsonInt64) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), "\"")
	if s == "" || s == "null" {
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		// 认不出来的形态（对象、科学计数、上游换字段类型）不当成错误上抛：
		// 这一腿的判据是"四形态里有没有一条读到正高度"，单字段解析失败应当**落到下一腿**，
		// 而不是让整次链头查询在 Unmarshal 处炸掉（那会把"还能读第 2 腿"的可用上游打成失败）。
		return nil
	}
	*j = jsonInt64(n)
	return nil
}

// tronBlockResp 链头高度。上游四种真实形态都要认（优先级见 tronNewestBlock）：
//   - ★ ㊾ 真上游 `POST /wallet/getnowblock`：{"block_header":{"raw_data":{"number":123}}}
//     （2026-10-06 本机独立网络实测：这一族里只有它在，四条 GET 全 404）
//   - /v1/blocks/latest 官方形态：{"block_header":{"number":123}}
//   - /v1/blocks?limit=1&order_by=… 列表形态：{"data":[{"block":123}]}
//   - mock_chain（UAT）顶层形态：{"block_number":123}
//
// ★ 只认后三种是 ⑮ 的一条根因：光换端点不换结构体，链头照样读 0；
//
//	而第 3 波那次换的 `/v1/blocks/latest` **本身就是一条不存在的路**（㊾ 实测）——
//	两条腿必须一起动，只动其一现网仍恒 404／恒读 0。
type tronBlockResp struct {
	Data []struct {
		Block int64 `json:"block"`
	} `json:"data"`
	BlockNumber int64 `json:"block_number"`
	BlockHeader struct {
		Number  int64 `json:"number"`
		RawData struct {
			Number jsonInt64 `json:"number"`
		} `json:"raw_data"`
	} `json:"block_header"`
}

// tronNewestBlock 按「raw_data.number ＞ block_header.number ＞ block_number ＞ data[0].block」取链头高度。
// 四腿都留着是刻意的：上游换形态时读 0 会让确认数永远不达标（钱到了也不入账），
// 而这一条腿一旦静默，界面上照样向客户承诺「达到确认数后自动入账」。
// 顺序按"真上游当前形态优先"排：真件里 raw_data.number 与 block_header.number 不会同时出现，
// 但**同时出现时问 raw_data**才是问那台机器现在真正在用的字段（㊾ 实测形态），
// 反过来会让"上游把 /v1 那族补回来"那一天悄悄读到一个过时的外层高度。
func tronNewestBlock(r tronBlockResp) int64 {
	if int64(r.BlockHeader.RawData.Number) > 0 {
		return int64(r.BlockHeader.RawData.Number)
	}
	if r.BlockHeader.Number > 0 {
		return r.BlockHeader.Number
	}
	if r.BlockNumber > 0 {
		return r.BlockNumber
	}
	if len(r.Data) > 0 {
		return r.Data[0].Block
	}
	return 0
}

// 链头查询的默认端点（★ ㊾ 2026-10-08 定档）：真上游只认
//
//	POST {base}/wallet/getnowblock，高度在 block_header.raw_data.number。
//	旧默认 /v1/blocks/latest 是 2026-10-05 第 3 波按「裸 /v1/blocks 恒 404」推出来的另一条不存在的路
//	（㊾ 本机独立网络实测：/v1/blocks/latest、/v1/blocks?limit=1、/v1/blocks/1000、/v1/statistics 四条全 404），
//	于是现网每 30 s 失败一次、每昼夜约 2,880 条失败日志、监听恒 failing、收银台恒降级人工核销。
const (
	tronHeadDefaultPath    = "/wallet/getnowblock"
	tronHeadDefaultMethod  = http.MethodPost
	tronHeadWalletBodyJSON = "{}"
)

// tronHeadRequest 链头查询的**方法＋路径＋请求体**三元组（★ ㊾ 的修法本体）。
//
// 为什么不再是 tronHeadURL 只给路径：上游那一族是 wallet API，**GET 同一路径也回 404**，
// 旧那个 `USDT_TRON_HEAD_PATH` 覆盖口"只重写路径、不改方法也不改解析"，
// 运营侧调档绕不过去（㊾ 现网实证）。所以覆盖口拆成两把、彼此**不做派生推断**：
//   - USDT_TRON_HEAD_PATH：路径（缺省 /wallet/getnowblock）；
//   - USDT_TRON_HEAD_METHOD：方法（缺省 POST）——"这条路径该用 GET 还是 POST"是上游契约，
//     禁止按路径前缀猜（猜错的表现就是又造一条死腿，同 ⑮/㊾ 两次的同形死法）。
//
// 请求体：POST 时固定发 `{}`（wallet 那一族接受空体）；GET 时不带体。
func tronHeadRequest(base string) (method, url string, body []byte) {
	p := strings.TrimRight(os.Getenv("USDT_TRON_HEAD_PATH"), "/")
	if p == "" {
		p = tronHeadDefaultPath
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	method = strings.ToUpper(strings.TrimSpace(os.Getenv("USDT_TRON_HEAD_METHOD")))
	if method == "" {
		method = tronHeadDefaultMethod
	}
	if method == http.MethodGet {
		return method, strings.TrimRight(base, "/") + p, nil
	}
	return method, strings.TrimRight(base, "/") + p, []byte(tronHeadWalletBodyJSON)
}

// FetchDeposits 拉取指定地址自 sinceBlock（不含）以来的 TRC20-USDT 转入。
func (f *TronFetcher) FetchDeposits(ctx context.Context, addr string, sinceBlock int64) ([]Deposit, int64, error) {
	base := f.Base
	if base == "" {
		return nil, 0, fmt.Errorf("USDT TRON Base 未配置（USDT_TRON_BASE）")
	}
	hdrs := map[string]string{}
	if f.APIKey != "" {
		hdrs["TRON-PRO-API-KEY"] = f.APIKey
	}
	var head tronBlockResp
	// ★ 链头这一腿的 key/URL/体**只有一份来源**＝tronHeadRequest（㊾：方法也是契约的一部分，
	// 过去只有 URL 能被 env 覆盖，运营侧无论怎么调都到不了真上游那一族）
	headMethod, headURL, headBody := tronHeadRequest(base)
	if err := httpJSON(ctx, headMethod, headURL, headBody, hdrs, &head); err != nil {
		return nil, 0, fmt.Errorf("TRON 链头查询失败: %w", err)
	}
	newest := tronNewestBlock(head)
	if newest <= 0 {
		// 端点通了但三形态都没读到高度：显式失败，不许带 0 往下走
		// （0 会让 newest-block+1 恒小于确认阈值 ⇒ 钱到了一辈子不入账，且日志一行不剩）
		return nil, 0, fmt.Errorf("TRON 链头读数取不到（四种响应形态均为空）")
	}
	u := fmt.Sprintf("%s/v1/accounts/%s/transactions?only_confirmed=true&only_to=true&limit=50&contract=TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t&from_block=%d",
		strings.TrimRight(base, "/"), addr, sinceBlock+1)
	var raw tronTxResp
	if err := httpJSON(ctx, http.MethodGet, u, nil, hdrs, &raw); err != nil {
		return nil, 0, fmt.Errorf("TRON 入账查询失败: %w", err)
	}
	var out []Deposit
	parseInt64 := func(s string) int64 { n, _ := strconv.ParseInt(s, 10, 64); return n }
	for _, t := range raw.Transactions {
		txid := t.TxID
		if txid == "" {
			txid = t.TxID2
		}
		v := parseInt64(t.TokenTranser.Value)
		if txid == "" || v <= 0 {
			continue
		}
		out = append(out, Deposit{Chain: "trc20", TxHash: txid, FromAddr: t.TokenTranser.From,
			AmountMicro: v, BlockNo: t.BlockNumber, NewestBlockNo: newest})
	}
	for _, t := range raw.Transfers { // mock_chain 简形态
		v := parseInt64(t.Value)
		if t.TxID == "" || v <= 0 {
			continue
		}
		out = append(out, Deposit{Chain: "trc20", TxHash: t.TxID, FromAddr: t.From,
			AmountMicro: v, BlockNo: t.BlockNumber, NewestBlockNo: newest})
	}
	return out, newest, nil
}

// EVMFetcher ERC20/BEP20 入账拉取器（JSON-RPC eth_getLogs / eth_blockNumber）。
type EVMFetcher struct {
	RPC      string // env USDT_ETH_RPC / USDT_BSC_RPC（可指向 mock_chain 的 /rpc 端点）
	Contract string // 链上 USDT 合约地址（生产默认值见调用方；mock 可注入）
	Chain    string // erc20|bep20
}

// hexInt 解析 16 进制字符串为 int64（兼容 0x/0X 前缀；空串或非法输入按 0 处理）。
// 用于解析链上事件 data 字段中的金额数值。
func hexInt(s string) int64 {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if s == "" {
		return 0
	}
	n, _ := new(big.Int).SetString(s, 16)
	if n == nil {
		return 0
	}
	return n.Int64()
}

// FetchDeposits 拉取指定地址自 sinceBlock（不含）以来的 ERC20/BEP20 USDT Transfer 事件。
func (f *EVMFetcher) FetchDeposits(ctx context.Context, addr string, sinceBlock int64) ([]Deposit, int64, error) {
	if f.RPC == "" {
		return nil, 0, fmt.Errorf("USDT %s RPC 未配置", f.Chain)
	}
	call := func(method string, params []interface{}, out interface{}) error {
		body, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		return httpJSON(ctx, http.MethodPost, f.RPC, body, nil, out)
	}
	var head struct {
		Result string `json:"result"`
	}
	if err := call("eth_blockNumber", []interface{}{}, &head); err != nil {
		return nil, 0, fmt.Errorf("EVM 链头查询失败: %w", err)
	}
	newest := hexInt(head.Result)
	if newest <= 0 {
		// 与 TRON 侧同一把尺子：链头读 0 会让 newest-block+1 永远不足确认数 ⇒ 钱到了不入账且零日志。
		// 这一腿刻意显式失败，让上层监听把自己的存活态翻成 failing（见 api/pay_usdt_watch.go）。
		return nil, 0, fmt.Errorf("EVM %s 链头读数取不到（eth_blockNumber 回空或非正高度）", f.Chain)
	}
	addrLow := strings.ToLower(strings.TrimPrefix(addr, "0x"))
	if len(addrLow) != 40 {
		return nil, 0, fmt.Errorf("EVM 收款地址非法: %s", addr)
	}
	topicTo := "0x000000000000000000000000" + addrLow
	var logs struct {
		Result []struct {
			Address     string   `json:"address"`
			Topics      []string `json:"topics"`
			Data        string   `json:"data"`
			BlockNumber string   `json:"blockNumber"`
			TxHash      string   `json:"transactionHash"`
			LogIndex    string   `json:"logIndex"`
		} `json:"result"`
	}
	params := map[string]interface{}{
		"address":   f.Contract,
		"fromBlock": fmt.Sprintf("0x%x", sinceBlock+1),
		"toBlock":   fmt.Sprintf("0x%x", newest),
		// topics[1]=nil 表示「from 任意」；Go 侧必须用 interface{} 保 null（[]string 会变空串）
		"topics": []interface{}{"0x" + usdtTransferTopic0, nil, topicTo},
	}
	if err := call("eth_getLogs", []interface{}{params}, &logs); err != nil {
		return nil, 0, fmt.Errorf("EVM 入账查询失败: %w", err)
	}
	var out []Deposit
	for _, l := range logs.Result {
		if len(l.Topics) < 3 || l.Data == "" {
			continue
		}
		amtWei := new(big.Int)
		amtWei.SetString(strings.TrimPrefix(l.Data, "0x"), 16)
		micro := new(big.Int).Div(amtWei, evmWeiPerMicro) // 18 位 → 6 位归一（截断，尾数 ≥1e12 wei 可辨）
		if !micro.IsInt64() || micro.Sign() <= 0 {
			continue
		}
		li := int(hexInt(l.LogIndex))
		if !reLogIdx.MatchString(strings.TrimPrefix(l.LogIndex, "0x")) {
			li = 0
		}
		out = append(out, Deposit{Chain: f.Chain, TxHash: l.TxHash, LogIndex: li,
			FromAddr: "0x" + l.Topics[1][len(l.Topics[1])-40:], AmountMicro: micro.Int64(),
			BlockNo: hexInt(l.BlockNumber), NewestBlockNo: newest})
	}
	return out, newest, nil
}

// ValidEVMTopicTo 校验 topic[2] 末 40 位地址（防御异常事件形态）。
func ValidEVMTopicTo(topic string) bool {
	if len(topic) != 66 {
		return false
	}
	_, err := hex.DecodeString(topic[2:])
	return err == nil
}

// 编译期断言：两适配器满足拉取器接口
var (
	_ DepositFetcher = (*TronFetcher)(nil)
	_ DepositFetcher = (*EVMFetcher)(nil)
)
