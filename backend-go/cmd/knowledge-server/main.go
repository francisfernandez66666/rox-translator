// ============ cmd/knowledge-server · 职责说明 ============
// 四期 W3-2：知识服务独立进程（:8791），只连 KNOWLEDGE_DB_DSN（§4.3.1 第 1/2 条）。
//
//   - 启动期硬断言双 DSN 不等（KN12）：KNOWLEDGE_DB_DSN == DB_DSN ⇒ 拒绝启动并把原因写日志。
//   - 只绑 127.0.0.1:8791，共享密钥头 X-Knowledge-Token；不挂公网、不进反代。
//   - 知识侧 KnowledgeMigrate() 在独立库建四张知识表（幂等）。
//   - 日志走 internal/observability（JSON + trace_id），与 assist-server 同口径。
//
// ⚠️ 本进程**不**持有主库连接：DB_DSN 只在 LoadConfig 里作为「断言比较的字符串」读入，
//    不打开、不查询。主服务那侧的读写一律经 internal/knowledge/client。
// ================================================
package main

import (
	"log/slog"
	"net/http"
	"os"

	"translator/internal/knowledge"
	"translator/internal/knowledge/contract"
	kapi "translator/internal/knowledge/api"
	knowledgeStore "translator/internal/knowledge/store"
	"translator/internal/observability"

	_ "github.com/lib/pq"  // PG 驱动（生产 KNOWLEDGE_DB_DRIVER=postgres）
	_ "modernc.org/sqlite" // SQLite 驱动（本地/测试）
)

func main() {
	slog.SetDefault(observability.NewLogger())

	cfg := knowledge.LoadConfig()

	// ★ KN12：双 DSN 不等硬断言。相等/空 ⇒ 拒绝启动（原因写结构化日志，不静默退出）。
	if err := knowledge.StartupGuard(cfg); err != nil {
		slog.Error("knowledge 服务拒绝启动（双 DSN 断言）", "err", err)
		os.Exit(1)
	}
	// 管理面 Token 三级皆空即拒启（同 assist-server P0-2 口径，不允许匿名裸奔）。
	if cfg.Token == "" {
		slog.Error("knowledge 服务拒绝启动：KNOWLEDGE_TOKEN 未配置")
		os.Exit(1)
	}

	ks, err := knowledgeStore.Open(knowledgeStore.Config{
		Driver: cfg.KnowledgeDriver,
		DSN:    cfg.KnowledgeDSN,
	})
	if err != nil {
		slog.Error("knowledge 独立库打开失败", "err", err)
		os.Exit(1)
	}
	defer ks.Close()

	if err := ks.Migrate(); err != nil {
		slog.Error("knowledge 四表迁移失败", "err", err)
		os.Exit(1)
	}

	srv := kapi.NewServer(ks, cfg.Token)
	slog.Info("knowledge 服务启动",
		"addr", cfg.Addr, "contract_version", contract.ContractVersion,
		"service_read", cfg.ServiceRead)
	if err := http.ListenAndServe(cfg.Addr, srv.Handler()); err != nil {
		slog.Error("knowledge 服务退出", "err", err)
		os.Exit(1)
	}
}
