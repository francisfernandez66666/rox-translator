// ============ 本文件职责中文说明 ============
// 一次性向量回填工具：将既有知识库向量索引（tm_embeddings.npz）写回 PostgreSQL 的
// tm_segments.embedding（pgvector）列，使原生向量语义检索（kb.VectorSearch）生效。
//
// 与 rebuild-kb-index 的区别：本工具不调用 Embedding API、不重新计算向量，而是直接复用
// 已落盘的 npz 向量（与线上术语召回完全一致），零成本、秒级完成，适合切流后即刻启用 pgvector。
// 若后续想用最新模型重算，可改跑 rebuild-kb-index（需 Embedding Key）。
//
// 幂等：UPDATE 按段主键写入，可重复执行。
// 用法：
//   backfill-embeddings -npz /opt/translator/data/tm_embeddings.npz -dsn "postgres://..."
//   DB_DRIVER=postgres DB_DSN=postgres://... backfill-embeddings
// =============================================
package main

import (
	"flag"
	"log"
	"os"

	_ "github.com/lib/pq"

	"translator/internal/config"
	"translator/internal/kb"
)

func main() {
	npz := flag.String("npz", "/opt/translator/data/tm_embeddings.npz", "npz 向量索引路径")
	dsn := flag.String("dsn", "", "目标 PostgreSQL DSN（缺省取环境变量 DB_DSN）")
	flag.Parse()

	cfg := config.Default()
	if *dsn != "" {
		cfg.DatabaseDSN = *dsn
	}
	if cfg.DatabaseDSN == "" {
		cfg.DatabaseDSN = os.Getenv("DB_DSN")
	}
	if cfg.DatabaseDSN == "" {
		log.Fatal("缺少 PG DSN：请用 -dsn 或环境变量 DB_DSN")
	}
	cfg.DatabaseDriver = "postgres"

	kdb, err := kb.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("[backfill] 打开知识库失败: %v", err)
	}
	idx, err := kb.LoadNPZ(*npz)
	if err != nil {
		log.Fatalf("[backfill] 加载 npz 失败: %v", err)
	}

	total := len(idx.IDs)
	ok, missing := 0, 0
	for i, id := range idx.IDs {
		if len(idx.Vecs[i]) == 0 {
			missing++
			continue
		}
		if err := kdb.UpsertEmbedding(id, idx.Vecs[i]); err != nil {
			log.Printf("[backfill] 段 %d 写回失败: %v", id, err)
			missing++
			continue
		}
		ok++
		if (i+1)%500 == 0 {
			log.Printf("[backfill] 进度 %d/%d", i+1, total)
		}
	}
	log.Printf("[backfill] 完成：写回=%d 跳过(空向量/缺失)=%d 总计=%d", ok, missing, total)
}
