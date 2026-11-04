// ============ openapi_compliance_gate_test.go · 职责说明 ============
// 2026-11-03 合规闸工单接入批 · 波 D「对外契约四面同步」的机制闸门。
//
// 背景（缺陷形态，别照旧写法改回去）：来源档 `sources` 是**对外契约**（SDK 客户按枚举分支），
// 本批给它加了第六档 `compliance_skipped`，同时新增两个出栈键 `compliance_skipped` / `compliance_items`。
// 这条链上**同一个事实有四个副本**，历史上每一次漏一处都长得一模一样：
//
//	① orchestrator 的 `Source*` 常量（唯一事实源）；
//	② `openapi.v1.json`（机器读的规范：enum + description）；
//	③ `admin_openapi.go` 的中英两段内置文档（人读的那一面）；
//	④ `api_openapi_tasks.go` 的实际出栈（文档承诺"同层另出两个键"，代码不写就是文档骗人）。
//
// 光改 ②③ 而 ④ 不落地，客户会读到"有两个键"却永远拿不到它们；光改 ④ 而 ②③ 不跟，
// 客户按旧规范写的 switch 会掉进 default 分支。两者都是**服务端全绿、客户端出错**。
//
// 本文件的锁（缺一不可）：
//
//	L1 跨面等值：规范 `TaskStatus.sources` 的 enum 集合 == orchestrator 六个 `Source*` 常量值集合
//	  （期望值从常量现取，**不在测试里手抄名单**——手抄的那一份迟早与常量打架）。
//	L2 文档点名：中英两段内置文档必须**逐档点名**第六档，并写清它「不计入未译出三档」
//	  （少了这句，客户会把合规跳过读成漏译、去追一条不存在的补译）。
//	L3 出栈行为：有跳过 ⇒ 两个键都在；没跳过 ⇒ **两个键都不出**（缺席优先，出 0/[] 会被读成
//	  "本该收 0"，§25-b 那条 points_free 同型）。
//	L4 反证：L1 的判据函数喂"少一档的 enum"必须报问题；L3 的判据函数喂旧形态（恒出两键）必须报问题
//	  ——没有变异体就分不清"腿有射程"与"腿恒真"（AGENTS §三）。
//
// 方言：自钉 SQLite 内存库（复用 ticket_f42_test.go 的夹具，AGENTS §一·4）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestOpenAPICompliance
// =============================================
package api

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"translator/internal/orchestrator"
)

// complianceSourceValues 来源档唯一事实源的六个值（单一名单，L1 期望值与 L2 点名共用）。
func complianceSourceValues() []string {
	return []string{
		orchestrator.SourceKB,
		orchestrator.SourceModel,
		orchestrator.SourceModelWithFallback,
		orchestrator.SourceUpstreamFailed,
		orchestrator.SourceApproved,
		orchestrator.SourceComplianceSkipped,
	}
}

// complianceSourceSetProblems 判据本体：规范 enum 与常量名单逐值等值（含重复项检测）。
// 返回空切片＝合规。反证直接喂变异名单（见 L4）。
func complianceSourceSetProblems(enum, want []string) []string {
	var probs []string
	seen := map[string]int{}
	for _, v := range enum {
		seen[v]++
		if seen[v] > 1 {
			probs = append(probs, "规范 enum 出现重复取值: "+v)
		}
	}
	for _, v := range want {
		if seen[v] == 0 {
			probs = append(probs, "规范 enum 缺来源档: "+v)
		}
	}
	wantSet := map[string]bool{}
	for _, v := range want {
		wantSet[v] = true
	}
	var extra []string
	for _, v := range enum {
		if !wantSet[v] {
			extra = append(extra, v)
		}
	}
	sort.Strings(extra)
	for _, v := range extra {
		probs = append(probs, "规范 enum 多出未登记来源档: "+v)
	}
	return probs
}

// L1：规范 enum 与常量名单逐值等值。
func TestOpenAPIComplianceSourceEnumMatchesConstants(t *testing.T) {
	raw, err := os.ReadFile("openapi.v1.json")
	if err != nil {
		t.Fatalf("读规范失败（应在 backend-go/internal/api 下运行）：%v", err)
	}
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					AdditionalProperties struct {
						Enum []string `json:"enum"`
					} `json:"additionalProperties"`
				} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("解析规范失败：%v", err)
	}
	taskStatus, ok := spec.Components.Schemas["TaskStatus"]
	if !ok {
		t.Fatal("规范里找不到 TaskStatus 模型（锚点漂了，闸门已失效）")
	}
	sources, ok := taskStatus.Properties["sources"]
	if !ok {
		t.Fatal("TaskStatus 里找不到 sources 属性（锚点漂了，闸门已失效）")
	}
	enum := sources.AdditionalProperties.Enum
	// 射程自证：名字对了但 enum 被摘空＝闸门空转。
	if len(enum) < len(complianceSourceValues()) {
		t.Fatalf("规范 sources.enum 只有 %d 个取值（期望 ≥%d），射程不足＝闸门已失效：%v",
			len(enum), len(complianceSourceValues()), enum)
	}
	for _, p := range complianceSourceSetProblems(enum, complianceSourceValues()) {
		t.Error(p)
	}
	// 两个新键必须在规范里声明（客户按规范生成 SDK 时才看得见它们）。
	for _, key := range []string{"compliance_skipped", "compliance_items"} {
		if _, ok := taskStatus.Properties[key]; !ok {
			t.Errorf("TaskStatus 缺新出栈键 %q 的声明（文档承诺了却不在规范里＝SDK 代码生成看不到）", key)
		}
	}
}

