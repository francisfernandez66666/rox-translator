// ============ cmd/server/assist_local_wiring.go · 职责说明 ============
// 四期 W5 收口（2026-11-15，审计 §3.3）：把 assist 接待引擎**装进主服务进程**。
//
// 旧形态：InstallAssistLocal / AssistLocalEngine 零调用者，server.go 仍把
// /assist-api/* 指向跨进程代理 ⇒ A3「兼容」绿着，但「并进程」根本没发生。
//
// ★ 装配顺序逐项对齐 cmd/assist-server/main.go（配置 → assist 库 → LLM → 引擎
//
//	→ seed → canned 清理 → handler），只是不监听 :8790、不启动第二个 HTTP 服务。
//	assist 的 LLM 与主服务**不共用** llm.Client：两套降级链/canned 缓存合成一套
//	会把各自的退避窗口与指纹口径搅在一起。
//
// ★ fail-open：装配失败（库打不开 / seed 失败）一律 WARN 后返回，**绝不把主站拖死**。
//
//	此时 /assist-api/* 由 handleAssistOpen 回落旧代理（A3 的兼容面）。
//
// ★ 管理台 Token：主服务的 /assist-api/* 白名单只放行四个访客端点，
//
//	assist 管理台路由并不对外注册；但 guard 的空头比较不能留空串，
//	故三级解析皆空时生成一个进程内随机 token（不外泄、不写库）。
//
// ================================================
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"translator/internal/api"
	assistapi "translator/internal/assist/api"
	assistconfig "translator/internal/assist/config"
	assistengine "translator/internal/assist/engine"
	assistllm "translator/internal/assist/llm"
	assistseed "translator/internal/assist/seed"
	assiststore "translator/internal/assist/store"
)

// installAssistLocal 在 NewServer 之后、ListenAndServe 之前调一次。
// 参数：无（配置全部读 env，与 cmd/assist-server 同源）；返回：无（失败只 WARN）。
func installAssistLocal() {
	cfg := assistconfig.Load()
	adb, err := assiststore.Open(cfg.DBPath)
	if err != nil {
		slog.Warn("assist 本地引擎未装配（库打不开，/assist-api 回落旧代理）", "path", cfg.DBPath, "err", err)
		return
	}
	eng := assistengine.New(adb, buildAssistLLM(cfg))
	if err := loadAssistSeed(adb, cfg.SeedFile); err != nil {
		slog.Warn("assist seed 跳过（不阻断主站）", "err", err)
	}
	if n := eng.PurgeStaleCannedTranslations(context.Background()); n > 0 {
		slog.Info("assist canned 旧代译文孤儿行已清理", "deleted", n)
	}
	h := assistapi.NewServer(adb, eng, assistLocalAdminToken(cfg, adb), cfg.CORSOrigin).Handler()
	api.InstallAssistLocal(eng, h)
	slog.Info("assist 本地引擎已装配（/assist-api/* 走进程内 handler，不再跨进程）",
		"db", cfg.DBPath)
}

// buildAssistLLM 照抄 cmd/assist-server 的降级链构造（主 + 备用，未配置即规则兜底）。
// 参数：cfg=assist 配置；返回：LLM 客户端。
func buildAssistLLM(cfg *assistconfig.Config) *assistllm.Client {
	if cfg.MockMode || cfg.LLMAPIKey == "" || cfg.LLMBaseURL == "" || cfg.LLMModel == "" {
		return assistllm.New(nil, cfg.LLMTimeoutSec)
	}
	providers := []assistllm.Provider{{
		Name: "main", BaseURL: cfg.LLMBaseURL, APIKey: cfg.LLMAPIKey, Model: cfg.LLMModel,
	}}
	if cfg.LLMModelBackup != "" {
		key := cfg.LLMAPIKey2
		if key == "" {
			key = cfg.LLMAPIKey
		}
		providers = append(providers, assistllm.Provider{
			Name: "backup", BaseURL: cfg.LLMBaseURL, APIKey: key, Model: cfg.LLMModelBackup,
		})
	}
	return assistllm.New(providers, cfg.LLMTimeoutSec)
}

// assistLocalAdminToken 三级解析：env → assist 自身 configs → 进程内随机值。
// 参数：cfg=assist 配置；adb=assist 存储；返回：非空 token。
func assistLocalAdminToken(cfg *assistconfig.Config, adb *assiststore.DB) string {
	if v := strings.TrimSpace(cfg.AdminToken); v != "" {
		return v
	}
	if v := strings.TrimSpace(adb.GetConfig("admin_token", "")); v != "" {
		return v
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "local-assist-internal-token"
	}
	return hex.EncodeToString(b)
}

// assistSeedFile 与 cmd/assist-server 的 SeedFile 同形（本地结构，避免跨 cmd 包依赖）。
type assistSeedFile struct {
	KB       []map[string]any  `json:"kb"`
	Scripts  []map[string]any  `json:"scripts"`
	Flows    []map[string]any  `json:"flows"`
	Features []map[string]any  `json:"features"`
	Configs  map[string]string `json:"configs"`
}

// loadAssistSeed 幂等灌入初始知识/话术/流程/功能入口（表非空即跳过，保护后台编辑）。
// 参数：adb=assist 存储；path=外置 seed 路径（可为空，回落二进制内嵌 seed）；返回：错误。
func loadAssistSeed(adb *assiststore.DB, path string) error {
	var raw []byte
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			raw = b
		}
	}
	if raw == nil && len(assistseed.SeedJSON) > 0 {
		raw = assistseed.SeedJSON
	}
	if raw == nil {
		return fmt.Errorf("无可用 assist seed（外置 %q 不可读且无内嵌 seed）", path)
	}
	var sf assistSeedFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return err
	}
	seedIfEmpty := func(table string, rows []map[string]any) {
		existing, _ := adb.List(table, false)
		if len(existing) > 0 || len(rows) == 0 {
			return
		}
		for _, r := range rows {
			for _, k := range []string{"priority", "sort", "enabled"} {
				if f, ok := r[k].(float64); ok {
					r[k] = int(f)
				}
			}
			if _, err := adb.Create(table, r); err != nil {
				slog.Warn("assist seed 行写入失败", "table", table, "err", err)
			}
		}
	}
	seedIfEmpty("kb_entries", sf.KB)
	seedIfEmpty("scripts", sf.Scripts)
	seedIfEmpty("flows", sf.Flows)
	seedIfEmpty("feature_links", sf.Features)
	if adb.SessionCount() == 0 {
		for k, v := range sf.Configs {
			if adb.GetConfig(k, "") == "" {
				_ = adb.SetConfig(k, v)
			}
		}
	}
	return nil
}
