// ============ main.go · 职责说明 ============
// cmd/rebuild-kb-index 包一次性向量索引回填工具。
// PostgreSQL 切流后，tm_segments.embedding（pgvector）列在迁移时
// 被刻意跳过（见 cmd/migrate-sqlite-to-pg 注释），本工具遍历全量段、调用 Embedding API
// 重新计算向量并 UpsertEmbedding 写回 PG，使语义检索（VectorSearch）生效。
//
// 前置条件：
//  1. DB_DRIVER=postgres 且 DB_DSN 指向已建好 schema 的 PG（建议先跑一次 server --init-db）；
//  2. Embedding API 可用（ONLINE_API_KEY / EMBED_API_KEY 或后台配置已水合）；
//  3. pgvector 扩展已安装（否则 UpsertEmbedding 自动跳过，语义检索回退 npz）。
//
// 幂等：已存在向量也会覆盖重写，可重复执行；支持 -batch 分批、-workers 并发嵌入、-limit 限量。
// 用法：
//   go run ./cmd/rebuild-kb-index
//   DB_DRIVER=postgres DB_DSN=postgres://... go run ./cmd/rebuild-kb-index -batch 256 -workers 4
// =============================================
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"translator/internal/config"
	"translator/internal/kb"
	"translator/internal/llm"
)

func main() {
	batch := flag.Int("batch", 256, "每批嵌入的段数量")
	workers := flag.Int("workers", 4, "并发嵌入批数（另受 LLM_EMBED_CONCURRENT 限流约束）")
	limit := flag.Int("limit", 0, "最多处理的段数（0=不限，调试用）")
	flag.Parse()

	if *workers < 1 {
		*workers = 1
	}
	if *batch < 1 {
		*batch = 256
	}

	cfg := config.Default()
	cfg.LoadConfigFromJSON(".")

	if cfg.DatabaseDriver != "postgres" {
		log.Println("[rebuild] 当前后端非 PostgreSQL（DB_DRIVER=" + cfg.DatabaseDriver + "），无需回填 pgvector；如需重建 npz 索引请另用引擎索引重建。退出。")
		return
	}
	if cfg.EmbedAPIKey == "" {
		log.Println("[rebuild] 警告: Embedding API Key 未配置，嵌入调用将失败；请配置 ONLINE_API_KEY/EMBED_API_KEY 或后台水合后重试。仍会继续尝试（失败段跳过）。")
	}

	kdb, err := kb.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("[rebuild] 打开知识库失败: %v", err)
	}
	cli := llm.NewClient(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("[rebuild] 收到中断信号，安全停止（已写回的向量已落库）…")
		cancel()
	}()

	const pageSize = 2000
	var totalDone, totalOK, totalFail int64
	offset := 0
	sem := make(chan struct{}, *workers)

	for {
		if ctx.Err() != nil {
			break
		}
		if *limit > 0 && atomic.LoadInt64(&totalDone) >= int64(*limit) {
			break
		}
		// 分页拉取（避免一次 SELECT 全表占内存）
		segs, err := kdb.SegmentsForEmbedding(pageSize, offset)
		if err != nil {
			log.Fatalf("[rebuild] 拉取段失败(offset=%d): %v", offset, err)
		}
		if len(segs) == 0 {
			break
		}
		offset += len(segs)

		// 按 batch 切片，受 workers 信号量约束并发嵌入 + 写回
		var wg sync.WaitGroup
		for s := 0; s < len(segs); s += *batch {
			if ctx.Err() != nil {
				break
			}
			e := s + *batch
			if e > len(segs) {
				e = len(segs)
			}
			chunk := segs[s:e]
			sem <- struct{}{}
			wg.Add(1)
			go func(chunk []kb.EmbedSeg) {
				defer wg.Done()
				defer func() { <-sem }()
				if ctx.Err() != nil {
					atomic.AddInt64(&totalFail, int64(len(chunk)))
					atomic.AddInt64(&totalDone, int64(len(chunk)))
					return
				}
				texts := make([]string, len(chunk))
				for i, c := range chunk {
					texts[i] = c.Zh
				}
				vecs, eerr := cli.EmbedBatch(ctx, texts)
				if eerr != nil {
					log.Printf("[rebuild] 批次嵌入失败(%d 段): %v", len(chunk), eerr)
					atomic.AddInt64(&totalFail, int64(len(chunk)))
					atomic.AddInt64(&totalDone, int64(len(chunk)))
					return
				}
				if len(vecs) != len(chunk) {
					log.Printf("[rebuild] 警告: 嵌入返回数量不匹配(%d!=%d)，本批跳过", len(vecs), len(chunk))
					atomic.AddInt64(&totalFail, int64(len(chunk)))
					atomic.AddInt64(&totalDone, int64(len(chunk)))
					return
				}
				for i, c := range chunk {
					if ctx.Err() != nil {
						atomic.AddInt64(&totalFail, int64(len(chunk)-i))
						atomic.AddInt64(&totalDone, int64(len(chunk)-i))
						return
					}
					if uerr := kdb.UpsertEmbedding(c.ID, vecs[i]); uerr != nil {
						log.Printf("[rebuild] 写回向量失败(段 %d): %v", c.ID, uerr)
						atomic.AddInt64(&totalFail, 1)
					} else {
						atomic.AddInt64(&totalOK, 1)
					}
				}
				atomic.AddInt64(&totalDone, int64(len(chunk)))
			}(chunk)
		}
		wg.Wait()

		done := atomic.LoadInt64(&totalDone)
		if done%(pageSize*5) < int64(pageSize) {
			log.Printf("[rebuild] 进度: 已处理 %d 段（成功 %d / 失败 %d）", done, atomic.LoadInt64(&totalOK), atomic.LoadInt64(&totalFail))
		}
	}

	log.Printf("[rebuild] 完成: 共处理 %d 段，向量写回成功 %d，失败 %d",
		atomic.LoadInt64(&totalDone), atomic.LoadInt64(&totalOK), atomic.LoadInt64(&totalFail))
	if atomic.LoadInt64(&totalFail) > 0 {
		log.Println("[rebuild] 提示: 失败段多为 Embedding API 限流/密钥问题，修复后可重新运行本工具（幂等覆盖）。")
		os.Exit(2)
	}
	time.Sleep(10 * time.Millisecond) // 确保落库日志刷出
	fmt.Println("rebuild-kb-index done")
}
