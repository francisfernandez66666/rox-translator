// Package config 环境变量配置。所有项均有默认值，零配置即可本地启动（mock 模式）。
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config 服务配置
type Config struct {
	Addr       string // 监听地址
	DBPath     string // SQLite 文件路径
	AdminToken string // 管理后台鉴权 token
	CORSOrigin string // 允许跨域来源，逗号分隔，* 表示全部
	SeedFile   string // 初始知识库 seed 文件（可为空：回落二进制内嵌 seed）
	WebDir     string // 管理页静态目录（可为空：回落二进制内嵌页面）
	// ★ 改造 1A（2026-09-17）：主服务 SQLite 路径。用于只读桥接 system_config.assist_admin_token，
	// 实现管理台 Token 免手填（与主后台 /api/admin/assist/token 同源）。
	// 主服务切 PostgreSQL 时不适用，需以 env ASSIST_ADMIN_TOKEN 为准。
	MainDBPath string
	// LLM 配置：[OI]-compatible（硅基流动/智谱/OpenAI 兼容均可）
	LLMBaseURL     string // 如 https://api.siliconflow.cn/v1
	LLMAPIKey      string // 主模型 key
	LLMModel       string // 主模型名
	LLMModelBackup string // 备用模型名（同 base_url 可为空）
	LLMAPIKey2     string // 备用模型 key（可复用主 key）
	LLMTimeoutSec  int    // 单模型调用超时秒
	MockMode       bool   // true 时不调 LLM，直接走规则兜底（本地开发/演示）
}

// getenv 读环境变量（trim 后空值回落默认）
func getenv(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

// getint 读整型环境变量（非法值回落默认）
func getint(k string, def int) int {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// getbool 读布尔环境变量（1/true/yes/on 均为真）
func getbool(k string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(k)))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

// Load 读取配置
func Load() *Config {
	return &Config{
		Addr:           getenv("ASSIST_ADDR", "127.0.0.1:8790"),
		DBPath:         getenv("ASSIST_DB", "data/assist.db"),
		AdminToken:     getenv("ASSIST_ADMIN_TOKEN", "change-me-please"),
		CORSOrigin:     getenv("ASSIST_CORS", "*"),
		SeedFile:       getenv("ASSIST_SEED", ""),
		WebDir:         getenv("ASSIST_WEB", ""),
		MainDBPath:     defaultMainDBPath(),
		LLMBaseURL:     getenv("ASSIST_LLM_BASE_URL", ""),
		LLMAPIKey:      getenv("ASSIST_LLM_API_KEY", ""),
		LLMModel:       getenv("ASSIST_LLM_MODEL", ""),
		LLMModelBackup: getenv("ASSIST_LLM_MODEL_BACKUP", ""),
		LLMAPIKey2:     getenv("ASSIST_LLM_API_KEY_BACKUP", ""),
		LLMTimeoutSec:  getint("ASSIST_LLM_TIMEOUT", 45),
		MockMode:       getbool("ASSIST_MOCK"),
	}
}

// defaultMainDBPath 推导主服务 SQLite 路径（★ 改造 1A）。
// 优先 MAIN_DB 显式配置；否则按主服务同款规则由 USER_DATA_DIR（未设则默认
// <home>/Library/Application Support/能言）拼出 tm.sqlite3；主服务以 PostgreSQL
// 运行（DB_DRIVER=postgres）时该路径无意义，调用方读不到即回落 env Token。
func defaultMainDBPath() string {
	if v := strings.TrimSpace(os.Getenv("MAIN_DB")); v != "" {
		return v
	}
	// 显式声明 PostgreSQL 后端：不存在可读的 SQLite 主库，直接返回空
	if strings.ToLower(strings.TrimSpace(os.Getenv("DB_DRIVER"))) == "postgres" {
		return ""
	}
	dir := strings.TrimSpace(os.Getenv("USER_DATA_DIR"))
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, "Library", "Application Support", "能言")
	}
	return filepath.Join(dir, "tm.sqlite3")
}