// L2：中英两段内置文档必须逐档点名第六档，并交代它与未译出三档互斥。
func TestOpenAPIComplianceDocsNameSixthSource(t *testing.T) {
	raw, err := os.ReadFile("admin_openapi.go")
	if err != nil {
		t.Fatalf("读内置文档失败：%v", err)
	}
	src := string(raw)
	for _, p := range complianceDocProblems(src) {
		t.Error(p)
	}
}

// complianceDocProblems 判据本体（L2 与反证共用）。
// 三条：① 第六档字面量出现在来源表里；② 写明"不计入/不混记"未译出三档；
// ③ 中文与英文两段都要有（按 §一·5「新增面板键必须双面补译」的同一条取向）。
//
// ⚠️ 判据必须先**归一化**再比对：内置文档是 Markdown，同一句话常被换行拆开
// （现网形态就是 `**not**\ncounted in unresolved_lines`），拿整串去 Contains 会
// 把"写对了但折了行"判成缺失（本批首跑真踩）。归一只剥 `*` 强调符与连续空白，
// 不改词序——所以"两句话都写了"与"只写了半句"仍分得开。
func complianceDocProblems(src string) []string {
	var probs []string
	flat := complianceDocFlatten(src)
	if strings.Count(flat, orchestrator.SourceComplianceSkipped) < 2 {
		probs = append(probs, "内置文档里 "+orchestrator.SourceComplianceSkipped+
			" 出现次数 <2（中英两段都要点名，当前只写了一面）")
	}
	if !strings.Contains(flat, "不计入 unresolved_lines") {
		probs = append(probs, "中文内置文档没交代「合规跳过不计入未译出三档」——客户会把设计读成漏译")
	}
	if !strings.Contains(flat, "not counted in unresolved_lines") {
		probs = append(probs, "英文内置文档没交代合规跳过不计入未译出三档")
	}
	return probs
}

// complianceDocFlatten 把 Markdown 正文归一成单行（剥 `*` 强调符、合并连续空白）。
func complianceDocFlatten(src string) string {
	src = strings.ReplaceAll(src, "*", "")
	return strings.Join(strings.Fields(src), " ")
}

// complianceOutboundProblems 判据本体（L3 与反证共用）：检查响应体里两个键的出现形态。
// wantPresent=true ⇒ 两键都在且非空；false ⇒ 两键都缺席（不许出 0 / null / []）。
func complianceOutboundProblems(body map[string]json.RawMessage, wantPresent bool) []string {
	var probs []string
	for _, key := range []string{"compliance_skipped", "compliance_items"} {
		raw, ok := body[key]
		if wantPresent {
			if !ok {
				probs = append(probs, "有合规跳过时缺出栈键: "+key)
				continue
			}
			s := strings.TrimSpace(string(raw))
			if s == "null" || s == "{}" || s == "[]" {
				probs = append(probs, "出栈键 "+key+" 是空形态（"+s+"），应缺席而不是出空值")
			}
			continue
		}
		if ok {
			probs = append(probs, "没有合规跳过时不该出键 "+key+"（实得 "+strings.TrimSpace(string(raw))+
				"；出 0/[] 会被读成「本该收 0」）")
		}
	}
	return probs
}

