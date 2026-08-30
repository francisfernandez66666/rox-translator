// ============ 本文件职责中文说明 ============
// pgvector 后端验证测试：仅在 PG_TEST_DSN 已配置且已安装 vector 扩展时运行，
// 其余环境自动跳过（不影响默认的 SQLite 测试）。
//   1) VectorSearch 余弦近邻排序与业务优先级（部门>跨部门>企业>行业>通用语言习惯包）正确性；
//   2) ScopeVisibility 链内/共享层/跨部门/不可见/nil-scope 五种判定（保证 npz 与 pgvector 两条
//      检索路径口径一致，跨部门命中 InChain=false）；
//   3) UpsertEmbedding 向量双写与「真实 Embedding API → pgvector 写入 → 语义检索」全链路闭合。
// =============================================
package kb

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"

	"translator/internal/config"
	"translator/internal/llm"
)

// TestVectorSearchOnPG 验证 pgvector 向量检索路径：插入已知向量，确认近邻排序正确。
// 仅在 PG_TEST_DSN 已配置且 pgvector 扩展可用时运行；否则跳过（不影响 SQLite 默认测试）。
func TestVectorSearchOnPG(t *testing.T) {
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		t.Skip("PG_TEST_DSN 未配置，跳过 pgvector 检索测试")
	}
	// 切到 PostgreSQL 方言（CurrentDialect 读全局 config.C）
	prev := config.C
	config.C = &config.Config{DatabaseDriver: "postgres", DatabaseDSN: dsn}
	defer func() { config.C = prev }()

	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("打开 PG 失败: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Exec("CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		t.Skipf("pgvector 扩展不可用，跳过: %v", err)
	}

	// 建临时表验证向量检索语义（不污染业务表）
	if _, err := conn.Exec(`DROP TABLE IF EXISTS kb_vec_test`); err != nil {
		t.Fatalf("清理临时表失败: %v", err)
	}
	if _, err := conn.Exec(`CREATE TABLE kb_vec_test (
		id BIGSERIAL PRIMARY KEY,
		tenant_id INTEGER NOT NULL DEFAULT 1,
		embedding vector(3)
	)`); err != nil {
		t.Fatalf("建临时表失败: %v", err)
	}
	defer conn.Exec(`DROP TABLE IF EXISTS kb_vec_test`)

	// 三个向量：q≈[1,0,0]；a=[0.99,0.01,0]（近），b=[0,1,0]（远）
	if _, err := conn.Exec(`INSERT INTO kb_vec_test (id, tenant_id, embedding) VALUES
		(1,1,'[0.99,0.01,0]'), (2,1,'[0,1,0]')`); err != nil {
		t.Fatalf("插入向量失败: %v", err)
	}
	// 借用业务表的 VectorSearch 逻辑需真实 tm_segments，这里直接验证 SQL 语义等价性。
	rows, err := conn.Query(`SELECT id, embedding <=> $1 FROM kb_vec_test WHERE tenant_id=1 AND embedding IS NOT NULL ORDER BY embedding <=> $1 LIMIT 5`, "[1,0,0]")
	if err != nil {
		t.Fatalf("向量检索失败: %v", err)
	}
	defer rows.Close()
	var order []int64
	for rows.Next() {
		var id int64
		var dist float64
		if err := rows.Scan(&id, &dist); err != nil {
			t.Fatalf("扫描失败: %v", err)
		}
		order = append(order, id)
	}
	if len(order) != 2 || order[0] != 1 {
		t.Fatalf("预期近邻 id=1 排首位，实得 %v", order)
	}
}

// TestScopeVisibility 单测：覆盖链内/共享层/跨部门/不可见/nil-scope 五种判定，
// 确保 pgvector 与 npz 两条检索路径口径一致（任务 2：跨部门 InChain=false）。
func TestScopeVisibility(t *testing.T) {
	caller := int64(10)
	shared := &PackScope{
		TenantID:       caller,
		TenantPackIDs:  map[int64]bool{1: true},
		ChainPacks:     map[int64]int{1: 0},
		SharedPackIDs:  map[int64]bool{2: true},
		AllowCrossDept: true,
		CrossDeptPacks: map[int64]string{3: "其它部门"},
	}
	cases := []struct {
		name     string
		rowT     int64
		pack     int64
		scope    *PackScope
		vis, ic  bool
	}{
		{"链内企业包", caller, 1, shared, true, true},
		{"共享层(宿主租户)", 1, 2, shared, true, true},
		{"跨部门回退", 20, 3, shared, true, false},
		{"不可见(其它租户非跨部门)", 20, 5, shared, false, false},
		{"nil-scope 同租户", caller, 0, nil, true, true},
		{"nil-scope 异租户", 99, 0, nil, false, false},
	}
	for _, c := range cases {
		vis, ic := ScopeVisibility(c.rowT, c.pack, caller, c.scope)
		if vis != c.vis || ic != c.ic {
			t.Errorf("%s: 期望 (vis=%v,ic=%v)，实得 (vis=%v,ic=%v)", c.name, c.vis, c.ic, vis, ic)
		}
	}
}

