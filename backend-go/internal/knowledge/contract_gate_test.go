// ============ contract_gate_test.go · KN1 契约三方等值闸门 ============
// 四期 W3-1 / KN1，2026-11-15 收口重写。
//
// ★ 原形态只有三条 reflect.DeepEqual：canonical / ClientFace() / api.ServerFace()。
//
//	而 ClientFace() 与 ServerFace() 的实现都逐字是 `return contract.CanonicalFace()`
//	—— 三方比较是**同一个函数的同一个返回值**，结构上恒真。它可以拦住的只有
//	「有人把某一面改成手写清单」（此时三条等值会红），拦不住这一族真正的缺陷：
//	**契约里登记了一条路径，而某一面根本没在用它**（请求 404 / 客户端永远不读）。
//
//	所以本文件现在有两条腿，缺一不可：
//	  ① 反射等值（原三条）：DTO 字段集 / 枚举集 / 版本。防「某一面改成手抄表」。
//	  ② 源码射程（新增）：**逐条问「这条路径有没有人在用」**——
//	     · 服务端（internal/knowledge/api）：EndpointPaths 里每一条都必须被显式引用，
//	       少一条＝路由表登记了但 dispatch 不认识 ⇒ 请求 404；
//	     · 客户端（internal/knowledge/client.go）：引用的路径必须全部登记在契约里
//	       （不许出现契约外的孤儿路径），且引用条数有下界（防「客户端被架空成空壳」）；
//	     · 全树：`/internal/knowledge/` 字面量只许出现在契约包内
//	       （别处写死一条路径＝契约拦不住它，换版本也不会红）。
//
// ⚠️ 为什么 ② 不做成「客户端手写清单 vs 服务端手写清单」：那会造出**第三份**手抄表，
//
//	而且 client.go 的路径本来就取自 contract 常量 —— 扫「用了哪些常量」才是真实射程。
//
// 变异体：M0358 —— 往 EndpointPaths 追加一条契约里没有实现的路由（PathBogus）⇒ ② 服务端腿红。
// ============================================================
package knowledge

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"translator/internal/knowledge/api"
	"translator/internal/knowledge/contract"
)

// kn1ConstRe 从契约包源码里读「常量名 → 路径字面量」。
// 之所以要解析源码而不是反射：Go 的常量名不进反射，而「有没有人在用这个常量」
// 只能按名字在源码里数。顺带用它校「EndpointPaths 里的每一项都来自一个具名常量」。
var kn1ConstRe = regexp.MustCompile(`(?m)^\s*(Path[A-Za-z0-9]+)\s*=\s*"([^"]+)"`)

// kn1PathRefRe 源码里对契约路径常量的引用。
var kn1PathRefRe = regexp.MustCompile(`contract\.(Path[A-Za-z0-9]+)`)

// kn1BarePathRe 契约包之外写死的知识服务路径字面量。
var kn1BarePathRe = regexp.MustCompile(`"/internal/knowledge/[^"]*"`)

// kn1ServerOnlyPaths 契约里「服务端 / 探针专用」的路径常量：客户端不直接发起请求。
//
// ★ 方案 §2.3 第 1 条要求客户端引用集合 ⊇ EndpointPaths。现状这四条由服务端 dispatch、
//
//	健康探针与事实准入读口消费，客户端只是不调用它们；为凑数硬造四个空方法会造出
//	「看起来接通了」的假能力，正是本批要根治的形态。故改成**显式极小豁免**：
//	每条仍由 TestKN1ServerRoutesCoverEveryEndpointPath 的服务端 ⊇ 腿逐条兜住，
//	「契约登记了却没人用」依然会红；豁免清单本身也不许膨胀（超过 4 条即判红）。
var kn1ServerOnlyPaths = map[string]bool{
	"PathAnswer":        true,
	"PathFactsAdmitted": true,
	"PathI18nList":      true,
	"PathHealth":        true,
}

