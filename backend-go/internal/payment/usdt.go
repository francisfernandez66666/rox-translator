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

// tronBlockResp 链头高度（官方 data[0].block；mock 顶层 block_number）。
type tronBlockResp struct {
	Data []struct {
		Block int64 `json:"block"`
	} `json:"data"`
	BlockNumber int64 `json:"block_number"`
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
	if err := httpJSON(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/v1/blocks", nil, hdrs, &head); err != nil {
		return nil, 0, fmt.Errorf("TRON 链头查询失败: %w", err)
	}
	newest := head.BlockNumber
	if newest == 0 && len(head.Data) > 0 {
		newest = head.Data[0].Block
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