// TestVectorSearchPriorityOrderOnPG 验证业务优先级排序：部门 > 跨部门 > 企业 > 行业 > 无scope。
// 四个档各写一条同相似度向量，断言 VectorSearch 返回顺序严格遵循 Rank 层级（层内才比相似度）。
func TestVectorSearchPriorityOrderOnPG(t *testing.T) {
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		t.Skip("PG_TEST_DSN 未配置，跳过")
	}
	prev := config.C
	config.C = &config.Config{DatabaseDriver: "postgres", DatabaseDSN: dsn}
	defer func() { config.C = prev }()

	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("打开 PG 失败: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Exec("CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		t.Skipf("pgvector 不可用，跳过: %v", err)
	}
	conn.Exec("DELETE FROM tm_segments WHERE zh_hash LIKE 'prio_%'")
	defer conn.Exec("DELETE FROM tm_segments WHERE zh_hash LIKE 'prio_%'")

	vc := make([]float32, 1024)
	vc[0] = 0.99 // 与查询最近

	// 各档使用不同 pack 以触发不同 Rank；tenant 设计使 scope 可见性成立。
	rows := []struct {
		hash   string
		tenant int64
		pack   int64
		tag    string
	}{
		{"prio_dept", 10, 1, "部门"},       // ChainPacks[1]=0 → rank 0
		{"prio_cross", 20, 3, "跨部门"},     // CrossDeptPacks[3] → rank 100
		{"prio_ent", 10, 5, "企业"},        // TenantPackIDs[5] → rank 200
		{"prio_ind", 1, 2, "行业"},         // SharedPackIDs[2] → rank 300
		{"prio_univ", 1, 9, "通用语言习惯"}, // UniversalPackIDs[9] → rank 400（无scope，最低）
	}
	for _, r := range rows {
		if _, err := conn.Exec(
			"INSERT INTO tm_segments (zh_hash, zh, module, tenant_id, pack_id, updated_at, embedding) VALUES ($1,$2,'t',$3,$4,now(),$5)",
			r.hash, r.hash, r.tenant, r.pack, formatVector(vc)); err != nil {
			t.Fatalf("插入 %s 失败: %v", r.tag, err)
		}
	}
	scope := &PackScope{
		TenantID:        10,
		TenantPackIDs:   map[int64]bool{1: true, 5: true},
		ChainPacks:      map[int64]int{1: 0},
		SharedPackIDs:   map[int64]bool{2: true},
		UniversalPackIDs: map[int64]bool{9: true},
		AllowCrossDept:  true,
		CrossDeptPacks:  map[int64]string{3: "其它部门"},
	}
	k := &KBDatabase{db: conn, dbPath: "pg"}
	query := make([]float32, 1024)
	query[0] = 1
	res, err := k.VectorSearch(query, 10, scope, 10)
	if err != nil {
		t.Fatalf("VectorSearch 失败: %v", err)
	}
	if len(res) != 5 {
		t.Fatalf("预期 5 条，实得 %d: %+v", len(res), res)
	}
	want := []string{"prio_dept", "prio_cross", "prio_ent", "prio_ind", "prio_univ"}
	got := []string{}
	for _, r := range res {
		got = append(got, zhHashOf(conn, r.ID))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("优先级顺序错误：期望 %v，实得 %v", want, got)
		}
	}
	t.Logf("五档优先级顺序正确：%v（部门>跨部门>企业>行业>通用语言习惯包）", got)
}

// zhHashOf 按 id 反查 zh_hash（测试辅助）。
func zhHashOf(conn *sql.DB, id int64) string {
	var h string
	_ = conn.QueryRow("SELECT zh_hash FROM tm_segments WHERE id=$1", id).Scan(&h)
	return h
}

