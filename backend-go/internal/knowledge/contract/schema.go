// ============ schema.go · 职责说明 ============
// 四期 W3：知识四表结构的**单一真值**（列名 + 顺序）。
// 建表（knowledge/store.KnowledgeMigrate）、搬迁（migrate.go）、反向搬迁
// （reverse_migrate.go）、双向指纹（行数 + 每表拼接哈希）全部从这里取列清单，
// 任何一处漏列都会让指纹不等 ⇒ 迁移返回 error（KN8/KN10 的判别力来源）。
//
// ★ 列顺序＝主键优先；products.id 是 TEXT（显式 id），其余三表 id 是自增整型。
//   lang/priority（product_facts）与 target_langs（products）原本是后补列，
//   知识侧 CREATE TABLE 直接带，旧库由 db.EnsureColumns 幂等补齐（§4.3.3 第 4 条）。
// ================================================
package contract

const (
	TableProducts        = "products"
	TableProductFacts    = "product_facts"
	TableProductAssets   = "product_assets"
	TableProductFactI18n = "product_fact_i18n"
)

// KnowledgeTableNames 知识服务 W3 期拥有的四张表（W5 再加 kb_entries/scripts/flows/feature_links）。
var KnowledgeTableNames = []string{
	TableProducts, TableProductFacts, TableProductAssets, TableProductFactI18n,
}

// AutoIncrementTables 三张带整型自增主键的表（PG setval 复位目标；products.id 是 TEXT 不复位）。
var AutoIncrementTables = []string{
	TableProductFacts, TableProductAssets, TableProductFactI18n,
}

// TableColumns 四表完整列清单（顺序固定）。指纹永远按这份**全量**清单算，
// 与搬迁时用的列切片互相独立——漏搬一列 ⇒ 目标库该列为默认旧值 ⇒ 指纹不等 ⇒ 红。
var TableColumns = map[string][]string{
	TableProducts: {
		"id", "tenant_id", "user_id", "code", "name", "tagline", "industry_code",
		"audience", "use_case", "style_notes", "banned_notes", "status", "slug",
		"tenant_code", "page_code", "seo_indexable", "created_at", "updated_at",
		"target_langs",
	},
	TableProductFacts: {
		"id", "tenant_id", "product_id", "kind", "key", "value", "status",
		"visibility", "source", "locked", "created_at", "updated_at", "lang", "priority",
	},
	TableProductAssets: {
		"id", "tenant_id", "product_id", "asset_type", "lang", "body", "artifact_ref",
		"review_status", "validity", "visibility", "fact_refs", "source_kind", "version",
		"created_at", "updated_at",
	},
	TableProductFactI18n: {
		"id", "tenant_id", "fact_id", "lang", "value", "source_hash", "translated_at", "engine",
	},
}

// ColumnsFor 返回某表完整列清单的拷贝（调用方可安全改写，不污染全局单一真值）。
func ColumnsFor(table string) []string {
	return append([]string(nil), TableColumns[table]...)
}

// PrimaryKey 返回某表主键列名（幂等 upsert 的冲突判定列，四表都是 id）。
func PrimaryKey(table string) string { return "id" }
