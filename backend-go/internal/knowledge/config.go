// ============ config.go · 职责说明 ============
// 四期 W3（§4.3.1 第 1 条 / KN12）：知识服务独立库、独立 DSN 的启动期硬断言与配置读取。
//
//   - env 命名照 internal/config/config.go:181-184 / 425-431 的 DatabaseDriver/DatabaseDSN 同形：
//     KNOWLEDGE_DB_DRIVER / KNOWLEDGE_DB_DSN，主库侧 DB_DRIVER / DB_DSN。
//   - 启动期由 AssertDistinct(mainDSN, knowledgeDSN) 硬断言两者**不相等**，相等即拒绝启动。
//     禁止「同库不同进程」的过渡态（§4.3.2 五阶段里不存在那一档）。
//   - 知识服务进程只连 KNOWLEDGE_DB_DSN；主服务进程不持有知识库连接。
//
// KN12 三层判定：空 / 相等 / 归一化后相等（去尾 `/`、大小写、host:port、localhost↔127.0.0.1）。
// ⚠️ 归一化**不许**宽到把 localhost 与 127.0.0.1 判成不同：那两个在本机是同一个库，
//    「看着不同、实际同库」正是这条闸要拦的东西（§4.3.5）。
// ================================================
package knowledge

import (
	"fmt"
	"os"
	"strings"
)

// env 绑定（照 config.go:425-431 的形态，只新增 knowledge 侧两个）。
const (
	EnvKnowledgeDriver = "KNOWLEDGE_DB_DRIVER"
	EnvKnowledgeDSN    = "KNOWLEDGE_DB_DSN"
	EnvMainDriver      = "DB_DRIVER"
	EnvMainDSN         = "DB_DSN"
	EnvBaseURL         = "KNOWLEDGE_BASE_URL"
	EnvToken           = "KNOWLEDGE_TOKEN"
	EnvServiceRead     = "KNOWLEDGE_SERVICE_READ"
	EnvAddr            = "KNOWLEDGE_ADDR"

	// DefaultAddr 只绑回环，不挂公网、不进 Caddy 反代（§4.3.1 安全条）。
	DefaultAddr    = "127.0.0.1:8791"
	DefaultBaseURL = "http://127.0.0.1:8791"
	// DefaultKnowledgeDriver 独立库默认驱动；生产两库都是 postgres，本地测试 sqlite。
	DefaultKnowledgeDriver = "sqlite"
)

// Config 启动期配置（知识服务端与主服务客户端共用一份字段定义）。
type Config struct {
	KnowledgeDriver string
	KnowledgeDSN    string
	MainDriver      string
	MainDSN         string
	BaseURL         string
	Token           string
	// ServiceRead：KNOWLEDGE_SERVICE_READ=on ⇒ 切读（失败必须 503，不许回落/假装空）。
	//             =off（默认）⇒ 影子期（失败回落直读 + WARN，仅 W3-4）。
	ServiceRead bool
	Addr        string
}

// LoadConfig 从环境变量读取全部配置（缺省值只补 addr/baseURL/driver，DSN 绝不补默认值——
// 缺 DSN 由 AssertDistinct 在启动期拒启，不静默用主库顶上）。
func LoadConfig() Config {
	c := Config{
		KnowledgeDriver: envOr(EnvKnowledgeDriver, DefaultKnowledgeDriver),
		KnowledgeDSN:    strings.TrimSpace(os.Getenv(EnvKnowledgeDSN)),
		MainDriver:      envOr(EnvMainDriver, "sqlite"),
		MainDSN:         strings.TrimSpace(os.Getenv(EnvMainDSN)),
		BaseURL:         envOr(EnvBaseURL, DefaultBaseURL),
		Token:           os.Getenv(EnvToken),
		Addr:            envOr(EnvAddr, DefaultAddr),
		ServiceRead:     strings.EqualFold(strings.TrimSpace(os.Getenv(EnvServiceRead)), contractReadOn),
	}
	return c
}

// contractReadOn 切读开关字面值（与 contract.ReadModeCutover 同值，但不引入循环 import 依赖）。
const contractReadOn = "on"

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// normalizeDSN 归一 DSN 用于「是否同一个库」判定：
//   - trim 空格、整体小写（密码大小写可能被吃掉，但宁可误拒不可误放——相等即拒启是本闸的方向）；
//   - 去尾部 `/`；
//   - localhost → 127.0.0.1（★ 不许宽到把两者判成不同，本机同库必须拦下）；
//   - 去掉 PG 默认端口 `:5432`，让 host 与 host:5432 同形。
func normalizeDSN(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSuffix(s, "/")
	s = strings.ReplaceAll(s, "localhost", "127.0.0.1")
	s = strings.ReplaceAll(s, ":5432", "")
	return s
}

// AssertDistinct 双 DSN 不等硬断言（KN12）。
// 返回非 nil ⇒ 启动必须拒绝（把原因写进日志/journal，不是静默退出）。
// 参数：mainDSN=主库 DSN；knowledgeDSN=知识库 DSN；返回拒绝原因（nil=允许启动）。
func AssertDistinct(mainDSN, knowledgeDSN string) error {
	if strings.TrimSpace(mainDSN) == "" || strings.TrimSpace(knowledgeDSN) == "" {
		return fmt.Errorf("KNOWLEDGE_DB_DSN 与 DB_DSN 均不得为空（独立库/独立 DSN 硬边界 KN12）")
	}
	a, b := normalizeDSN(mainDSN), normalizeDSN(knowledgeDSN)
	if a == b {
		return fmt.Errorf("KNOWLEDGE_DB_DSN 与 DB_DSN 指向同一个库（归一化=%q），拒绝启动（KN12）", a)
	}
	return nil
}

// StartupGuard 启动期一次性校验：双 DSN 不等 + 切读档位日志所需信息齐备。
// 主服务与知识服务两站启动链都要调用（接线清单见 /tmp/w3_wiring.md）。
func StartupGuard(cfg Config) error {
	return AssertDistinct(cfg.MainDSN, cfg.KnowledgeDSN)
}