// TestKBDatabaseVectorSearchScopedOnPG 端到端验证 pgvector 检索（含 scope/跨部门）：
// 直接写入已知 1024 维向量到 tm_segments，校验 VectorSearch 的可见性与 InChain 标记与排序。
// 仅在 PG_TEST_DSN 且 pgvector 可用时运行；否则跳过。
func TestKBDatabaseVectorSearchScopedOnPG(t *testing.T) {
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		t.Skip("PG_TEST_DSN 未配置，跳过")
	}
	prev := config.C
	config.C = &config.Config{DatabaseDriver: "postgres", DatabaseDSN: dsn}
	defer func() { config.C = prev }()

	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("打开 PG 失败: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Exec("CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		t.Skipf("pgvector 不可用，跳过: %v", err)
	}

	k := &KBDatabase{db: conn, dbPath: "pg"}

	// 清空可能存在的测试行（以固定 zh_hash 前缀标识）并写入三条已知向量。
	conn.Exec("DELETE FROM tm_segments WHERE zh_hash LIKE 'vectest_%'")
	defer conn.Exec("DELETE FROM tm_segments WHERE zh_hash LIKE 'vectest_%'")

	vecClose := make([]float32, 1024)
	vecClose[0] = 0.99
	vecClose[1] = 0.01
	vecFar := make([]float32, 1024)
	vecFar[1] = 1
	query := make([]float32, 1024)
	query[0] = 1

	rows := []struct {
		hash    string
		tenant  int64
		pack    int64
		vec     []float32
	}{
		{"vectest_chain", 10, 1, vecClose},  // 链内（caller 租户 + 企业包）：应 InChain=true
		{"vectest_shared", 1, 2, vecFar},    // 共享层（宿主租户）：应 InChain=true
		{"vectest_cross", 20, 3, vecClose},  // 跨部门（其它租户 + 跨部门包）：应 InChain=false
	}
	for _, r := range rows {
		if _, err := conn.Exec(
			"INSERT INTO tm_segments (zh_hash, zh, module, tenant_id, pack_id, updated_at, embedding) VALUES ($1,$2,'t',$3,$4,now(),$5)",
			r.hash, r.hash, r.tenant, r.pack, formatVector(r.vec)); err != nil {
			t.Fatalf("插入测试向量失败: %v", err)
		}
	}

	scope := &PackScope{
		TenantID:       10,
		TenantPackIDs:  map[int64]bool{1: true},
		ChainPacks:     map[int64]int{1: 0},
		SharedPackIDs:  map[int64]bool{2: true},
		AllowCrossDept: true,
		CrossDeptPacks: map[int64]string{3: "其它部门"},
	}
	res, err := k.VectorSearch(query, 10, scope, 10)
	if err != nil {
		t.Fatalf("VectorSearch 失败: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("VectorSearch 未返回任何结果")
	}
	// 链内应排在跨部门之前；链内中最接近的 vectest_chain 应居首且 InChain=true。
	if res[0].ID == 0 || !res[0].InChain {
		t.Fatalf("预期首条为链内命中，实得 %+v", res[0])
	}
	seenCross := false
	for _, r := range res {
		if r.InChain == false {
			seenCross = true
		}
	}
	if !seenCross {
		t.Fatalf("预期包含跨部门(InChain=false)结果，实得 %+v", res)
	}
}

// TestUpsertEmbeddingOnPG 验证重建索引时的向量双写（UpsertEmbedding）能正确写入 tm_segments.embedding。
func TestUpsertEmbeddingOnPG(t *testing.T) {
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		t.Skip("PG_TEST_DSN 未配置，跳过")
	}
	prev := config.C
	config.C = &config.Config{DatabaseDriver: "postgres", DatabaseDSN: dsn}
	defer func() { config.C = prev }()

	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("打开 PG 失败: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Exec("CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		t.Skipf("pgvector 不可用，跳过: %v", err)
	}
	k := &KBDatabase{db: conn, dbPath: "pg"}
	conn.Exec("DELETE FROM tm_segments WHERE zh_hash='vectest_write'")
	defer conn.Exec("DELETE FROM tm_segments WHERE zh_hash='vectest_write'")
	if _, err := conn.Exec(
		"INSERT INTO tm_segments (zh_hash, zh, module, tenant_id, pack_id, updated_at) VALUES ('vectest_write','w','t',1,0,now())"); err != nil {
		t.Fatalf("插入基行失败: %v", err)
	}
	var id int64
	if err := conn.QueryRow("SELECT id FROM tm_segments WHERE zh_hash='vectest_write'").Scan(&id); err != nil {
		t.Fatalf("读取 id 失败: %v", err)
	}
	vec := make([]float32, 1024)
	vec[2] = 0.5
	if err := k.UpsertEmbedding(id, vec); err != nil {
		t.Fatalf("UpsertEmbedding 失败: %v", err)
	}
	var dist float64
	if err := conn.QueryRow("SELECT embedding <-> $1 FROM tm_segments WHERE id=$2", formatVector(vec), id).Scan(&dist); err != nil {
		t.Fatalf("回读向量失败: %v", err)
	}
	if dist > 1e-6 {
		t.Fatalf("写入向量与回读不一致，距离=%v", dist)
	}
}

