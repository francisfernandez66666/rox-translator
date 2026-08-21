// ============ 本文件职责中文说明 ============
// 运行时配置：定义 LLM 供应商路由（ProviderConfig / ModelRoutes 多模型路由权重策略）、
// 翻译/Embedding 模型、模型降级（Hunyuan fallback、首调用超时、熔断阈值）、
// 相似度阈值、目录路径、采样参数等；提供 Default() 默认配置（密钥全部来自环境变量，
// 未配置时生成随机临时值并打告警，不泄露任何硬编码密钥），
// 以及从 config.json / 环境变量覆盖配置的加载逻辑。
// ========================================
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// ProviderConfig LLM 供应商路由项（模型路由策略）
type ProviderConfig struct {
	Provider string `json:"provider"` // 供应商标识（用于计量成本核算）
	APIBase  string `json:"api_base"` // 供应商 API 基地址
	APIKey   string `json:"api_key"`  // 供应商 API 密钥
	Model    string `json:"model"`    // 该供应商下使用的模型名
	Weight   int    `json:"weight"`   // 权重，越高越优先（0 表示仅作 fallback）
}

// 流程阶段标识（stage_models 的键）
const (
	StageKBMatch   = "kb_match"   // 知识库匹配兜底翻译
	StageAIInitial = "ai_initial" // 初翻
	StageEvals     = "evals"      // LLM-as-Judge 评估（旧键，兼容保留）
	StageReview    = "review"     // 审校

	// 业务五阶段（面向流程的模型配置）
	StageKBEmbed      = "kb_embed"       // 知识库 Embed 向量模型
	StageInitialEvals = "initial_evals"  // 初翻 Evals 评估模型
	StageReviewEvals  = "review_evals"   // 校对 Evals 评估模型
)

// StageModel 单个流程阶段的模型配置（stage_models 中的一项）
type StageModel struct {
	Provider string `json:"provider"` // 供应商标识（计量分组用，可为空自动推断）
	APIBase  string `json:"api_base"` // 该阶段 LLM API 基地址（必填才启用该阶段独立模型）
	APIKey   string `json:"api_key"`  // 该阶段 API 密钥（为空继承全局默认密钥）
	Model    string `json:"model"`    // 该阶段使用的模型名（必填才启用该阶段独立模型）
}

// StageModels 各流程阶段模型配置映射（system_config 键 "stage_models"）
// 键为流程阶段标识（kb_match / ai_initial / evals / review）。
type StageModels map[string]StageModel

// Config 保存运行时配置（等价于 Python lib.py 的模块级配置）
type Config struct {
	// 翻译 LLM（SiliconFlow）
	OnlineAPIBase string // 在线翻译 API 基地址
	OnlineAPIKey  string // 在线翻译 API 密钥
	OnlineModel   string // 在线翻译默认模型
	OnlineTimeout int    // 在线调用超时秒数

	// 模型路由策略：按权重选主模型，失败后按顺序降级。空则用 Online* 单供应商。
	ModelRoutes []ProviderConfig // 多供应商路由列表（权重路由/降级链）

	// Embedding（智谱）
	EmbedAPIBase string // Embedding API 基地址
	EmbedAPIKey  string // Embedding API 密钥

	// 模型降级
	HunyuanMTModel       string          // Hunyuan 多语翻译模型名
	HunyuanFallbackModel string          // Hunyuan 不支持语种或失败时的降级模型
	HunyuanMTLangCode    map[string]bool // Hunyuan 支持的目标语言代码集合
	// HunyuanFirstTimeoutSec Hunyuan 主模型首次调用超时秒数；超时立即降级到 fallback
	HunyuanFirstTimeoutSec int

	// 主模型熔断恢复
	BreakerThreshold   int // 连续失败多少次触发熔断（默认 5）
	BreakerCoolDownSec int // 熔断冷却秒数（默认 1800）

	// 用户数据目录
	UserDataDir string // 用户数据根目录
	DBPath      string // 知识库 SQLite 路径
	EmbPath     string // 向量文件路径（npz）
	IndexStamp  string // 索引时间戳文件路径

	// 上传/输出目录
	UploadDir string // 上传文件目录
	OutputDir string // 输出文件目录

	// 采样参数
	HunyuanTemp       float64 // 主模型温度
	HunyuanTopP       float64 // 主模型 top_p
	HunyuanTopK       int     // 主模型 top_k
	HunyuanRepetition float64 // 主模型重复惩罚
	FallbackTemp      float64 // 降级模型温度（低温度保证确定性）

	// 相似度阈值
	HighSim  float64 // 语义命中高相似阈值（默认 0.90）
	MedSim   float64 // 参考例句中等相似阈值（默认 0.75）
	TopK     int     // 语义检索返回条数
	TopFuzzy int     // 模糊匹配返回条数

	// 语义命中要求命中行中文与输入达到的 CJK 字符重叠率；
	// 低于该值视为"语义相近但非同一句"，不直接替换翻译，改走模型
	SemHitCharOverlap float64 // CJK 字符重叠率下限（默认 0.55）

	// 管理后台访问凭证（租户管理接口鉴权）
	AdminToken string

	// CORS 允许的跨域来源（同源部署无需配置；前后端分离时通过 CORS_ALLOWED_ORIGINS 环境变量指定，逗号分隔）
	CORSOrigins []string
}

