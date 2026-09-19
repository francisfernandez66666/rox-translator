// ============================================================================
// prompt_prefix_test.go — ★ B4（方案 B Phase 1）prompt 前缀重组回归（2026-09-19）
// 锁定的缺陷（缓存前置工程五杀手拆 ①②③④）：
//
//	① per-request 检索术语 ref 曾占 system 头部 → 现必须只出现在 user；
//	② 文化规则 SQL 无 ORDER BY，重载后行序抖动 → 现按 sp.id 定序渲染；
//	③ 术语行按条数编号 1./2./3.，少一条全变 → 现按 source_text 字典序、去编号；
//	④ 有/无 KB 命中两套 system 形态互不为前缀 → 现 system 逐字节一致。
//
// 另断言批量路径租户术语层（batchTermLayer）：可见域过滤、定序渲染、60s 缓存。
// 运行：go test ./internal/engine/ -run 'TestB4|TestBatchTermLayer|TestCultureRules' -v
// ============================================================================
package engine

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"translator/internal/config"
	"translator/internal/kb"
	"translator/internal/store"
)

// b4Store 内存 SQLite Store 厂（同 stagemodel_test 模式：显式钉 sqlite 方言）。
func b4Store(t *testing.T) *store.Store {
	t.Helper()
	oldCfg := config.C
	config.C = config.Default()
	config.C.DatabaseDriver = "sqlite"
	t.Cleanup(func() { config.C = oldCfg })
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	s, err := store.New(db)
	if err != nil {
		t.Fatalf("创建测试 Store 失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return s
}

// insertPack 直插一条 kb_packages（可指定显式 id 以打乱物理序）。
func insertPack(t *testing.T, s *store.Store, id, tid int64, packType string, enabled int) {
	t.Helper()
	_, err := s.DB().Exec(
		"INSERT INTO kb_packages (id, tenant_id, parent_id, code, name, pack_type, role, sort_order, enabled, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
		id, tid, 0, "p", "p", packType, "source", 0, enabled, "2026-09-19", "2026-09-19")
	if err != nil {
		t.Fatalf("插入 kb_packages 失败: %v", err)
	}
}

// insertEntry 直插一条 kb_entries 术语（layer=1）。
func insertEntry(t *testing.T, s *store.Store, tid, pkgID int64, layer int, srcLang, src, tgtLang, tgt string) {
	t.Helper()
	_, err := s.DB().Exec(
		"INSERT INTO kb_entries (tenant_id, package_id, layer, source_lang, source_text, target_lang, target_text, module, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)",
		tid, pkgID, layer, srcLang, src, tgtLang, tgt, "", "2026-09-19", "2026-09-19")
	if err != nil {
		t.Fatalf("插入 kb_entries 失败: %v", err)
	}
}

// ---------- 杀手①④：装配后 system 与检索结果无关、ref 只在 user ----------

func TestB4AssemblePrefixStability(t *testing.T) {
	instr := "把下面的中文翻译为德语，只输出译文本身。<t>契约</t>"
	culture := "\n【语言文化规范（必须遵守，违反将被拒绝）】\n· 写作规范：用 Sie 称谓"
	ref := "知识库术语参考（仅用于沿用其专有词/术语译法，不得复述以下参考内容）：\n蓝牙 → Bluetooth\n"
	src := "激活蓝牙钥匙。"

	withRef := assembleTranslateMessages(instr, "", ref, culture, src)
	noRef := assembleTranslateMessages(instr, "", "", culture, src)
	if len(withRef) != 2 || len(noRef) != 2 {
		t.Fatalf("期望 2 条消息，实际 %d/%d", len(withRef), len(noRef))
	}
	// 杀手④拆除：有/无 KB 命中 system 逐字节一致
	if withRef[0]["content"] != noRef[0]["content"] {
		t.Errorf("system 前缀随检索结果漂移:\n%q\n%q", withRef[0]["content"], noRef[0]["content"])
	}
	// 杀手①拆除：system 只含规范头+文化块，绝不掺 ref
	if strings.Contains(withRef[0]["content"], "蓝牙 →") {
		t.Errorf("per-request 术语 ref 仍在污染 system 头部")
	}
	if !strings.Contains(withRef[0]["content"], instr) || !strings.Contains(withRef[0]["content"], culture) {
		t.Errorf("system 应同时含规范头与文化块: %q", withRef[0]["content"])
	}
	// ref 紧邻原文之前；注记在 ref 之前
	u := withRef[1]["content"]
	if !strings.HasPrefix(u, ref) || !strings.HasSuffix(u, src) {
		t.Errorf("user 应为 ref+原文: %q", u)
	}
	noted := assembleTranslateMessages(instr, "缩翻限制。", ref, culture, src)[1]["content"]
	if !strings.HasPrefix(noted, "缩翻限制。\n\n"+ref) {
		t.Errorf("注记应排在 ref 前且空行分隔: %q", noted)
	}
}

// ---------- 杀手③：术语行字典序 + 去编号 ----------

func TestB4BuildExamplesPromptOrderedNoNumber(t *testing.T) {
	// 乱序注入（模拟按相关度返回），期望按 source_text 字节序重排
	rows := []*kb.Row{
		{Zh: "车控", Langs: map[string]string{"en": "Vehicle Control"}},
		{Zh: "蓝牙钥匙", Langs: map[string]string{"en": "Bluetooth Key"}},
		{Zh: "手机", Langs: map[string]string{"en": "Phone"}},
	}
	out := buildExamplesPrompt("车控蓝牙手机", "en", rows)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 { // 标题 + 3 行
		t.Fatalf("期望标题+3行，实际:\n%s", out)
	}
	for _, l := range lines[1:] {
		if len(l) > 0 && l[0] >= '0' && l[0] <= '9' {
			t.Errorf("术语行不应再带条数编号: %q", l)
		}
	}
	body := strings.Join(lines[1:], "\n")
	iPhone := strings.Index(body, "手机")
	iLan := strings.Index(body, "蓝牙钥匙")
	iChe := strings.Index(body, "车控")
	if !(iPhone >= 0 && iPhone < iLan && iLan < iChe) {
		t.Errorf("期望字典序 手机<蓝牙钥匙<车控，实际:\n%s", body)
	}
	if !strings.Contains(lines[0], "不得复述") {
		t.Errorf("头部应自带不得复述约束（ref 已移到 user，不能再靠 system 尾部）")
	}
}

// ---------- 杀手②：文化块按 id 定序渲染（物理序 ≠ id 序场景） ----------

func TestB4CultureRulesOrderedById(t *testing.T) {
	s := b4Store(t)
	insertPack(t, s, 91, 7, "locale", 1) // id 避开 store.New 预置包（id=1）
	// 显式倒序 id：物理行序 (3,1)，ORDER BY sp.id 应渲染 (1,3)
	if _, err := s.DB().Exec(
		"INSERT INTO kb_safety_phrases (id, tenant_id, package_id, lang, phrase, kind, created_at) VALUES (3,0,91,'de','后插的规范','style','2026-09-19'),(1,0,91,'de','先排的规范','style','2026-09-19')",
	); err != nil {
		t.Fatalf("插入安全句失败: %v", err)
	}
	e := &Engine{St: s, Cfg: config.Default()}
	text, rules := e.cultureRules(context.Background(), 7, "de")
	if len(rules) != 2 {
		t.Fatalf("期望 2 条规则，实际 %d:\n%s", len(rules), text)
	}
	if rules[0].Phrase != "先排的规范" || rules[1].Phrase != "后插的规范" {
		t.Errorf("文化块未按 sp.id 定序（杀手②回归）: %q %q", rules[0].Phrase, rules[1].Phrase)
	}
}

// ---------- 批量租户术语层：可见域过滤 + 定序 + 缓存 ----------

func TestB4BatchTermLayer(t *testing.T) {
	s := b4Store(t)
	insertPack(t, s, 11, 7, "tenant", 1) // 自有包
	insertPack(t, s, 12, 0, "locale", 1) // 平台共享 locale 包
	insertPack(t, s, 13, 0, "locale", 0) // 停用的共享包
	insertPack(t, s, 14, 8, "tenant", 1) // 他租户包
	insertEntry(t, s, 7, 11, 1, "zh", "车控", "en", "Vehicle Control")
	insertEntry(t, s, 7, 11, 1, "zh", "蓝牙", "en", "Bluetooth")
	insertEntry(t, s, 0, 12, 1, "zh", "手机", "en", "Phone")
	insertEntry(t, s, 0, 13, 1, "zh", "停用包词", "en", "Off")                      // 包 enabled=0，不可见
	insertEntry(t, s, 8, 14, 1, "zh", "他租户词", "en", "Other")                    // 他租户，不可见
	insertEntry(t, s, 7, 11, 2, "zh", "长句层", "en", "L2")                        // layer≠1，不收
	insertEntry(t, s, 7, 11, 1, "zh", "日语层", "ja", "JP")                        // 目标语言不符，不收
	insertEntry(t, s, 7, 11, 1, "zh", strings.Repeat("超", 31), "en", "TooLong") // >30 字过滤
	e := &Engine{St: s, Cfg: config.Default()}
	ctx := context.Background()

	text := e.batchTermLayer(ctx, 7, "en")
	for _, want := range []string{"租户术语层", "车控 → Vehicle Control", "蓝牙 → Bluetooth", "手机 → Phone"} {
		if !strings.Contains(text, want) {
			t.Errorf("术语层缺少 %q:\n%s", want, text)
		}
	}
	for _, ban := range []string{"停用包词", "他租户词", "长句层", "日语层", "TooLong"} {
		if strings.Contains(text, ban) {
			t.Errorf("术语层不应包含 %q（可见域/过滤回归）", ban)
		}
	}
	// 字典序：手机(手 E6) < 蓝牙(E8 93) < 车控(E8 BD)
	i1, i2, i3 := strings.Index(text, "手机"), strings.Index(text, "蓝牙"), strings.Index(text, "车控")
	if !(i1 >= 0 && i1 < i2 && i2 < i3) {
		t.Errorf("术语层应按 source_text 定序:\n%s", text)
	}

	// 缓存语义：60s 内改库不重查；清缓存后新词生效
	insertEntry(t, s, 7, 11, 1, "zh", "新增词", "en", "NewTerm")
	if again := e.batchTermLayer(ctx, 7, "en"); again != text {
		t.Errorf("TTL 内应命中缓存（文本逐字节一致以稳定前缀）")
	}
	e.termLayerCache = map[string]termLayerEntry{}
	if fresh := e.batchTermLayer(ctx, 7, "en"); !strings.Contains(fresh, "新增词") {
		t.Errorf("缓存失效后应重新查询: %s", fresh)
	}

	// 边界：无 Store / tid=0 / 空语言 → 空串（不 panic）
	if got := (&Engine{}).batchTermLayer(ctx, 7, "en"); got != "" {
		t.Errorf("St=nil 期望空串: %q", got)
	}
	if got := e.batchTermLayer(ctx, 0, "en"); got != "" {
		t.Errorf("tid=0 期望空串: %q", got)
	}
	// 缓存 key 按 (tenant,lang) 分片：他租户查不到本租户术语
	if other := e.batchTermLayer(ctx, 99, "en"); strings.Contains(other, "蓝牙") {
		t.Errorf("术语层串租户: %s", other)
	}
}
