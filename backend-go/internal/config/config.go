package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config 保存运行时配置（等价于 Python lib.py 的模块级配置）
type Config struct {
	// 翻译 LLM（SiliconFlow）
	OnlineAPIBase string
	OnlineAPIKey  string
	OnlineModel   string
	OnlineTimeout int

	// Embedding（智谱）
	EmbedAPIBase string
	EmbedAPIKey  string

	// 模型降级
	HunyuanMTModel       string
	HunyuanFallbackModel string
	HunyuanMTLangCode    map[string]bool
	// HunyuanFirstTimeoutSec Hunyuan 主模型首次调用超时秒数；超时立即降级到 fallback
	HunyuanFirstTimeoutSec int

	// 主模型熔断恢复
	BreakerThreshold  int // 连续失败多少次触发熔断（默认 5）
	BreakerCoolDownSec int // 熔断冷却秒数（默认 1800）

	// 用户数据目录
	UserDataDir string
	DBPath      string
	EmbPath     string
	IndexStamp  string

	// 上传/输出目录
	UploadDir string
	OutputDir string

	// 采样参数
	HunyuanTemp      float64
	HunyuanTopP      float64
	HunyuanTopK      int
	HunyuanRepetition float64
	FallbackTemp     float64

	// 相似度阈值
	HighSim  float64
	MedSim   float64
	TopK     int
	TopFuzzy int

	// 语义命中要求命中行中文与输入达到的 CJK 字符重叠率；
	// 低于该值视为"语义相近但非同一句"，不直接替换翻译，改走模型
	SemHitCharOverlap float64

	// 管理后台访问凭证（租户管理接口鉴权）
	AdminToken string
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

// Default 返回默认配置（带内置硬编码 Key）
func Default() *Config {
	c := &Config{
		OnlineAPIBase:      "https://api.siliconflow.cn/v1",
		OnlineAPIKey:       os.Getenv("SILICONFLOW_API_KEY"),
		OnlineModel:        "tencent/Hunyuan-MT-7B",
		OnlineTimeout:      120,
		EmbedAPIBase:       "https://open.bigmodel.cn/api/paas/v4",
		EmbedAPIKey:        os.Getenv("ONLINE_API_KEY"),
		HunyuanMTModel:      "tencent/Hunyuan-MT-7B",
		HunyuanFallbackModel: "THUDM/GLM-4-9B-0414",
		HunyuanFirstTimeoutSec: 30,
		BreakerThreshold:       5,
		BreakerCoolDownSec:     1800,
		HunyuanMTLangCode:  HunyuanMTLangSet(),
		HunyuanTemp:        0.7,
		HunyuanTopP:        0.6,
		HunyuanTopK:        20,
		HunyuanRepetition:  1.05,
		FallbackTemp:       0.1,
		HighSim:            0.90,
		MedSim:             0.75,
		TopK:               4,
		TopFuzzy:           3,
		SemHitCharOverlap:  0.55,
		AdminToken:         "rox-admin-2026",
	}

	// 内置默认 Key（编译进二进制）
	if c.OnlineAPIKey == "" {
		c.OnlineAPIKey = "***REMOVED***"
	}
	if c.EmbedAPIKey == "" {
		c.EmbedAPIKey = "***REMOVED***"
	}
	if v := os.Getenv("ADMIN_TOKEN"); v != "" {
		c.AdminToken = v
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
