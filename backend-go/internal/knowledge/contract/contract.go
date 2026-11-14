// ============ contract.go · 职责说明 ============
// 四期 W3-1（§4.3.4）：知识服务对外契约 v1 的**唯一真值**。
//
//   - 主服务客户端（internal/knowledge/client.go）与知识服务端（internal/knowledge/api）
//     只许引用本包的 DTO / 枚举 / contract_version / 路径，禁止各自定义一份。
//   - 每次不兼容改动必须抬 ContractVersion，并由 KN1（contract_gate_test.go）判三方等值。
//   - 知识服务零 LLM、零跨域：配额/积分/LLM/租户品牌一律不进本包（§4.3.1 第 3/4 条）。
//
// KN1 的「逐字段 + 逐枚举」判据：CanonicalFace() 用反射从真实 DTO 类型现算字段集，
// 从枚举清单现算枚举集——不写死字段名单（写死名单会随 DTO 改动静默漂移）。
// ================================================
package contract

import (
	"reflect"
	"sort"
)

// ContractVersion 契约版本；W3-1 冻结，之后每次不兼容改动必须抬版本并过 KN1。
const ContractVersion = "knowledge-contract-v1"

// ---- 端点路径（§4.3.4 十六行十七路径，含一条健康面）----
// client 的调用清单与 server 的路由清单都从 EndpointPaths 派生，不许另写字符串。
const (
	PathProductResolve        = "/internal/knowledge/product/resolve"
	PathProductResolveByCodes = "/internal/knowledge/product/resolve-by-codes"
	PathProductsList          = "/internal/knowledge/products/list"
	PathProductsGet    = "/internal/knowledge/products/get"
	PathProductsUpsert = "/internal/knowledge/products/upsert"
	PathProductsDelete = "/internal/knowledge/products/delete"
	PathSitemapList    = "/internal/knowledge/sitemap/list"
	PathCorpusPublic   = "/internal/knowledge/corpus/public"
	PathAnswer         = "/internal/knowledge/answer"
	PathFactsList      = "/internal/knowledge/facts/list"
	PathFactsUpsert    = "/internal/knowledge/facts/upsert"
	PathFactsAdmitted  = "/internal/knowledge/facts/admitted"
	PathAssetsList     = "/internal/knowledge/assets/list"
	PathAssetsUpsert   = "/internal/knowledge/assets/upsert"
	PathAssetsGet      = "/internal/knowledge/assets/get"
	PathReadiness      = "/internal/knowledge/readiness"
	PathI18nList       = "/internal/knowledge/i18n/list"
	PathI18nUpsert     = "/internal/knowledge/i18n/upsert"
	// PathI18nByTenantLang：按（tenant_id, lang）批量回该租户全部事实的译文（带 fact_id），
	// 供主服务 W1 热路径「一次取齐、按 fact_id 匹配」（避免逐 fact 调 i18n/list 的 N 次往返）。
	PathI18nByTenantLang = "/internal/knowledge/i18n/by_tenant_lang"
	PathEntriesQuery     = "/internal/knowledge/entries/query"
	PathHealth           = "/internal/health"
)

// EndpointPaths 全部契约路径（顺序稳定，供路由注册与客户端共用）。
var EndpointPaths = []string{
	PathProductResolve, PathProductResolveByCodes, PathProductsList, PathProductsGet, PathProductsUpsert,
	PathProductsDelete, PathSitemapList, PathCorpusPublic, PathAnswer,
	PathFactsList, PathFactsUpsert, PathFactsAdmitted, PathAssetsList, PathAssetsUpsert, PathAssetsGet,
	PathReadiness, PathI18nList, PathI18nUpsert, PathI18nByTenantLang, PathEntriesQuery, PathHealth,
}