// kn1BackendRoot 返回 backend-go 根（本文件在 backend-go/internal/knowledge/）。
func kn1BackendRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("KN1 取当前测试文件位置失败（射程未知）")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

// kn1GoFiles 列出 root 下全部非测试 .go 文件；skipDirs 里的目录名整棵跳过。
// 参数：root=扫描根；skipDirs=目录名集合；返回文件路径列表。
func kn1GoFiles(t *testing.T, root string, skipDirs map[string]bool) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, e os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if e.IsDir() {
			if skipDirs[e.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		out = append(out, p)
		return nil
	})
	if err != nil {
		t.Fatalf("KN1 扫描 %s 失败: %v", root, err)
	}
	return out
}

// kn1ConstTable 解析契约包源码，返回 常量名→路径值、以及一个「按值反查名字」的表。
func kn1ConstTable(t *testing.T, contractSrc string) (map[string]string, map[string]string) {
	t.Helper()
	nameToValue := map[string]string{}
	valueToName := map[string]string{}
	for _, m := range kn1ConstRe.FindAllStringSubmatch(contractSrc, -1) {
		name, val := m[1], m[2]
		nameToValue[name] = val
		if _, dup := valueToName[val]; dup {
			t.Fatalf("KN1 契约里两条常量指向同一路径 %q（路由会互相覆盖）", val)
		}
		valueToName[val] = name
	}
	if len(nameToValue) == 0 {
		t.Fatal("KN1 射程失明：契约包源码里没解析出任何 Path* 常量（正则锚点漂了）")
	}
	return nameToValue, valueToName
}

// kn1RefsIn 统计 dir 下非测试 .go 里引用到的契约路径常量名集合。
func kn1RefsIn(t *testing.T, dir string) map[string]bool {
	t.Helper()
	refs := map[string]bool{}
	files := kn1GoFiles(t, dir, nil)
	if len(files) == 0 {
		t.Fatalf("KN1 射程失明：%s 下一个非测试 .go 都没有", dir)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("KN1 读 %s 失败: %v", f, err)
		}
		for _, m := range kn1PathRefRe.FindAllStringSubmatch(string(b), -1) {
			refs[m[1]] = true
		}
	}
	return refs
}

// TestKN1ThreeFacesEqual 反射等值（DTO 字段 / 枚举 / 版本）。
//
// ⚠️ 这三条比的是**同一个函数的产物**（两个 Face 都 return contract.CanonicalFace()），
// 因此它们能拦的只有「某一面被改成手写清单」这一种形态。真正的射程在下面两条源码腿。
func TestKN1ThreeFacesEqual(t *testing.T) {
	canonical := contract.CanonicalFace()
	client := ClientFace()
	server := api.ServerFace()

	if !reflect.DeepEqual(canonical, client) {
		t.Fatalf("KN1 三方等值失败：客户端面与契约包不等（客户端被改成手写清单就会在此红）")
	}
	if !reflect.DeepEqual(canonical, server) {
		t.Fatalf("KN1 三方等值失败：服务端面与契约包不等（服务端被改成手写清单就会在此红）")
	}
	// 判据现算（不写死 DTO/枚举名单）：字段集、枚举集、路径集都必须非空。
	if len(canonical.DTOFields) == 0 || len(canonical.Enums) == 0 || len(canonical.Paths) != len(contract.EndpointPaths) {
		t.Fatalf("KN1 契约面为空/路径数不对: dto=%d enums=%d paths=%d",
			len(canonical.DTOFields), len(canonical.Enums), len(canonical.Paths))
	}
	if canonical.Version != contract.ContractVersion {
		t.Fatalf("KN1 契约版本不等: %s != %s", canonical.Version, contract.ContractVersion)
	}
	for name, fs := range canonical.DTOFields {
		if len(fs) == 0 {
			t.Fatalf("KN1 DTO %s 零字段（判据失明）", name)
		}
	}
	for name, vs := range canonical.Enums {
		if len(vs) == 0 {
			t.Fatalf("KN1 枚举 %s 零值（判据失明）", name)
		}
	}
}