// L3：真发一次 /tasks/status，验两个新键的出现形态。
func TestOpenAPIComplianceStatusEmitsKeysOnlyWhenSkipped(t *testing.T) {
	s, st, key, uid := f42Setup(t)

	// ① 有跳过：来源第六档 + 两键同层出栈（文档承诺的那一句必须真落地）。
	withSkip := f42APITicket(t, st, uid, `{
		"translations":{"en":"紫火核弹T36的描述"},
		"sources":{"en":"compliance_skipped"},
		"compliance_skipped":{"en":1},
		"compliance_items":[{"lang":"en","line":1,"terms":["紫火核弹T36"],"side":"input",
			"lead":"内容合规审核：该处未翻译，原文已按原样保留"}]
	}`, false, "")
	w := f42Get(t, s, s.handleOpenAPITaskStatus, key, "id="+strconv.FormatInt(withSkip.ID, 10))
	var got map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("响应解析失败：%v %s", err, w.Body.String())
	}
	if st := strings.TrimSpace(string(got["status"])); st != `"completed"` {
		t.Fatalf("前置不成立：任务应为 completed，实得 %s（响应体 %s）", st, w.Body.String())
	}
	for _, p := range complianceOutboundProblems(got, true) {
		t.Error(p)
	}
	// 逐条素材五键必须原样带出（客户按 line 定位、按 lead 展示）。
	var items []orchestrator.ComplianceItem
	if err := json.Unmarshal(got["compliance_items"], &items); err != nil {
		t.Fatalf("compliance_items 解析失败：%v", err)
	}
	if len(items) != 1 || items[0].Line != 1 || items[0].Side != orchestrator.ComplianceSideInput ||
		len(items[0].Terms) != 1 || items[0].Terms[0] != "紫火核弹T36" || items[0].Lead == "" {
		t.Errorf("compliance_items 五键没原样出栈，实得 %+v", items)
	}
	if s := strings.TrimSpace(string(got["sources"])); !strings.Contains(s, orchestrator.SourceComplianceSkipped) {
		t.Errorf("sources 应含第六档 %q，实得 %s", orchestrator.SourceComplianceSkipped, s)
	}

	// ② 没跳过：两键**都不许出**（缺席优先，§25-b 那条 points_free 同型）。
	noSkip := f42APITicket(t, st, uid,
		`{"translations":{"en":"plain translation"},"sources":{"en":"model"}}`, false, "")
	w2 := f42Get(t, s, s.handleOpenAPITaskStatus, key, "id="+strconv.FormatInt(noSkip.ID, 10))
	var got2 map[string]json.RawMessage
	if err := json.Unmarshal(w2.Body.Bytes(), &got2); err != nil {
		t.Fatalf("对照单响应解析失败：%v %s", err, w2.Body.String())
	}
	if st := strings.TrimSpace(string(got2["status"])); st != `"completed"` {
		t.Fatalf("前置不成立：对照单应为 completed，实得 %s", st)
	}
	for _, p := range complianceOutboundProblems(got2, false) {
		t.Error(p)
	}
}

// L5：SDK 类型面（第四面）必须与常量同源——TypeScript 的 `SourceKind` 联合类型就是
// SDK 客户的分支语言，少一档他们会在编译期把 `compliance_skipped` 判成非法值。
// 期望值仍从 `Source*` 常量现取（不在测试里手抄）。
func TestOpenAPIComplianceSdkTypesMatchConstants(t *testing.T) {
	raw, err := os.ReadFile("../../../sdk/typescript/src/index.ts")
	if err != nil {
		t.Fatalf("读 TypeScript SDK 失败（仓库结构变了？）：%v", err)
	}
	src := string(raw)
	var probs []string
	probs = append(probs, complianceTSUnionProblems(src, "SourceKind", complianceSourceValues())...)
	// 两个新出栈键必须进 TaskStatus（否则 SDK 客户按类型取属性会编译不过）。
	taskStatus := complianceTSInterfaceBody(src, "TaskStatus")
	if taskStatus == "" {
		t.Fatalf("TypeScript SDK 里找不到 TaskStatus 接口（锚点漂了）")
	}
	for _, key := range []string{"compliance_skipped", "compliance_items"} {
		if !strings.Contains(taskStatus, key) {
			probs = append(probs, "TypeScript TaskStatus 缺字段 "+key)
		}
	}
	// 两份 SDK README 都要写得出来（Python 侧返回原 dict，没有类型可改，只能靠文档）。
	for _, path := range []string{"../../../sdk/python/README.md", "../../../sdk/typescript/README.md"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读 %s 失败：%v", path, err)
		}
		if !strings.Contains(string(b), orchestrator.SourceComplianceSkipped) {
			probs = append(probs, path+" 没有点名第六档 "+orchestrator.SourceComplianceSkipped)
		}
	}
	for _, p := range probs {
		t.Error(p)
	}
}

// complianceTSUnionProblems 判据本体：TS 的类型联合必须逐值等值于给定名单。
func complianceTSUnionProblems(src, typeName string, want []string) []string {
	marker := "export type " + typeName + " ="
	i := strings.Index(src, marker)
	if i < 0 {
		return []string{"TypeScript SDK 里找不到类型 " + typeName + "（锚点漂了）"}
	}
	rest := src[i+len(marker):]
	if j := strings.Index(rest, ";"); j >= 0 {
		rest = rest[:j]
	}
	var probs []string
	for _, v := range want {
		if !strings.Contains(rest, `"`+v+`"`) {
			probs = append(probs, "TypeScript "+typeName+" 缺来源档 \""+v+"\"")
		}
	}
	return probs
}

