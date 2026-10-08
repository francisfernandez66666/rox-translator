// ============ 本文件职责中文说明 ============
// USDT 收款纯逻辑单元测试：汇率换算与舍入、micro 格式化、地址/交易哈希校验、
// 链名归一、TronGrid 与 JSON-RPC(EVM) 两类入账拉取器的响应解析（httptest mock）。
// =============================================
package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
		// ★ 链头这一族按真上游形态应答（POST /wallet/getnowblock，㊾ 2026-10-08）；
		//   只认这一条路径（HasSuffix 而非 Contains）：⑮ 的旧 stub 用 Contains("/v1/blocks")，
		//   把裸打错端点也算"命中"，于是这条用例对死腿完全无感。打错路径落 default 回 400 ⇒ 当场红。
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/wallet/getnowblock"):
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

// TestTronHeadUsesWalletPostEndpoint ★ ㊾（2026-10-08）端点与方法两条腿一起锁：
// 链头必须打 POST {base}/wallet/getnowblock，高度读嵌套的 block_header.raw_data.number。
//
// 这一条替代的是 ⑮ 那版 `TestTronHeadUsesLatestEndpoint`——它把端点锁在 `/v1/blocks/latest`，
// 而㊾ 用本机独立网络实测证明**那一族四条 GET 全 404**（/v1/blocks/latest、/v1/blocks?limit=1、
// /v1/blocks/1000、/v1/statistics），真上游只剩 `POST /wallet/getnowblock`。
// 假上游照真上游的形态回：GET 同一路径 404、默认那族旧路径 404 ⇒
// 谁把方法写回 GET、或把路径写回 /v1/blocks/latest，本用例当场红（两层反证）。
// 判据取「第一个上游请求的方法＋路径＋请求体」做等值，不看兜底应答：
// 只断言 newest 非零会被 default 分支的随便什么回显蒙过（⑮/㊾ 两批反复点名的假绿形态）。
func TestTronHeadUsesWalletPostEndpoint(t *testing.T) {
	type hitT struct {
		method, path, body string
	}
	var hits []hitT
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		hits = append(hits, hitT{r.Method, r.URL.Path, strings.TrimSpace(string(b))})
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/wallet/getnowblock":
			// 真上游形态（㊾ 实测层级）：高度在嵌套的 raw_data.number
			_, _ = w.Write([]byte(`{"block_header":{"raw_data":{"number":86875123,"timestamp":1762000000000}}}`))
		case r.URL.Path == "/v1/accounts/Taddr/transactions":
			_, _ = w.Write([]byte(`{"transfers":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":404,"error":"Not Found"}`))
		}
	}))
	defer srv.Close()
	f := &TronFetcher{Base: srv.URL}
	deps, newest, err := f.FetchDeposits(context.Background(), "Taddr", 100)
	if err != nil {
		t.Fatalf("链头走 POST /wallet/getnowblock 应成功，实得 err=%v（命中 %+v）", err, hits)
	}
	if newest != 86875123 {
		t.Fatalf("链头高度应为 86875123（raw_data.number），实得 %d", newest)
	}
	if len(deps) != 0 {
		t.Fatalf("本轮无转入，deps 应为空，实得 %d 笔", len(deps))
	}
	if len(hits) == 0 {
		t.Fatal("一次上游请求都没打到")
	}
	if hits[0].method != http.MethodPost || hits[0].path != "/wallet/getnowblock" {
		t.Fatalf("第一个上游请求必须是 POST /wallet/getnowblock，实际 %s %s（全部 %+v）", hits[0].method, hits[0].path, hits)
	}
	// 请求体这一格也锁住：wallet 那一族要合法 JSON 体，nil 体在某些自建网关上是 400。
	if hits[0].body != "{}" {
		t.Fatalf("POST 链头的请求体必须是 {}，实得 %q", hits[0].body)
	}
	for _, h := range hits {
		if h.path == "/v1/blocks/latest" || h.path == "/v1/blocks" {
			t.Fatalf("默认端点已按㊾换成 wallet 那一族，仍在打退役的 %s（全部 %+v）", h.path, hits)
		}
	}
}

// TestTronHeadPathEnvOverride USDT_TRON_HEAD_PATH／_METHOD 覆盖腿：上游改路径**或改方法**时不改码顶住。
// ★ ㊾ 补的就是方法这一把：旧覆盖口只重写 URL 路径，方法钉死 GET ⇒ 面对"同一路径 GET 也 404"的真上游，
// 运营侧无论怎么调档都到不了（那正是 ㊾ 现网恒 404 的第二层）。
// 判据＝换档后第一个请求打到的是覆盖后的 **方法＋路径**（默认那一路一次都不许出现），且高度照读得出。
func TestTronHeadPathEnvOverride(t *testing.T) {
	t.Setenv("USDT_TRON_HEAD_PATH", "api/chain-head") // 刻意不带前导斜杠：覆盖腿要自己补齐
	t.Setenv("USDT_TRON_HEAD_METHOD", "get")          // 刻意小写：覆盖口要归一成大写方法
	type hitT struct{ method, path string }
	var hits []hitT
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, hitT{r.Method, r.URL.Path})
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
		t.Fatalf("覆盖路径应生效并读到高度：newest=%d err=%v 命中 %+v", newest, err, hits)
	}
	if len(hits) == 0 || hits[0].method != http.MethodGet || hits[0].path != "/api/chain-head" {
		t.Fatalf("覆盖口生效时第一个请求必须是 GET /api/chain-head，实际 %+v（全部 %+v）", hits, hits)
	}
	for _, h := range hits {
		if h.path == "/wallet/getnowblock" {
			t.Fatalf("设了覆盖口还打默认 wallet 端点，命中 %+v", hits)
		}
	}
}

// TestTronHeadAcceptsFourShapes ⑮＋㊾ 结构腿：四种真实响应形态都必须读出同一个高度，
// 且优先级钉成「raw_data.number 优先 ＞ block_header.number ＞ block_number ＞ data[0].block」。
//
// 只认后三种（block_header.number／block_number／data[].block）是 ㊾ 的第二层根因：
// 真上游 `POST /wallet/getnowblock` 的高度在**再往里一层**的 raw_data.number，
// 光把端点改对也照样读 0 ⇒ 而 newest=0 会让确认数永远不达标（钱到了也不入账）。
// ★ 带引号那一档（数字写成 "7"）不是凑数：wallet 那一族由 protobuf 直转 JSON，
// 数字字段可能是 7 也可能是 "7"，按 int64 硬解会在 Unmarshal 阶段整条报错——
// 表现与「端点不存在」完全同形（监听恒 failing、日志只说查询失败），是最难分的一档。
func TestTronHeadAcceptsFourShapes(t *testing.T) {
	cases := []struct{ name, body string }{
		{"真上游 raw_data.number（数字）", `{"block_header":{"raw_data":{"number":7}}}`},
		{"真上游 raw_data.number（带引号）", `{"block_header":{"raw_data":{"number":"7"}}}`},
		{"latest/block_header", `{"block_header":{"number":7}}`},
		{"mock/顶层 block_number", `{"block_number":7}`},
		{"列表 data[0].block", `{"data":[{"block":7}]}`},
		{"优先级取第一腿（raw_data 赢）", `{"block_header":{"raw_data":{"number":7},"number":8},"block_number":9,"data":[{"block":11}]}`},
		{"优先级第二腿（raw_data 缺位时 block_header 顶上）", `{"block_header":{"raw_data":{"timestamp":1},"number":7},"block_number":9}`},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/wallet/getnowblock") {
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
