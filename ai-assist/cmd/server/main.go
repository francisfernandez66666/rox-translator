// ai-assist 轻量 AI 销售/客服服务（从 ai-scrm-v2 剥离）。
// 职责：访客免登录接待、LLM 多模型降级、知识库/话术/流程/功能入口管理。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"ai-assist/internal/api"
	"ai-assist/internal/config"
	"ai-assist/internal/engine"
	"ai-assist/internal/llm"
	"ai-assist/internal/store"
)

// main 入口：装配配置→存储→LLM 客户端→引擎→HTTP 服务，seed 仅首启灌入
func main() {
	addr := flag.String("addr", "", "监听地址（默认读 ASSIST_ADDR，再默认 127.0.0.1:8790）")
	dbPath := flag.String("db", "", "SQLite 路径（默认读 ASSIST_DB）")
	seed := flag.String("seed", "", "seed 文件（默认读 ASSIST_SEED）")
	flag.Parse()

	cfg := config.Load()
	if *addr != "" {
		cfg.Addr = *addr
	}
	if *dbPath != "" {
		cfg.DBPath = *dbPath
	}
	if *seed != "" {
		cfg.SeedFile = *seed
	}

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// LLM 降级链：主模型 → 备用模型（同 OpenAI 兼容协议）
	if cfg.MockMode {
		log.Println("[llm] ASSIST_MOCK=1，不调用真实模型（规则兜底）")
	} else if cfg.LLMAPIKey != "" && cfg.LLMBaseURL != "" && cfg.LLMModel != "" {
		log.Println("[llm] 降级链已配置: " + cfg.LLMModel + " → " + cfg.LLMModelBackup)
	} else {
		log.Println("[llm] 未配置模型，将走纯规则兜底（可配 ASSIST_LLM_* 环境变量启用）")
	}
	client := buildLLM(cfg)

	eng := engine.New(db, client)

	// seed：首启灌入知识库/话术/流程/功能入口
	if err := loadSeed(db, cfg.SeedFile); err != nil {
		log.Printf("[seed] 跳过: %v", err)
	}

	srv := api.NewServer(db, eng, cfg.AdminToken, cfg.CORSOrigin)
	log.Printf("========================================")
	log.Printf("  ai-assist 启动成功  http://%s", cfg.Addr)
	log.Printf("  管理台: http://%s/assist/admin  (token: %s)", cfg.Addr, maskToken(cfg.AdminToken))
	log.Printf("  健康:   http://%s/health", cfg.Addr)
	log.Printf("========================================")
	if err := http.ListenAndServe(cfg.Addr, srv.Handler()); err != nil {
		log.Fatalf("listen: %v", err)
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

// loadSeed 按 seed.json 灌入初始数据；对应表非空则整体跳过（幂等，保护后台编辑）
func loadSeed(db *store.DB, path string) error {
	if path == "" {
		return fmt.Errorf("no seed file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var sf SeedFile
	if err := json.Unmarshal(b, &sf); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	seedIfEmpty := func(table string, rows []map[string]any, keyField string) {
		existing, _ := db.List(table, false)
		if len(existing) > 0 || len(rows) == 0 {
			return
		}
		for _, r := range rows {
			normalizeRowForSeed(table, r)
			if _, err := db.Create(table, r); err != nil {
				log.Printf("[seed] %s %v 失败: %v", table, r[keyField], err)
			}
		}
		log.Printf("[seed] %s 灌入 %d 条", table, len(rows))
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
