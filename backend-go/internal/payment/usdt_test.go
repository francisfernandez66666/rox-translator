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
		// ★ 只认 /latest 这一条路径（HasSuffix 而非 Contains）：⑮ 的旧 stub 用 Contains("/v1/blocks")，
		//   把裸打错端点也算"命中"，于是这条用例对死腿完全无感。裸 /v1/blocks 落 default 回 400 ⇒ 当场红。
		case strings.HasSuffix(r.URL.Path, "/v1/blocks/latest"):
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

// TestTronHeadUsesLatestEndpoint ⑮ 端点腿：链头必须打 GET {base}/v1/blocks/latest。
// 假上游**只**在 /latest 上应答，裸 /v1/blocks 照真 TronGrid 回 404（该集合端点要求
// limit/order_by 参数）⇒ 端点写回旧的裸 /v1/blocks 时，本用例当场红（反证口径）。
// 同时锁"取到的第一个路径就是 /latest"：只看 newest 非零会被 default 分支的兜底应答蒙过。
func TestTronHeadUsesLatestEndpoint(t *testing.T) {
	var hit []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = append(hit, r.URL.Path)
		switch r.URL.Path {
		case "/v1/blocks/latest":
			_, _ = w.Write([]byte(`{"block_header":{"number":123}}`))
		case "/v1/accounts/Taddr/transactions":
			_, _ = w.Write([]byte(`{"transfers":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":404,"error":"Not Found","path":"/v1/blocks"}`))
		}
	}))
	defer srv.Close()
	f := &TronFetcher{Base: srv.URL}
	deps, newest, err := f.FetchDeposits(context.Background(), "Taddr", 100)
	if err != nil {
		t.Fatalf("链头走 /latest 应成功，实得 err=%v（命中路径 %v）", err, hit)
	}
	if newest != 123 {
		t.Fatalf("链头高度应为 123，实得 %d", newest)
	}
	if len(deps) != 0 {
		t.Fatalf("本轮无转入，deps 应为空，实得 %d 笔", len(deps))
	}
	if len(hit) == 0 || hit[0] != "/v1/blocks/latest" {
		t.Fatalf("第一个上游请求必须是 /v1/blocks/latest，实际命中 %v", hit)
	}
}

// TestTronHeadPathEnvOverride USDT_TRON_HEAD_PATH 覆盖腿：上游改路径时不改码顶住。
// 判据＝换档后打的确实是覆盖路径（不是默认 /latest），且高度照读得出。
func TestTronHeadPathEnvOverride(t *testing.T) {
	t.Setenv("USDT_TRON_HEAD_PATH", "api/chain-head") // 刻意不带前导斜杠：覆盖腿要自己补齐
	var hit []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = append(hit, r.URL.Path)
		if r.URL.Path == "/api/chain-head" {
			_, _ = w.Write([]byte(`{"block_header":{"number":55}}`))
			return
		}
		if r.URL.Path == "/v1/accounts/Taddr/transactions" {
			_, _ = w.Write([]byte(`{"transfers":[]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	f := &TronFetcher{Base: srv.URL}
	_, newest, err := f.FetchDeposits(context.Background(), "Taddr", 10)
	if err != nil || newest != 55 {
		t.Fatalf("覆盖路径应生效并读到高度：newest=%d err=%v 命中 %v", newest, err, hit)
	}
	for _, p := range hit {
		if p == "/v1/blocks/latest" {
			t.Fatalf("设了覆盖口还打默认 /latest，命中 %v", hit)
		}
	}
}

// TestTronHeadAcceptsThreeShapes ⑮ 结构腿：三种真实响应形态都必须读出同一个高度。
// 只认后两种（block_number／data[].block）是旧缺陷的第二条根因——光换端点不换结构体，
// /latest 的 block_header.number 照样读 0，而 newest=0 会让确认数永不自达标。
func TestTronHeadAcceptsThreeShapes(t *testing.T) {
	cases := []struct{ name, body string }{
		{"latest/block_header", `{"block_header":{"number":7}}`},
		{"mock/顶层 block_number", `{"block_number":7}`},
		{"列表 data[0].block", `{"data":[{"block":7}]}`},
		{"优先级取第一腿", `{"block_header":{"number":7},"block_number":9,"data":[{"block":11}]}`},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/v1/blocks/latest") {
				_, _ = w.Write([]byte(c.body))
				return
			}
			_, _ = w.Write([]byte(`{"transfers":[]}`))
		}))
		f := &TronFetcher{Base: srv.URL}
		_, newest, err := f.FetchDeposits(context.Background(), "Taddr", 1)
		srv.Close()
		if err != nil {
			t.Fatalf("%s: 应解析成功，err=%v", c.name, err)
		}
		if newest != 7 {
			t.Fatalf("%s: 链头应读 7，实得 %d", c.name, newest)
		}
	}
}

// TestTronHeadEmptyReadingFailsLoudly 端点通了但三形态都读不到高度 ⇒ **必须显式报错**，
// 不许带 newest=0 往下走。旧形态带 0 返回＝"这一轮扫过了"，日志一行不剩，
// 而 newest-block+1 恒小于确认阈值 ⇒ 客户转账永远不会入账（⑮ 的静默形态）。
func TestTronHeadEmptyReadingFailsLoudly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true}`)) // 合法 JSON、零高度字段
	}))
	defer srv.Close()
	f := &TronFetcher{Base: srv.URL}
	if _, _, err := f.FetchDeposits(context.Background(), "Taddr", 1); err == nil {
		t.Fatal("链头读数为空必须报错，实得 nil")
	} else if !strings.Contains(err.Error(), "链头读数取不到") {
		t.Fatalf("报错文案要点名链头读数，实得: %v", err)
	}
}

// TestEVMHeadEmptyReadingFailsLoudly 同族判据的 EVM 腿：eth_blockNumber 回空/非正高度
// 必须报错，不许带 0 继续（旧形态静默把"读不到"当"链还没长"）。
func TestEVMHeadEmptyReadingFailsLoudly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Method == "eth_blockNumber" {
			_, _ = w.Write([]byte(`{"result":""}`))
			return
		}
		_, _ = w.Write([]byte(`{"result":[]}`))
	}))
	defer srv.Close()
	f := &EVMFetcher{RPC: srv.URL, Contract: "0xdAC17F958D2ee523a2206206994597C13D831ec7", Chain: "erc20"}
	if _, _, err := f.FetchDeposits(context.Background(), "0x1111111111111111111111111111111111111111", 1); err == nil {
		t.Fatal("eth_blockNumber 回空必须报错，实得 nil")
	} else if !strings.Contains(err.Error(), "链头读数取不到") {
		t.Fatalf("报错文案要点名链头读数，实得: %v", err)
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