// ---- 枚举（逐值冻结；新增值必须同批抬 ContractVersion 并过 KN1）----
const (
	ProductStatusDraft    = "draft"
	ProductStatusLive     = "live"
	ProductStatusArchived = "archived"
)
const (
	FactStatusUnverified = "unverified"
	FactStatusConfirmed  = "confirmed"
	FactStatusWithdrawn  = "withdrawn"
)
const (
	FactSourceManual      = "manual"
	FactSourceOCR         = "ocr"
	FactSourceExternalRef = "external_ref"
)
const (
	VisibilityPublic = "public"
	VisibilityGated  = "gated"
)
const (
	AssetTypeTagline     = "tagline"
	AssetTypeOnepager    = "onepager"
	AssetTypeQuoteSheet  = "quote_sheet"
	AssetTypeImageCard   = "image_card"
	AssetTypeVoiceScript = "voice_script"
	AssetTypeQAPack      = "qa_pack"
)
const (
	AssetReviewUnreviewed = "unreviewed"
	AssetReviewApproved   = "approved"
	AssetReviewRejected   = "rejected"
)
const (
	AssetValidityValid   = "valid"
	AssetValidityExpired = "expired"
	AssetValidityMissing = "missing"
)
const (
	I18nEngineManual     = "manual"
	I18nEngineIdentity   = "identity"
	I18nEngineSingleLang = "single_lang"
)
const (
	HealthOK          = "ok"
	HealthDegraded    = "degraded"
	HealthUnavailable = "unavailable"
)
const (
	// ReadModeShadow：W3-4 影子读，服务不可达回落直读 + WARN（仅本阶段）。
	// ReadModeCutover：W3-5 切读后，服务不可达必须 503，不许回落、不许假装空。
	ReadModeShadow  = "off"
	ReadModeCutover = "on"
)

// ReasonNotOwnedUntilW5：W3 期 entries/query 未接管，返回 503 + 该 reason（绝不返回空结果）。
const ReasonNotOwnedUntilW5 = "not_owned_until_w5"

var ProductStatuses = []string{ProductStatusDraft, ProductStatusLive, ProductStatusArchived}
var FactStatuses = []string{FactStatusUnverified, FactStatusConfirmed, FactStatusWithdrawn}
var FactSources = []string{FactSourceManual, FactSourceOCR, FactSourceExternalRef}
var Visibilities = []string{VisibilityPublic, VisibilityGated}
var AssetTypes = []string{
	AssetTypeTagline, AssetTypeOnepager, AssetTypeQuoteSheet,
	AssetTypeImageCard, AssetTypeVoiceScript, AssetTypeQAPack,
}
var AssetReviews = []string{AssetReviewUnreviewed, AssetReviewApproved, AssetReviewRejected}
var AssetValidities = []string{AssetValidityValid, AssetValidityExpired, AssetValidityMissing}
var I18nEngines = []string{I18nEngineManual, I18nEngineIdentity, I18nEngineSingleLang}
var HealthStates = []string{HealthOK, HealthDegraded, HealthUnavailable}
var ReadModes = []string{ReadModeShadow, ReadModeCutover}

// I18nLangs 12 语种内部码单一真值（§4.3.3 第 3 条：预生成/读口/回避句同一张表）。
// 主服务 translation 域与知识服务 i18n/list 都引用这一份，禁止另写 []string。
var I18nLangs = []string{
	"zh", "zh_hant", "en", "ru", "fr", "ar", "es", "pt", "de", "ja", "ko", "th",
}

// AllEnums 逐枚举清单（现算，供 KN1 逐枚举判等值）。
var AllEnums = map[string][]string{
	"product_status": ProductStatuses,
	"fact_status":    FactStatuses,
	"fact_source":    FactSources,
	"visibility":     Visibilities,
	"asset_type":     AssetTypes,
	"asset_review":   AssetReviews,
	"asset_validity": AssetValidities,
	"i18n_engine":    I18nEngines,
	"health_state":   HealthStates,
	"read_mode":      ReadModes,
	"i18n_lang":      I18nLangs,
}

// ---- DTO（唯一真值；client / server 只许引用这一份）----

// TenantScope 所有请求的租户作用域。TenantID<=0 一律 fail-closed 拒绝（KN3/KN13），
// 不靠"查不到就空"来兜——那会把越权读伪装成业务空结果。
type TenantScope struct {
	TenantID int64 `json:"tenant_id"`
}