// TestKN1ServerRoutesCoverEveryEndpointPath 契约登记了每一条路径，服务端就必须都认。
//
// 判据：EndpointPaths 里每一项都要能反查到**一个具名常量**，且那个常量名必须出现在
// internal/knowledge/api 的源码里。少一条＝ /internal/... 路由被 mux 注册了（Handler 的循环
// 无条件注册），但 dispatch 不认识 ⇒ 客户端拿到 404，而三方等值照旧全绿。
func TestKN1ServerRoutesCoverEveryEndpointPath(t *testing.T) {
	backend := kn1BackendRoot(t)
	contractSrc, err := os.ReadFile(filepath.Join(backend, "internal", "knowledge", "contract", "contract.go"))
	if err != nil {
		t.Fatalf("KN1 读契约源码失败: %v", err)
	}
	nameToValue, valueToName := kn1ConstTable(t, string(contractSrc))

	// ⓪ 正反向：EndpointPaths 与常量表必须一一对应（防「只往 EndpointPaths 塞字面量」）。
	if len(contract.EndpointPaths) != len(nameToValue) {
		var unregistered []string
		for _, p := range contract.EndpointPaths {
			if _, ok := valueToName[p]; !ok {
				unregistered = append(unregistered, p)
			}
		}
		sort.Strings(unregistered)
		t.Fatalf("KN1 契约不完整：EndpointPaths 有 %d 条，而具名常量只有 %d 条；"+
			"未具名的路径 %v —— 写成字面量就没人能在源码里引用它", len(contract.EndpointPaths), len(nameToValue), unregistered)
	}
	for name, val := range nameToValue {
		if !containsStr(contract.EndpointPaths, val) {
			t.Fatalf("KN1 契约不完整：常量 %s=%q 没登记进 EndpointPaths（路由不会注册、客户端也不知道它存在）", name, val)
		}
	}

	// ① 主体：服务端源码必须引用每一条路径的常量。
	refs := kn1RefsIn(t, filepath.Join(backend, "internal", "knowledge", "api"))
	var missing []string
	for _, p := range contract.EndpointPaths {
		name := valueToName[p]
		if !refs[name] {
			missing = append(missing, name+" ("+p+")")
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("KN1 服务端射程不足：端点路径已登记但 internal/knowledge/api 从不引用 ⇒ 请求会 404：%v", missing)
	}
}

// TestKN1ClientStaysWithinContract 客户端不许出现契约外的孤儿路径，且不许被架空成空壳。
func TestKN1ClientStaysWithinContract(t *testing.T) {
	backend := kn1BackendRoot(t)
	contractSrc, err := os.ReadFile(filepath.Join(backend, "internal", "knowledge", "contract", "contract.go"))
	if err != nil {
		t.Fatalf("KN1 读契约源码失败: %v", err)
	}
	nameToValue, _ := kn1ConstTable(t, string(contractSrc))

	clientSrc, err := os.ReadFile(filepath.Join(backend, "internal", "knowledge", "client.go"))
	if err != nil {
		t.Fatalf("KN1 读客户端源码失败: %v", err)
	}
	refs := map[string]bool{}
	for _, m := range kn1PathRefRe.FindAllStringSubmatch(string(clientSrc), -1) {
		refs[m[1]] = true
	}
	if len(refs) == 0 {
		t.Fatal("KN1 射程失明：client.go 里一条 contract.Path* 都没引用（客户端被架空或改成写死路径）")
	}
	// 下界 15：现读客户端引用 17 条（21 条里 answer/facts_admitted/i18n_list/health 走服务端或探针）。
	// 这是「比现状短一大截」的探测器，不是镜像 —— 合法删除一条不会红（同 AGENTS §一·14）。
	if len(refs) < 15 {
		t.Fatalf("KN1 客户端引用路径数 %d 低于下界 15 —— 客户端被架空（绿是假的）", len(refs))
	}
	var orphans []string
	for name := range refs {
		if _, ok := nameToValue[name]; !ok {
			orphans = append(orphans, "contract."+name)
		}
	}
	if len(orphans) > 0 {
		sort.Strings(orphans)
		t.Fatalf("KN1 客户端引用了契约里不存在的路径常量 %v（客户端会打到一个没人实现的路由）", orphans)
	}
	// ★ 客户端 ⊇ 判据（方案 §2.3 第 1 条，带极小豁免）：除服务端专用的四条外，
	//   client.go 必须引用契约里的**每一条**具名路径常量；少一条＝这条契约没人调。
	//   只用「引用数下界」是会被架空成空壳的（删掉十条仍可能过 15 条下界），
	//   逐条问名字才有射程。
	if len(kn1ServerOnlyPaths) > 4 {
		t.Fatalf("KN1 豁免清单膨胀到 %d 条（上限 4）⇒ 有人靠往里塞路径绕过客户端射程", len(kn1ServerOnlyPaths))
	}
	var missing []string
	for name := range nameToValue {
		if kn1ServerOnlyPaths[name] {
			continue
		}
		if !refs[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("KN1 客户端射程不足：契约登记了但这些路径客户端从不引用 ⇒ %v", missing)
	}
}

// TestKN1NoBareKnowledgePathLiterals 契约包之外不许写死知识服务路径字面量。
//
// 写死一处 =\u003e 换契约版本、改路径、删路由，它都不会红；而线上是「有一台还在打旧路径」。
func TestKN1NoBareKnowledgePathLiterals(t *testing.T) {
	backend := kn1BackendRoot(t)
	skip := map[string]bool{"contract": true}
	var offenders []string
	files := 0
	for _, root := range []string{filepath.Join(backend, "internal"), filepath.Join(backend, "cmd")} {
		for _, f := range kn1GoFiles(t, root, skip) {
			files++
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("KN1 读 %s 失败: %v", f, err)
			}
			for _, hit := range kn1BarePathRe.FindAllString(string(b), -1) {
				offenders = append(offenders, f+" ⇒ "+hit)
			}
		}
	}
	// 射程下界：现读 backend-go 的 internal+cmd 非测试 .go 在 500+ 量级。
	if files < 400 {
		t.Fatalf("KN1 射程失明：只扫到 %d 个非测试 .go 文件（下界 400）——“没有写死路径”这句话是假的", files)
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Fatalf("KN1 契约包外出现写死的知识服务路径（改契约时它不会跟着变，线上会打旧路径）：\n%s",
			strings.Join(offenders, "\n"))
	}
}

// TestKN1MutantDiscriminatesExtraClientField 反证判别力：
// 客户端多一个字段 / 多一个枚举值 / 多一条路径，等值比较器必须判为不等（不是空转锁）。
func TestKN1MutantDiscriminatesExtraClientField(t *testing.T) {
	mutant := ClientFace()
	mutant.DTOFields["I18nListResponse"] = append(mutant.DTOFields["I18nListResponse"], "Extra|extra")
	if reflect.DeepEqual(contract.CanonicalFace(), mutant) {
		t.Fatalf("KN1 判据失明：客户端多一个字段却判等值（空转锁）")
	}
	mutant2 := ClientFace()
	mutant2.Enums["health_state"] = append(mutant2.Enums["health_state"], "bogus")
	if reflect.DeepEqual(contract.CanonicalFace(), mutant2) {
		t.Fatalf("KN1 判据失明：多一个枚举值却判等值")
	}
	mutant3 := ClientFace()
	mutant3.Paths = append(mutant3.Paths, "/internal/knowledge/bogus")
	if reflect.DeepEqual(contract.CanonicalFace(), mutant3) {
		t.Fatalf("KN1 判据失明：多一个路径却判等值")
	}
}

// containsStr 字面等值成员判定（不引 slices，保证 Go 版本无关）。
func containsStr(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
