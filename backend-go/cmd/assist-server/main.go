// ============ cmd/assist-server · 职责说明 ============
// AI 助手（销售/客服）服务入口（★ 改造 1A，2026-09-17 并入主仓单 module）。
//
// 职责：访客免登录接待、LLM 多模型降级、知识库/话术/流程/功能入口管理。
// 装配：配置 → 存储（独立 SQLite，保持与业务库隔离）→ LLM 客户端 → 引擎 → HTTP 服务。
//
// 融合要点（相对原 ai-assist 独立仓库）：
//   - 单 module：源码位于 internal/assist/*，依赖与主服务共用一份 go.mod；
//   - 单产物：管理台页面（web.AdminHTML）与初始 seed（seed.SeedJSON）随二进制内嵌，
//     部署不再需要投放 web/ 与 seed/ 目录；
//   - 统一观测：slog JSON 日志器（observability.NewLogger），与主服务同格式、带 trace_id；
//   - 鉴权贯通：管理台 Token 优先取 env ASSIST_ADMIN_TOKEN（保底），回落到主库
//     system_config.assist_admin_token（enc:v1: 密文，可经主后台轮换），
//     用户不再需要在管理台手工粘贴 Token。
//
// =============================================
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"translator/internal/assist/api"
	"translator/internal/assist/config"
	"translator/internal/assist/engine"
	"translator/internal/assist/llm"
	"translator/internal/assist/seed"
	"translator/internal/assist/store"
	"translator/internal/observability"
	"translator/internal/secret"
)

// main 入口：装配配置→存储→LLM 客户端→引擎→HTTP 服务，seed 仅首启灌入
func main() {
	addr := flag.String("addr", "", "监听地址（默认读 ASSIST_ADDR，再默认 127.0.0.1:8790）")
	dbPath := flag.String("db", "", "SQLite 路径（默认读 ASSIST_DB）")
	seedFile := flag.String("seed", "", "seed 文件（默认读 ASSIST_SEED；留空用内嵌 seed）")
	mainDB := flag.String("main-db", "", "主服务 SQLite 路径（读取 system_config.assist_admin_token；默认读 MAIN_DB）")
	flag.Parse()

	// ★ 改造 1A：与主服务同口径的结构化日志（JSON + trace_id）
	slog.SetDefault(observability.NewLogger())

	cfg := config.Load()
	if *addr != "" {
		cfg.Addr = *addr
	}
	if *dbPath != "" {
		cfg.DBPath = *dbPath
	}
	if *seedFile != "" {
		cfg.SeedFile = *seedFile
	}
	if *mainDB != "" {
		cfg.MainDBPath = *mainDB
	}

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		slog.Error("assist 数据库打开失败", "path", cfg.DBPath, "err", err)
		os.Exit(1)
	}
	defer db.Close()

	// LLM 降级链：主模型 → 备用模型（同 OpenAI 兼容协议）
	switch {
	case cfg.MockMode:
		slog.Info("assist.llm 运行于 mock 模式，不调用真实模型（规则兜底）")
	case cfg.LLMAPIKey != "" && cfg.LLMBaseURL != "" && cfg.LLMModel != "":
		slog.Info("assist.llm 降级链已配置", "model", cfg.LLMModel, "backup", cfg.LLMModelBackup)
	default:
		slog.Info("assist.llm 未配置模型，将走纯规则兜底（可配 ASSIST_LLM_* 环境变量启用）")
	}
	client := buildLLM(cfg)

	eng := engine.New(db, client)

	// seed：首启灌入知识库/话术/流程/功能入口（外置文件优先，否则用内嵌 seed）
	if err := loadSeed(db, cfg.SeedFile); err != nil {
		slog.Warn("assist seed 跳过", "err", err)
	}

	// ★ 改造 1A：管理台 Token 解析链 env → 主库 system_config（密文）→ 默认值
	adminToken, tokenSrc := resolveAdminToken(cfg)

	srv := api.NewServer(db, eng, adminToken, cfg.CORSOrigin)
	slog.Info("assist 服务启动",
		"addr", cfg.Addr, "admin_path", "/assist/admin",
		"token_source", tokenSrc, "token", maskToken(adminToken))
	if err := http.ListenAndServe(cfg.Addr, srv.Handler()); err != nil {
		slog.Error("assist 服务退出", "err", err)
		os.Exit(1)
	}
}

// buildLLM 构建 LLM 客户端（主 + 备用）
func buildLLM(cfg *config.Config) *llm.Client {
	if cfg.MockMode || cfg.LLMAPIKey == "" || cfg.LLMBaseURL == "" || cfg.LLMModel == "" {
		return llm.New(nil, cfg.LLMTimeoutSec)
	}
	providers := []llm.Provider{{
		Name: "main", BaseURL: cfg.LLMBaseURL, APIKey: cfg.LLMAPIKey, Model: cfg.LLMModel,
	}}
	if cfg.LLMModelBackup != "" {
		key := cfg.LLMAPIKey2
		if key == "" {
			key = cfg.LLMAPIKey
		}
		providers = append(providers, llm.Provider{
			Name: "backup", BaseURL: cfg.LLMBaseURL, APIKey: key, Model: cfg.LLMModelBackup,
		})
	}
	return llm.New(providers, cfg.LLMTimeoutSec)
}