// ProductRow 产品档案行（字段名/标记对齐主库 store.Product 出栈形态）。
type ProductRow struct {
	ID           string   `json:"id"`
	TenantID     int64    `json:"tenant_id"`
	UserID       int64    `json:"user_id"`
	Code         string   `json:"code"`
	Name         string   `json:"name"`
	Tagline      string   `json:"tagline"`
	IndustryCode string   `json:"industry_code"`
	Audience     string   `json:"audience"`
	UseCase      string   `json:"use_case"`
	StyleNotes   string   `json:"style_notes"`
	BannedNotes  string   `json:"banned_notes"`
	Status       string   `json:"status"`
	Slug         string   `json:"slug"`
	TenantCode   string   `json:"tenant_code"`
	PageCode     string   `json:"page_code"`
	SeoIndexable int      `json:"seo_indexable"`
	TargetLangs  []string `json:"target_langs"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}

// FactRow 事实项行。
type FactRow struct {
	ID         int64  `json:"id"`
	TenantID   int64  `json:"tenant_id"`
	ProductID  string `json:"product_id"`
	Kind       string `json:"kind"`
	Key        string `json:"key"`
	Value      string `json:"value"`
	Status     string `json:"status"`
	Visibility string `json:"visibility"`
	Source     string `json:"source"`
	Lang       string `json:"lang"`
	Priority   int    `json:"priority"`
	Locked     int    `json:"locked"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// AssetRow 物料行。
type AssetRow struct {
	ID           int64  `json:"id"`
	TenantID     int64  `json:"tenant_id"`
	ProductID    string `json:"product_id"`
	AssetType    string `json:"asset_type"`
	Lang         string `json:"lang"`
	Body         string `json:"body"`
	ArtifactRef  string `json:"artifact_ref"`
	ReviewStatus string `json:"review_status"`
	Validity     string `json:"validity"`
	Visibility   string `json:"visibility"`
	FactRefs     string `json:"fact_refs"`
	SourceKind   string `json:"source_kind"`
	Version      int    `json:"version"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// I18nItem 每语种译文现状（有无 / engine / source_hash），供主服务"只翻缺的"。
type I18nItem struct {
	Lang       string `json:"lang"`
	Present    bool   `json:"present"`
	Engine     string `json:"engine"`
	SourceHash string `json:"source_hash"`
}

// I18nListRequest / I18nListResponse：i18n/list 契约。
type I18nListRequest struct {
	TenantID int64 `json:"tenant_id"`
	FactID   int64 `json:"fact_id"`
}
type I18nListResponse struct {
	Items []I18nItem `json:"items"`
}

// I18nUpsertRequest / I18nUpsertResponse：i18n/upsert 契约（零 LLM，纯回写）。
type I18nUpsertRequest struct {
	TenantID   int64  `json:"tenant_id"`
	FactID     int64  `json:"fact_id"`
	Lang       string `json:"lang"`
	Value      string `json:"value"`
	SourceHash string `json:"source_hash"`
	Engine     string `json:"engine"`
}
type I18nUpsertResponse struct {
	OK bool `json:"ok"`
}

// I18nByTenantLangItem 一条（fact_id, lang）译文现状（带 fact_id，供主服务按 fact_id 匹配）。
type I18nByTenantLangItem struct {
	FactID     int64  `json:"fact_id"`
	Lang       string `json:"lang"`
	Value      string `json:"value"`
	SourceHash string `json:"source_hash"`
	Engine     string `json:"engine"`
	Present    bool   `json:"present"`
}

// I18nByTenantLangRequest / I18nByTenantLangResponse：i18n/by_tenant_lang 契约。
// 按（tenant_id, lang）一次回该租户该语种全部事实的译文（带 fact_id），
// 供主服务 W1 热路径「一次取齐、按 fact_id 匹配」，避免逐 fact 调 i18n/list 的 N 次往返。
type I18nByTenantLangRequest struct {
	TenantID int64  `json:"tenant_id"`
	Lang     string `json:"lang"`
}
type I18nByTenantLangResponse struct {
	Items []I18nByTenantLangItem `json:"items"`
}

// EntriesQueryRequest / EntriesQueryResponse：W3 期未接管，服务只回 503+reason。
type EntriesQueryRequest struct {
	TenantID int64    `json:"tenant_id"`
	Kinds    []string `json:"kinds"`
	Query    string   `json:"query"`
}
type EntriesQueryResponse struct {
	Items []map[string]any `json:"items"`
}

// HealthResponse：/internal/health 就绪/版本/contract_version（KN9 三态）。
type HealthResponse struct {
	Status          string `json:"status"`
	Version         string `json:"version"`
	ContractVersion string `json:"contract_version"`
}

// ErrorResponse：所有 4xx/5xx 的统一形态（含 reason，供 KN4 四档判别）。
type ErrorResponse struct {
	Error  string `json:"error"`
	Reason string `json:"reason,omitempty"`
}

// ListRequest 分页/单产品/语料等读口的通用作用域入参。
type ListRequest struct {
	TenantID  int64  `json:"tenant_id"`
	ProductID string `json:"product_id"`
	Lang      string `json:"lang"`
	Page      int    `json:"page"`
	Limit     int    `json:"limit"`
}

// AnswerPayload 是「素材包」不是答案文本：主服务把 product/facts/assets/readiness/gated_count
// 交给 knowledge，knowledge 回同一结构（§4.3.4）。主服务不许自己再算一遍 gated_count。
type AnswerPayload struct {
	TenantID   int64            `json:"tenant_id"`
	Product    *ProductRow      `json:"product"`
	Facts      []FactRow        `json:"facts"`
	Assets     []AssetRow       `json:"assets"`
	Readiness  []map[string]any `json:"readiness"`
	GatedCount int              `json:"gated_count"`
}

// ---- Face：三方等值的可比对面（KN1）----

// Face 契约面：版本 + 路径集 + 枚举集 + DTO 字段集。
type Face struct {
	Version   string              `json:"version"`
	Paths     []string            `json:"paths"`
	Enums     map[string][]string `json:"enums"`
	DTOFields map[string][]string `json:"dto_fields"`
}

// canonicalDTOs 需要纳入逐字段判等的 DTO（现算字段集，不写死名单）。
var canonicalDTOs = map[string]reflect.Type{
	"TenantScope":              reflect.TypeOf(TenantScope{}),
	"ProductRow":               reflect.TypeOf(ProductRow{}),
	"FactRow":                  reflect.TypeOf(FactRow{}),
	"AssetRow":                 reflect.TypeOf(AssetRow{}),
	"I18nItem":                 reflect.TypeOf(I18nItem{}),
	"I18nListRequest":          reflect.TypeOf(I18nListRequest{}),
	"I18nListResponse":         reflect.TypeOf(I18nListResponse{}),
	"I18nUpsertRequest":        reflect.TypeOf(I18nUpsertRequest{}),
	"I18nUpsertResponse":       reflect.TypeOf(I18nUpsertResponse{}),
	"I18nByTenantLangItem":     reflect.TypeOf(I18nByTenantLangItem{}),
	"I18nByTenantLangRequest":  reflect.TypeOf(I18nByTenantLangRequest{}),
	"I18nByTenantLangResponse": reflect.TypeOf(I18nByTenantLangResponse{}),
	"EntriesQueryRequest":      reflect.TypeOf(EntriesQueryRequest{}),
	"EntriesQueryResponse":     reflect.TypeOf(EntriesQueryResponse{}),
	"HealthResponse":           reflect.TypeOf(HealthResponse{}),
	"ErrorResponse":            reflect.TypeOf(ErrorResponse{}),
	"ListRequest":              reflect.TypeOf(ListRequest{}),
	"AnswerPayload":            reflect.TypeOf(AnswerPayload{}),
}

// dtoFields 用反射从真实 DTO 类型现算字段名集（含 json tag），按名排序保证稳定。
func dtoFields() map[string][]string {
	out := make(map[string][]string, len(canonicalDTOs))
	for name, t := range canonicalDTOs {
		fs := make([]string, 0, t.NumField())
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			fs = append(fs, f.Name+"|"+f.Tag.Get("json"))
		}
		sort.Strings(fs)
		out[name] = fs
	}
	return out
}

// CanonicalFace 返回契约包的规范面（唯一真值）。client.Face() / api.ServerFace()
// 必须与此逐字段、逐枚举、逐路径等值（KN1）。
func CanonicalFace() Face {
	enums := make(map[string][]string, len(AllEnums))
	for k, v := range AllEnums {
		enums[k] = append([]string(nil), v...)
	}
	return Face{
		Version:   ContractVersion,
		Paths:     append([]string(nil), EndpointPaths...),
		Enums:     enums,
		DTOFields: dtoFields(),
	}
}