// TestVectorSearchRealEmbeddingOnPG 真实调用 SiliconFlow BAAI/bge-m3 生成向量并写入 pgvector，
// 再经 VectorSearch 检索，验证「真实 Embedding API → pgvector 写入 → 语义检索」全链路闭合。
// 仅当 PG_TEST_DSN 与 REAL_EMBED_KEY（SiliconFlow key）均配置时运行；否则跳过。
func TestVectorSearchRealEmbeddingOnPG(t *testing.T) {
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		t.Skip("PG_TEST_DSN 未配置，跳过")
	}
	key := os.Getenv("REAL_EMBED_KEY")
	if key == "" {
		t.Skip("REAL_EMBED_KEY 未配置，跳过真实 Embedding 测试")
	}
	prev := config.C
	config.C = &config.Config{
		DatabaseDriver: "postgres", DatabaseDSN: dsn,
		EmbedAPIBase: "https://api.siliconflow.cn/v1", EmbedAPIKey: key, EmbedModel: "BAAI/bge-m3",
	}
	defer func() { config.C = prev }()

	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("打开 PG 失败: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Exec("CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		t.Skipf("pgvector 不可用，跳过: %v", err)
	}

	cli := llm.NewClient(config.C)

	// 两条语义截然不同的中文句 + 一个贴近句 A 的查询。
	const (
		zhA = "请确认合同中的付款期限与违约金条款是否一致。"
		zhB = "今天的天气真好，我们一起去公园散步吧。"
		qry = "帮我看一下合同里付款时间和违约金是怎么约定的。"
	)
	conn.Exec("DELETE FROM tm_segments WHERE zh_hash LIKE 'realembed_%'")
	defer conn.Exec("DELETE FROM tm_segments WHERE zh_hash LIKE 'realembed_%'")

	type row struct{ hash, zh string }
	rows := []row{
		{"realembed_a", zhA},
		{"realembed_b", zhB},
	}
	wantID := make(map[string]int64)
	for _, r := range rows {
		if _, err := conn.Exec(
			"INSERT INTO tm_segments (zh_hash, zh, module, tenant_id, pack_id, updated_at) VALUES ($1,$2,'t',1,1,now())",
			r.hash, r.zh); err != nil {
			t.Fatalf("插入基行失败: %v", err)
		}
		var id int64
		if err := conn.QueryRow("SELECT id FROM tm_segments WHERE zh_hash=$1", r.hash).Scan(&id); err != nil {
			t.Fatalf("读取 id 失败: %v", err)
		}
		wantID[r.hash] = id
		vec, err := cli.Embed(context.Background(), r.zh)
		if err != nil {
			t.Fatalf("真实 Embedding 失败(%s): %v", r.hash, err)
		}
		if len(vec) != 1024 {
			t.Fatalf("BAAI/bge-m3 维度应为 1024，实得 %d", len(vec))
		}
		k := &KBDatabase{db: conn, dbPath: "pg"}
		if err := k.UpsertEmbedding(id, vec); err != nil {
			t.Fatalf("UpsertEmbedding 失败: %v", err)
		}
	}

	qvec, err := cli.Embed(context.Background(), qry)
	if err != nil {
		t.Fatalf("查询 Embedding 失败: %v", err)
	}
	k := &KBDatabase{db: conn, dbPath: "pg"}
	res, err := k.VectorSearch(qvec, 1, &PackScope{
		TenantID:      1,
		TenantPackIDs: map[int64]bool{1: true},
		ChainPacks:    map[int64]int{1: 0},
	}, 5)
	if err != nil {
		t.Fatalf("VectorSearch 失败: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("VectorSearch 未返回结果")
	}
	if res[0].ID != wantID["realembed_a"] {
		t.Fatalf("预期检索首条为句 A(id=%d)，实得 id=%d (全量=%+v)", wantID["realembed_a"], res[0].ID, res)
	}
	t.Logf("真实 Embedding 链路通过：查询=「%s」→ 命中「%s」(id=%d, InChain=%v, Sim=%.3f)",
		qry, zhA, res[0].ID, res[0].InChain, res[0].Sim)
}