// resolveAdminToken 解析生效的管理台 Token（★ 改造 1A）。
// 优先级：env ASSIST_ADMIN_TOKEN（部署侧保底）→ 主库 system_config.assist_admin_token
// （enc:v1: 密文，与主后台 /api/admin/assist/token 同源，可在后台轮换）→ 内置默认值。
// 参数：cfg=服务配置。返回：Token 明文与来源标识（env/db/default）。
func resolveAdminToken(cfg *config.Config) (string, string) {
	if v := strings.TrimSpace(os.Getenv("ASSIST_ADMIN_TOKEN")); v != "" {
		return v, "env"
	}
	// 主库可能尚未初始化（或未配置路径）：读不到就回落到内置默认值，不阻断启动
	mainDBPath := strings.TrimSpace(cfg.MainDBPath)
	if mainDBPath == "" {
		mainDBPath = strings.TrimSpace(os.Getenv("MAIN_DB"))
	}
	if mainDBPath != "" {
		if tok := readMainDBAdminToken(mainDBPath); tok != "" {
			return tok, "db"
		}
	}
	return cfg.AdminToken, "default"
}

// readMainDBAdminToken 只读方式打开主服务 SQLite，读取并解密 assist_admin_token。
// 只读连接（mode=ro）避免与主服务写锁竞争；任何失败均返回空串（调用方回落），不阻断启动。
// 解密复用 internal/secret（与主后台写入侧同源），不引入对业务存储层的反向依赖。
// 参数：path=主库文件路径。返回：Token 明文（无配置/解密失败为 ""）。
func readMainDBAdminToken(path string) string {
	raw := store.ReadMainDBConfig(path, "assist_admin_token")
	if raw == "" {
		return ""
	}
	return secret.DecryptSecret(raw)
}

// maskToken 管理台启动日志的 Token 脱敏展示（只露前 4 位）
func maskToken(t string) string {
	if len(t) <= 6 {
		return "***"
	}
	return t[:4] + "***"
}

// ============================================================
// seed 灌入：仅当对应表为空时加载（幂等，不覆盖后台已编辑的数据）
// ============================================================

// SeedFile seed JSON 结构
type SeedFile struct {
	KB       []map[string]any  `json:"kb"`
	Scripts  []map[string]any  `json:"scripts"`
	Flows    []map[string]any  `json:"flows"`
	Features []map[string]any  `json:"features"`
	Configs  map[string]string `json:"configs"`
}

// loadSeed 按 seed 灌入初始数据；对应表非空则整体跳过（幂等，保护后台编辑）。
// 数据来源优先级：显式路径（ASSIST_SEED/-seed）可读 → 该文件；否则回落到内嵌 seed（seed.SeedJSON）。
// 参数：db=assist 存储；path=外置 seed 路径（可为空）。返回错误（仅当两个来源都不可用时）。
func loadSeed(db *store.DB, path string) error {
	var raw []byte
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			raw = b
		}
	}
	if raw == nil && len(seed.SeedJSON) > 0 {
		raw = seed.SeedJSON // ★ 改造 1A：内嵌兜底，部署无需外置 seed 文件
	}
	if raw == nil {
		return fmt.Errorf("无可用 seed 来源（外置 %q 不可读且无内嵌 seed）", path)
	}
	var sf SeedFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return fmt.Errorf("seed 解析失败: %w", err)
	}
	seedIfEmpty := func(table string, rows []map[string]any, keyField string) {
		existing, _ := db.List(table, false)
		if len(existing) > 0 || len(rows) == 0 {
			return
		}
		for _, r := range rows {
			normalizeRowForSeed(table, r)
			if _, err := db.Create(table, r); err != nil {
				slog.Warn("assist seed 行写入失败", "table", table, "key", r[keyField], "err", err)
			}
		}
		slog.Info("assist seed 灌入", "table", table, "rows", len(rows))
	}
	seedIfEmpty("kb_entries", sf.KB, "key")
	seedIfEmpty("scripts", sf.Scripts, "key")
	seedIfEmpty("flows", sf.Flows, "key")
	seedIfEmpty("feature_links", sf.Features, "key")
	if n := db.SessionCount(); n == 0 && sf.Configs != nil {
		for k, v := range sf.Configs {
			if db.GetConfig(k, "") == "" {
				_ = db.SetConfig(k, v)
			}
		}
	}
	return nil
}

// normalizeRowForSeed seed 行类型归一（JSON 数字为 float64）
func normalizeRowForSeed(table string, r map[string]any) {
	for _, k := range []string{"priority", "sort", "enabled"} {
		if v, ok := r[k]; ok {
			if f, ok := v.(float64); ok {
				r[k] = int(f)
			}
		}
	}
	_ = table
}