// HunyuanMTLangSet 返回支持的语言代码集合
func HunyuanMTLangSet() map[string]bool {
	return map[string]bool{
		"zh": true, "en": true, "fr": true, "pt": true, "es": true, "ja": true,
		"tr": true, "ru": true, "ar": true, "ko": true, "th": true, "it": true,
		"de": true, "vi": true, "ms": true, "id": true, "tl": true, "hi": true,
		"zh_hant": true, "pl": true, "cs": true, "nl": true, "km": true,
		"my": true, "fa": true, "gu": true, "ur": true, "te": true, "mr": true,
		"he": true, "bn": true, "ta": true, "uk": true, "bo": true, "kk": true,
		"mn": true, "ug": true, "yue": true,
	}
}

// TRANSLATE_LANGS：知识库完整覆盖的 9 种语言
var TranslateLangs = []string{"en", "ru", "ar", "es", "pt", "fr", "kk", "de", "zh_hant"}

// ALL_LANGS：DB 语言列（16 列）
var AllLangs = []string{"en", "ru", "ar", "es", "pt", "fr", "kk", "de", "zh_hant",
	"ms", "id_lang", "th", "tr", "it", "pl", "sv"}

// LangNames：语言代码 → 中文名
var LangNames = map[string]string{
	"en": "英语", "ru": "俄语", "ar": "阿拉伯语", "es": "西班牙语", "pt": "葡萄牙语",
	"fr": "法语", "kk": "哈萨克语", "de": "德语", "zh_hant": "繁体中文",
	"ms": "马来语", "id_lang": "印尼语", "th": "泰语", "tr": "土耳其语",
	"it": "意大利语", "pl": "波兰语", "sv": "瑞典语",
	"ja": "日语", "ko": "韩语", "mn": "蒙古语", "vi": "越南语", "id": "印尼语",
	"nl": "荷兰语", "uk": "乌克兰语", "hi": "印地语", "fa": "波斯语", "he": "希伯来语",
	"el": "希腊语", "my": "缅甸语", "km": "柬埔寨语", "lo": "老挝语", "tl": "菲律宾语",
	"gu": "古吉拉特语", "ur": "乌尔都语", "te": "泰卢固语", "mr": "马拉地语",
	"bn": "孟加拉语", "ta": "泰米尔语", "bo": "藏语", "ug": "维吾尔语", "yue": "粤语",
}

// KBFlag：KB 语言对应的国旗
var Flags = map[string]string{
	"en": "🇬🇧", "ru": "🇷🇺", "ar": "🇸🇦", "es": "🇪🇸", "pt": "🇵🇹",
	"fr": "🇫🇷", "kk": "🇰🇿", "de": "🇩🇪", "zh_hant": "🇹🇼",
}