// complianceTSInterfaceBody 取 `export interface <name> { ... }` 的花括号区间（按花括号配平，
// 不用"下一个顶层声明"那种切法——它在本仓的注释风格下会切过头，见 §一·5 doubleConfirmGate 那条）。
func complianceTSInterfaceBody(src, name string) string {
	marker := "export interface " + name + " {"
	i := strings.Index(src, marker)
	if i < 0 {
		return ""
	}
	start := i + len(marker)
	depth := 1
	for p := start; p < len(src); p++ {
		switch src[p] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[start:p]
			}
		}
	}
	return ""
}

// L4 反证：两把判据各自喂变异输入必须报问题（证明腿有射程，不是恒真）。
func TestOpenAPIComplianceGateReverseProofs(t *testing.T) {
	// R1：规范 enum 少一档 / 多一档，都必须被抓到。
	short := []string{orchestrator.SourceKB, orchestrator.SourceModel,
		orchestrator.SourceModelWithFallback, orchestrator.SourceUpstreamFailed, orchestrator.SourceApproved}
	if len(complianceSourceSetProblems(short, complianceSourceValues())) == 0 {
		t.Error("R1 反射失效：enum 少了第六档却没被判红")
	}
	extra := append(append([]string{}, complianceSourceValues()...), "brand_new_source")
	if len(complianceSourceSetProblems(extra, complianceSourceValues())) == 0 {
		t.Error("R1 反射失效：enum 多出未登记档却没被判红")
	}
	if len(complianceSourceSetProblems(complianceSourceValues(), complianceSourceValues())) != 0 {
		t.Error("R1 反射失效：完全等值时却判红（判据自伤）")
	}

	// R2：文档少了第六档 / 少了互斥说明，都必须被抓到。
	if len(complianceDocProblems("kb model model_with_source_fallback upstream_failed approved")) == 0 {
		t.Error("R2 反射失效：文档完全没写第六档却没被判红")
	}
	if len(complianceDocProblems(orchestrator.SourceComplianceSkipped+" "+orchestrator.SourceComplianceSkipped)) == 0 {
		t.Error("R2 反射失效：只写了字面量、没写「不计入未译出三档」却没被判红")
	}
	var buf strings.Builder
	for i := 0; i < 2; i++ {
		buf.WriteString(orchestrator.SourceComplianceSkipped + "\n不计入 unresolved_lines not counted in unresolved_lines\n")
	}
	if len(complianceDocProblems(buf.String())) != 0 {
		t.Error("R2 反射失效：完全合规的文档却判红（判据自伤）")
	}

	// R3：出栈判据两侧各喂一次坏形态。
	old := map[string]json.RawMessage{
		"compliance_skipped": json.RawMessage(`{}`),
		"compliance_items":   json.RawMessage(`[]`),
	}
	if len(complianceOutboundProblems(old, false)) < 2 {
		t.Error("R3 反射失效：旧形态（没跳过也照出两个空键）没被判红")
	}
	if len(complianceOutboundProblems(map[string]json.RawMessage{}, true)) < 2 {
		t.Error("R3 反射失效：有跳过却两键全缺没被判红")
	}
	good := map[string]json.RawMessage{
		"compliance_skipped": json.RawMessage(`{"en":1}`),
		"compliance_items":   json.RawMessage(`[{"lang":"en","line":1,"side":"input"}]`),
	}
	if len(complianceOutboundProblems(good, true)) != 0 {
		t.Error("R3 反射失效：合规形态却判红（判据自伤）")
	}
	if len(complianceOutboundProblems(good, false)) < 2 {
		t.Error("R3 反射失效：两键都在却按「该缺席」判时没被判红")
	}

	// R4：SDK 类型判据两侧各喂一次。
	tsOK := "export type SourceKind =\n  | \"kb\"\n  | \"model\"\n  | \"model_with_source_fallback\"\n" +
		"  | \"upstream_failed\"\n  | \"approved\"\n  | \"compliance_skipped\";\n"
	if len(complianceTSUnionProblems(tsOK, "SourceKind", complianceSourceValues())) != 0 {
		t.Error("R4 反射失效：完整联合类型却判红（判据自伤）")
	}
	if len(complianceTSUnionProblems("export type SourceKind = \"kb\" | \"model\";", "SourceKind",
		complianceSourceValues())) == 0 {
		t.Error("R4 反射失效：联合类型缺四档却没被判红")
	}
	body := "export interface TaskStatus {\n  task_id: number;\n  compliance_items?: ComplianceItem[];\n}\n"
	if got := complianceTSInterfaceBody(body, "TaskStatus"); !strings.Contains(got, "compliance_items") ||
		strings.Contains(got, "compliance_skipped") {
		t.Errorf("R4 反射失效：接口体切分不对，实得 %q", got)
	}
}
