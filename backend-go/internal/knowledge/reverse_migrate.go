// ============ reverse_migrate.go · 职责说明 ============
// 四期 W3-5 回滚（§4.3.8 / KN10）：知识库 → 主库反向幂等搬迁。
//
//   - 唯一合法回滚源是**知识库**（切读后主库四表停写，"冻结的主库副本"会把
//     W3-4 之后的新事实静默丢掉 ⇒「回滚成功、数据没了」）。
//   - 与正向同一套真 SQL 内核（显式 id、ON CONFLICT upsert、PG setval 复位、双向指纹），
//     只是 src/dst 对调 ⇒ 跑第二遍行数不变（KN10 幂等）。
//   - ReverseTables() 独立列出四表列规格：故意漏一列（M0313）会让目标库该列落默认值，
//     而指纹按 contract 全量列算 ⇒ 指纹不等 ⇒ 返回 error ⇒ 反向演练判红。
//
// ⚠️ 适用范围（写进文件头，AGENTS §一·9）：本工具**只**用于 W3-5 删主库表之后的硬回滚
//   数据回搬；删表动作本身不可由代码回滚，回退二进制不会把表变回来。
// ================================================
package knowledge

import (
	"context"
	"database/sql"

	"translator/internal/db"
	"translator/internal/knowledge/contract"
)

// ReverseTables 反向搬迁（知识库 → 主库）的列规格。
// ★ 四表逐条显式列出（不复用正向循环）：任何一表漏列都能被 KN10 的双向指纹当场抓到。
func ReverseTables() []tableSpec {
	return []tableSpec{
		{
			Table:   contract.TableProducts,
			Columns: contract.ColumnsFor(contract.TableProducts),
			PK:      contract.PrimaryKey(contract.TableProducts),
			AutoInc: false, // products.id 是 TEXT，无序列
		},
		{
			Table:   contract.TableProductFacts,
			Columns: contract.ColumnsFor(contract.TableProductFacts),
			PK:      contract.PrimaryKey(contract.TableProductFacts),
			AutoInc: true,
		},
		{
			Table:   contract.TableProductAssets,
			Columns: contract.ColumnsFor(contract.TableProductAssets),
			PK:      contract.PrimaryKey(contract.TableProductAssets),
			AutoInc: true,
		},
		{
			Table:   contract.TableProductFactI18n,
			Columns: contract.ColumnsFor(contract.TableProductFactI18n),
			PK:      contract.PrimaryKey(contract.TableProductFactI18n),
			AutoInc: true,
		},
	}
}

// ReverseMigrate 知识库 → 主库反向搬迁（W3-5 硬回滚数据腿）。
// 参数：knowledge/knowD=知识库连接与方言（源）；main/mainD=主库连接与方言（目标）。
// 幂等：显式 id + ON CONFLICT upsert，跑第二遍行数不变；PG 目标库序列同样复位。
func ReverseMigrate(ctx context.Context, knowledge *sql.DB, knowD db.Dialect, main *sql.DB, mainD db.Dialect) (Report, error) {
	return copyFourTables(ctx, knowledge, knowD, main, mainD, ReverseTables())
}