// Default 返回默认配置（密钥一律从环境变量读取，未配置时生成随机临时值并告警）
func Default() *Config {
	c := &Config{
		OnlineAPIBase:          "https://api.siliconflow.cn/v1",
		OnlineAPIKey:           os.Getenv("SILICONFLOW_API_KEY"),
		OnlineModel:            "tencent/Hunyuan-MT-7B",
		OnlineTimeout:          120,
		EmbedAPIBase:           "https://open.bigmodel.cn/api/paas/v4",
		EmbedAPIKey:            os.Getenv("ONLINE_API_KEY"),
		HunyuanMTModel:         "tencent/Hunyuan-MT-7B",
		HunyuanFallbackModel:   "THUDM/GLM-4-9B-0414",
		HunyuanFirstTimeoutSec: 30,
		BreakerThreshold:       5,
		BreakerCoolDownSec:     1800,
		HunyuanMTLangCode:      HunyuanMTLangSet(),
		HunyuanTemp:            0.7,
		HunyuanTopP:            0.6,
		HunyuanTopK:            20,
		HunyuanRepetition:      1.05,
		FallbackTemp:           0.1,
		HighSim:                0.90,
		MedSim:                 0.75,
		TopK:                   4,
		TopFuzzy:               3,
		SemHitCharOverlap:      0.55,
		AdminToken:             "",
		// 默认仅允许本地开发来源；生产同源部署（Caddy 反代）不受影响
		CORSOrigins: []string{"http://localhost:5173", "http://127.0.0.1:5173", "http://localhost:8080"},
	}

	// ★ 密钥安全：不再内置任何硬编码密钥。
	// 未配置环境变量时生成随机临时值，保证进程可用但不泄露真实凭证；
	// 随机 Key 调用外部 LLM 会鉴权失败，属预期行为（提示通过环境变量配置）。
	if c.OnlineAPIKey == "" {
		c.OnlineAPIKey = "sk-" + randHex(16) // 随机占位，避免硬编码
		log.Println("[config] 警告: 未配置 SILICONFLOW_API_KEY，已生成随机占位 Key（LLM 调用将失败）")
	}
	if c.EmbedAPIKey == "" {
		c.EmbedAPIKey = randHex(24) // 随机占位
		log.Println("[config] 警告: 未配置 ONLINE_API_KEY，已生成随机占位 Key（Embedding 调用将失败）")
	}
	if v := os.Getenv("ADMIN_TOKEN"); v != "" {
		c.AdminToken = v
	} else {
		// 未配置时随机生成管理凭证，避免默认值泄露
		c.AdminToken = randHex(24)
		log.Println("[config] 警告: 未配置 ADMIN_TOKEN，已生成随机管理凭证（回调校验将无法通过）")
	}
	if v := os.Getenv("CORS_ALLOWED_ORIGINS"); v != "" {
		// 逗号分隔的来源列表；空项剔除
		var origins []string
		for _, o := range strings.Split(v, ",") {
			if o = strings.TrimSpace(o); o != "" {
				origins = append(origins, o)
			}
		}
		c.CORSOrigins = origins
	}
	if v := os.Getenv("ONLINE_API_BASE"); v != "" {
		c.EmbedAPIBase = v
	}
	if v := os.Getenv("ONLINE_MODEL"); v != "" {
		c.OnlineModel = v
		c.HunyuanMTModel = v
	}
	if v := os.Getenv("USER_DATA_DIR"); v != "" {
		c.UserDataDir = v
	}
	if c.UserDataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		c.UserDataDir = filepath.Join(home, "Library", "Application Support", "翻译助手")
	}
	c.DBPath = filepath.Join(c.UserDataDir, "tm.sqlite3")
	c.EmbPath = filepath.Join(c.UserDataDir, "tm_embeddings.npz")
	c.IndexStamp = filepath.Join(c.UserDataDir, ".index_stamp")
	c.UploadDir = filepath.Join(c.UserDataDir, "_uploads")
	c.OutputDir = filepath.Join(c.UserDataDir, "_output")
	return c
}

// LoadConfigFromJSON 尝试从项目根/可执行目录加载 config.json（model 字段）
func (c *Config) LoadConfigFromJSON(exeDir string) {
	paths := []string{
		filepath.Join(exeDir, "config.json"),
		filepath.Join(exeDir, "..", "config.json"),
		"config.json",
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var m map[string]interface{}
		if json.Unmarshal(data, &m) == nil {
			if model, ok := m["model"].(string); ok && model != "" {
				c.OnlineModel = model
				c.HunyuanMTModel = model
			}
		}
		return
	}
}

// randHex 生成 n 字节随机数并以十六进制字符串返回（密钥占位 / 临时凭证生成）。
func randHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// 随机源不可用时回退固定值（仅用于占位，不用于安全边界）
		return strings.Repeat("0", n*2)
	}
	return hex.EncodeToString(buf)
}
