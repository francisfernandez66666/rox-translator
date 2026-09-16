// ============ 本文件职责中文说明 ============
// USDT 收款纯逻辑单元测试：汇率换算与舍入、micro 格式化、地址/交易哈希校验、
// 链名归一、TronGrid 与 JSON-RPC(EVM) 两类入账拉取器的响应解析（httptest mock）。
// =============================================
package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCalcBaseMicro(t *testing.T) {
	// ¥8.97（897 分）@ ¥7.2/USDT（720 分）→ 1.245833 USDT = 1245833 micro（四舍五入 .33↓）
	got, err := CalcBaseMicro(897, 720)
	if err != nil || got != 1_245_833 {
		t.Fatalf("897@720 应得 1245833，实得 %d err=%v", got, err)
	}
	// 进位边界：1000000000 micro = ¥7.2/1e6...：fen=5 rate=720 → 5e6/720=6944.44→6944
	if v, _ := CalcBaseMicro(5, 720); v != 6944 {
		t.Fatalf("5@720 应得 6944，实得 %d", v)
	}
	if _, err := CalcBaseMicro(0, 720); err == nil {
		t.Fatal("金额 0 应报错")
	}
	if _, err := CalcBaseMicro(100, 0); err == nil {
		t.Fatal("汇率未配置应报错")
	}
}

func TestFormatUSDTMicro(t *testing.T) {
	cases := map[int64]string{
		1_245_833: "1.245833",
		1_000_000: "1",
		500_000:   "0.5",
		1:         "0.000001",
		0:         "0",
	}
	for in, want := range cases {
		if got := FormatUSDTMicro(in); got != want {
			t.Fatalf("FormatUSDTMicro(%d)=%s 期望 %s", in, got, want)
		}
	}
}

func TestUSDTValidators(t *testing.T) {
	// 合法 TRON 地址（T + 33 位 base58）
	goodTron := "T" + strings.Repeat("a", 33)
	if !ValidUSDTAddress("trc20", goodTron) {
		t.Fatalf("TRC20 地址应合法: %s", goodTron)
	}
	if ValidUSDTAddress("trc20", "T0"+strings.Repeat("a", 32)) { // '0' 不在 base58
		t.Fatal("含 0 的 base58 应拒绝")
	}
	if !ValidUSDTAddress("erc20", "0xdAC17F958D2ee523a2206206994597C13D831ec7") {
		t.Fatal("ERC20 地址应合法")
	}
	if ValidUSDTAddress("bep20", "0x123") {
		t.Fatal("短 EVM 地址应拒绝")
	}
	if !ValidUSDTTxHash("trc20", strings.Repeat("a", 64)) || ValidUSDTTxHash("trc20", "0x"+strings.Repeat("a", 64)) {
		t.Fatal("TRON txid 规则错误")
	}
	if !ValidUSDTTxHash("erc20", "0x"+strings.Repeat("Ab34", 16)) || ValidUSDTTxHash("bep20", strings.Repeat("b", 64)) {
		t.Fatal("EVM txid 规则错误")
	}
	if NormalizeChain("TRON") != "trc20" || NormalizeChain("eth") != "erc20" || NormalizeChain("bnb") != "bep20" || NormalizeChain("doge") != "" {
		t.Fatal("链名归一错误")
	}
	if ExplorerURL("trc20", "abc") != "https://tronscan.org/#/transaction/abc" {
		t.Fatal("浏览器链接错误")
	}
}

func TestTronFetcherParsesOfficialAndMockShapes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/v1/blocks"):
			_, _ = w.Write([]byte(`{"data":[{"block":100}]}`))
		case strings.Contains(r.URL.Path, "/transactions"):
			// 官方形态 + mock 简形态混发，验证双解析
			_, _ = w.Write([]byte(`{
				"transactions":[{"tx_id":"` + strings.Repeat("c", 64) + `","block_number":90,
					"token_transfer_info":{"value":"1245833","to":"Taddr","from":"Tfrom"}}],
				"transfers":[{"tx_id":"` + strings.Repeat("d", 64) + `","block_number":91,"from":"Tf2","to":"Taddr","value":"2000000"}]}`))
		default:
			http.Error(w, "bad", 400)
		}
	}))
	defer srv.Close()
	f := &TronFetcher{Base: srv.URL}
	deps, newest, err := f.FetchDeposits(context.Background(), "Taddr", 80)
	if err != nil {
		t.Fatal(err)
	}
	if newest != 100 || len(deps) != 2 {
		t.Fatalf("newest=%d deps=%d", newest, len(deps))
	}
	if deps[0].AmountMicro != 1245833 || deps[0].Chain != "trc20" || deps[0].Confirmations() != 11 {
		t.Fatalf("官方形态解析错误: %+v", deps[0])
	}
	if deps[1].TxHash != strings.Repeat("d", 64) || deps[1].AmountMicro != 2000000 {
		t.Fatalf("mock 形态解析错误: %+v", deps[1])
	}
}

func TestEVMFetcherParsesLogs(t *testing.T) {
	addr := "0x1111111111111111111111111111111111111111"
	amt, _ := new(big.Int).SetString("1245833000000000000", 10) // 18 位：1245833 micro = 1.245833e18 wei
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch req.Method {
		case "eth_blockNumber":
			_ = json.NewEncoder(w).Encode(map[string]any{"result": "0x64"}) // 100
		case "eth_getLogs":
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{
				"address":         "0xdAC17F958D2ee523a2206206994597C13D831ec7",
				"topics":          []string{"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef", "0x000000000000000000000000ababababababababababababababababababab", "0x000000000000000000000000" + addr[2:]},
				"data":            "0x" + fmt.Sprintf("%064x", amt),
				"blockNumber":     "0x5a", // 90
				"transactionHash": "0x" + strings.Repeat("e", 64),
				"logIndex":        "0x3",
			}}})
		}
	}))
	defer srv.Close()
	f := &EVMFetcher{RPC: srv.URL, Contract: "0xdAC17F958D2ee523a2206206994597C13D831ec7", Chain: "erc20"}
	deps, newest, err := f.FetchDeposits(context.Background(), addr, 80)
	if err != nil {
		t.Fatal(err)
	}
	if newest != 100 || len(deps) != 1 {
		t.Fatalf("newest=%d deps=%d", newest, len(deps))
	}
	d := deps[0]
	if d.AmountMicro != 1245833 || d.LogIndex != 3 || d.BlockNo != 90 || d.Chain != "erc20" || d.Confirmations() != 11 {
		t.Fatalf("EVM 解析错误: %+v", d)
	}
	if !strings.HasPrefix(ExplorerURL(d.Chain, d.TxHash), "https://etherscan.io/tx/") {
		t.Fatal("浏览器链接错误")
	}
}
