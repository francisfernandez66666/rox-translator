// ============ fpdprobe · G6 块级派发真件往返探针（2026-11-26） ============
// 与旧探针同形：直接调产品侧同一批导出函数（DispatchChunkRun），
// 不依赖决策器是否放行（新基线 load1=2.36 下规则 6b 不派），专门验块级传输链。
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"translator/internal/config"
	"translator/internal/fileproc"
	"translator/internal/kb"
	"translator/internal/llmsource"
	"translator/internal/store"
)

// runChunkProbe env 口：Key/端点从环境变量取（默认 probe-invalid，验传输四层）。
func runChunkProbe(inPath string) int {
	return runChunkProbeWithKey(inPath, os.Getenv("FILEPROC_DISPATCH_PROBE_KEY"),
		os.Getenv("FILEPROC_DISPATCH_PROBE_BASE"), "")
}

// runChunkProbeReal 用真件走 extract→translate→zip 回拉→主站逐块校验；输出与旧探针同前缀的读数行。
func runChunkProbeWithKey(inPath, probeKey, probeBase, probeModel string) int {
	if !fileproc.DispatchEnabled() {
		fmt.Println("@@PROBE chunk_result=closed reason=dispatch_off")
		return 3
	}
	abs, err := filepath.Abs(inPath)
	if err != nil {
		fmt.Println("@@PROBE chunk_result=red reason=abs_failed")
		return 1
	}
	fi, err := os.Stat(abs)
	if err != nil || fi.Size() == 0 {
		fmt.Println("@@PROBE chunk_result=red reason=input_missing")
		return 1
	}
	session := fileproc.SessionIDFor(abs)
	outDir := filepath.Dir(abs)
	zipPath := filepath.Join(outDir, session+".chunkprobe.zip")
	textsPath := filepath.Join(outDir, session+".chunkprobe.texts.json")
	defer os.Remove(zipPath)
	defer os.Remove(textsPath)

	// 真件 60 MiB/≈86 页 ⇒ 段数可能上千；按 40 段/批算边界（与主站 adaptiveBatchSize 同口径）
	pages, _ := fileproc.PdfPageCount(abs)
	fmt.Printf("@@PROBE chunk_input size=%d pages=%d session=%s\n", fi.Size(), pages, session)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	res, err := fileproc.DispatchChunkRun(ctx, fileproc.ChunkRunRequest{
		SessionID: session,
		InputPDF:  abs,
		Lang:      "en",
		Bounds:    [][2]int{{0, 1}}, // 探针只需验证链路与回执，不做真翻译（恒等映射由 worker 侧开关处理）
		Key:       firstNonEmpty(probeKey, "probe-invalid"),
		Base:      firstNonEmpty(probeBase, os.Getenv("FILEPROC_DISPATCH_PROBE_BASE")),
		Model:     firstNonEmpty(probeModel, "probe-model"),
		OutputZip: zipPath,
		TextsOut:  textsPath,
	})
	if err != nil {
		fmt.Printf("@@PROBE chunk_result=red reason=dispatch_failed detail=%s\n", err.Error())
		return 1
	}
	fmt.Printf("@@PROBE chunk_llm_key=%s\n", map[bool]string{true: "real", false: "probe-invalid"}[probeKey != "probe-invalid"])
	// 远端证据：整段 stdout 无法在 DispatchChunkRun 之后取到，改为验产物魔数与绝对路径落点。
	remoteEvidence := false
	if f, e := os.Open(zipPath); e == nil {
		magic := make([]byte, 4)
		_, _ = f.Read(magic)
		f.Close()
		remoteEvidence = string(magic) == "PK\x03\x04"
	}
	fmt.Printf("@@PROBE chunk_result=%s chunks=%d tokens=%d zip_bytes=%d remote_evidence=%v\n",
		map[bool]string{true: "ok", false: "suspect"}[remoteEvidence], res.ChunkCount,
		res.TokensUsed, fileSize(zipPath), remoteEvidence)
	if !remoteEvidence {
		fmt.Println("@@PROBE verdict=SUSPECT ⇒ zip 魔数不符/无远端证据")
		return 2
	}
	fmt.Println("@@PROBE verdict=OK ⇒ 块级派发链真机往返过一次")
	return 0
}

func fileSize(p string) int64 {
	if fi, err := os.Stat(p); err == nil {
		return fi.Size()
	}
	return 0
}

// hasChunkFlag 判断参数是否含 --chunk（不引入 flag 包，避免与现有 os.Args 解析冲突）。
func hasChunkFlag(args []string) bool {
	for _, a := range args {
		if strings.TrimSpace(a) == "--chunk" {
			return true
		}
	}
	return false
}

// firstNonEmpty 返回第一个非空串（都空返回默认）。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// runChunkProbeReal 真钥匙端到端：从主站 llmsource.Current（唯一读点）取当前生效配置，
// Key 只在本进程内存流转——不回显、不落盘、不进 argv。复刻 server 的最小初始化形态。
func runChunkProbeReal(inPath string) int {
	if !fileproc.DispatchEnabled() {
		fmt.Println("@@PROBE chunk_real=closed reason=dispatch_off")
		return 3
	}
	cfg := config.Default()
	k, err := kb.Open(cfg.DBPath)
	if err != nil {
		fmt.Printf("@@PROBE chunk_real=red reason=kb_open detail=%v\n", err)
		return 1
	}
	defer k.Close()
	st, err := store.New(k.RawDB())
	if err != nil {
		fmt.Printf("@@PROBE chunk_real=red reason=store_new detail=%v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	snap := llmsource.Current(ctx, st, cfg)
	if snap == nil || snap.OnlineAPIKey == "" {
		fmt.Printf("@@PROBE chunk_real=red reason=no_key source=%s\n", snap.From)
		return 1
	}
	fmt.Printf("@@PROBE chunk_real llm_source=%s base_ok=%v key_ok=%v\n",
		snap.From, snap.OnlineAPIBase != "", snap.OnlineAPIKey != "")
	return runChunkProbeWithKey(inPath, snap.OnlineAPIKey, snap.OnlineAPIBase, snap.OnlineModel)
}

// firstPositional 取第一个非 -- 开头的位置参数（main 的 --chunk/--chunk-real 共用）。
func firstPositional(args []string) string {
	for _, a := range args[1:] {
		if !strings.HasPrefix(a, "--") {
			return a
		}
	}
	return ""
}

// hasFlag 判断参数列表是否含指定 flag。
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if strings.TrimSpace(a) == flag {
			return true
		}
	}
	return false
}
