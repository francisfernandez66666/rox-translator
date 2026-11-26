// ============ cmd/chunkworker · 广州执行机上的块翻译 worker ============
// stdin 读 ChunkTranslateRequest JSON，stdout 写 ChunkTranslateResult JSON。
// 不接 DB、不读配置文件、不监听端口、不写任何文件——Key 只在进程的 env/内存里。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"translator/internal/engine"
	"translator/internal/llm"
)

func main() {
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 64<<20))
	if err != nil {
		os.Exit(30)
	}
	var req engine.ChunkTranslateRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		os.Exit(30)
	}
	if req.Key == "" {
		// Python 胶水通过 env 传 Key（不落盘、不进 argv）
		req.Key = os.Getenv("CHUNKWORKER_LLM_KEY")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	// ★ 2026-11-26 C-7：assemble 接手（超限后广州本地拼回写回），
	// 与翻译共用同一 worker 二进制；不另造脚本、不回主站补做（整单锁定）。
	if req.Op == "assemble" {
		if err := engine.RunChunkAssemble(ctx, req); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(31)
		}
		os.Stdout.Write([]byte(`{"ok":true,"op":"assemble"}`))
		return
	}
	client := llm.NewClient(nil)
	res := engine.RunChunkTranslation(ctx, client, req)
	out, err := engine.MarshalChunkResult(res)
	if err != nil {
		os.Exit(30)
	}
	os.Stdout.Write(out)
	if !res.OK {
		os.Exit(31)
	}
}
