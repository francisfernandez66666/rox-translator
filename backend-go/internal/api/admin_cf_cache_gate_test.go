// ============ admin_cf_cache_gate_test.go · 职责说明 ============
// ★ 2026-11-02 用户令「CF 的 token 配置放在管理后台，平台管理员配置并可热更新」的门禁。
//
// 十九腿，问的都是"这份凭据真的只有一份事实、且改了立刻算"那一族：
//
//	A 未配置态（source=none／默认 zone／默认导出路径都在场）
//	B 保存＝密文落库＋出栈零明文＋导出文件真写成＋模式 0600
//	C 掩码回写＝未改动（绝不把掩码写回库，历史踩坑）
//	D 留空＝未改动
//	E ★ 热更新：再存一把 ⇒ 读侧当场是新值（不重启、不换件），且导出文件跟着翻
//	F 显式清空 ⇒ 库里空 **且导出文件被删**（留着文件＝执行体继续拿上一把密钥，比「没清」更坏）
//	G env 占位 ⇒ 不导出（一次界面保存不许改写运维 env 的语义）
//	H 鉴权分流：匿名 401／租户管理员 403／方法闸 405
//	I purge 正测：先现查 zone（URL 形状判）→ 再打 purge_cache；UA 必须带可联系的产品址
//	J purge 两档失败分开报（取不到 zone ⇒ 令牌 Zone 读取权限；success:false ⇒ Cache Purge: Edit）
//	K 留痕只出状态，不许含凭据
//	L 顺序锁：写文件必须晚于**最后一个** SetConfig（先落库、后碰文件侧）
//	M zone 名判据表驱动（脏值拒收，脏值落库的表现是"purge 打了个查不到的地方"）
//	N 导出失败不改判保存失败，但必须出声（file_written=false ＋ 一句可读原因）
//	O 被拒的保存必须**一个字节都不落地**（先验后写；旧形态会留下"新凭据＋旧 zone"的半套配置）
//	P 换导出路径／填 none ⇒ **旧位置那份投影必须删掉**（留着＝换个地方继续拿旧密钥清缓存）
//	R 旧投影删不掉必须出声但不判保存失败（新位置已写成，旧位置仍在＝两条腿都要在读数里）
//	S 客户可见中文全部翻得出英文（错误面＋成功面 file_note；中文语境原样透传作反向对照）
//	T 默认导出路径与执行体同源、且落在**服务账号写得进去**的地方（不是在 /etc 下）
//
// ★ 夹具方言自钉由 newEstCfgFixture 承担（AGENTS §一·4）；假 CF 服务器只认协议——
//
//	按"我以为的响应形状"实现桩＝给缺陷发证件（AGENTS §三 那条）。
//
// ==========================================
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"translator/internal/i18n"
	"translator/internal/store"
)

// cfFixture 复用预估配置的夹具，并把导出路径显式关掉（哨兵 none）。
//
// 为什么每条腿都要先关：不关的话本文件的保存腿会去写 /opt/translator/data/cf_token——
// 开发机上它不存在（报一条 WARN 后 pass），**部署机上目录是真的**，于是一次 `go test`
// 会把测试凭据落到生产机那把真文件上（测试污染现网配置，执行体下一次就读到假密钥）。
// 需要验导出的用例自己调用 cfUseFile 覆盖回来。
func cfFixture(t *testing.T) *estCfgFixture {
	t.Helper()
	f := newEstCfgFixture(t)
	if err := f.st.SetConfig("cf_token_file", "none"); err != nil {
		t.Fatalf("关掉导出失败: %v", err)
	}
	return f
}

// 夹具常量：两条路由路径 + 两把可辨认的假凭据 + 一条真形状的 zones 响应。
// 假凭据刻意带 `_PLAIN_` 字样，用来在响应体里做"明文绝不出现"的负向判据。
const (
	cfCachePath  = "/api/admin/ops/cf-cache"
	cfPurgePath  = "/api/admin/ops/cf-cache/purge"
	cfPlainTok   = "cfut_PLAIN_0123456789abcdef"
	cfPlainTok2  = "cfut_SECOND_0123456789abcdef"
	cfZoneHeader = `{"success":true,"result":[{"id":"0123456789abcdef0123456789abcdef","name":"lexicorn.cn"}]}`
)

// cfMux 与生产注册同路径、同分派口（挂两个 handler 到同一前缀＝鉴权与审计迟早分叉）。
func (s *Server) cfMux() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc(cfCachePath, s.handleAdminCFCache)
	m.HandleFunc(cfPurgePath, s.handleAdminCFCachePurge)
	return m
}

// cfCacheCall 打一次接口，返回状态码、原始响应体与解析结果。
func cfCacheCall(t *testing.T, h http.Handler, path, method, body, tok string) (int, string, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out := map[string]interface{}{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, rec.Body.String(), out
}

// cfUseFile 把导出路径指到本次用例的临时目录（生产默认是 /opt/translator/data/cf_token，测试绝不写那里）。
func cfUseFile(t *testing.T, f *estCfgFixture, p string) {
	t.Helper()
	if err := f.st.SetConfig("cf_token_file", p); err != nil {
		t.Fatalf("设置导出路径失败: %v", err)
	}
}

// A：未配置态。
// 这一条**故意不关掉导出**（用裸夹具）：它要证的正是"库里没表态时默认路径落在哪"，
// 而默认值判据只能在没被显式覆盖的库上跑；本条全程只做一次 GET，不会碰磁盘。
func TestCFCacheUnsetShowsDefaults(t *testing.T) {
	f := newEstCfgFixture(t)
	code, raw, body := cfCacheCall(t, f.srv.cfMux(), cfCachePath, http.MethodGet, "", f.super)
	if code != http.StatusOK {
		t.Fatalf("超管读取应 200，实得 %d body=%s", code, raw)
	}
	if body["set"] != false || body["source"] != "none" {
		t.Fatalf("未配置态必须如实报 set=false／source=none，实得 %v／%v", body["set"], body["source"])
	}
	if body["zone"] != "lexicorn.cn" || body["token_file"] != "/opt/translator/data/cf_token" {
		t.Fatalf("默认 zone／默认导出路径必须在场（缺了＝前端只能自己抄一份默认）：%v %v", body["zone"], body["token_file"])
	}
}

// T：默认导出路径必须与执行体同源，且必须落在**服务账号写得进去**的目录下。
//
// 为什么这条是硬锁而不是纪律：后台把 token 写到 A、执行体去 B 读，表现是"保存成功、徽标在场、
// 发版永远 skipped"——两个读数各自都正常，谁也指不到对方（同 AGENTS §一·5 批④ 那族
// "有自动降级的链路，产物能打开不算验收"）。而 `/etc` 下的路径**本机测试永远写得进去**
// （跑测试的是 root/开发账号），只有服务账号写不进去 ⇒ 这一档只能靠现网自证 + 这条同源锁。
func TestCFCacheTokenFileDefaultMatchesReleaseScript(t *testing.T) {
	raw, err := os.ReadFile("../../../deploy/swap_web_and_binary.sh")
	if err != nil {
		t.Fatalf("读不到换源执行体（判据此刻没有输入＝空转）：%v", err)
	}
	re := regexp.MustCompile(`(?m)^CF_TOKEN_FILE=\$\{CF_TOKEN_FILE:-([^}]+)\}`)
	m := re.FindStringSubmatch(string(raw))
	if len(m) != 2 {
		t.Fatalf("执行体里读不到 CF_TOKEN_FILE 的默认档（锚点漂了＝本判据已失效，别删它）")
	}
	if m[1] != cfTokenFileDefault {
		t.Fatalf("默认导出路径两处不一致：后台写 %q，执行体读 %q ⇒ 保存成功而发版永远 skipped",
			cfTokenFileDefault, m[1])
	}
	if !strings.HasPrefix(cfTokenFileDefault, "/") {
		t.Fatalf("默认导出路径必须是绝对路径，实得 %q", cfTokenFileDefault)
	}
	// /etc 下服务写不进去（现网实测：`/etc/translator` 是 root:root 0750，unit 又以 translator 跑
	// 且 ProtectSystem=full）⇒ 放那儿＝导出恒失败。这一条问的是**形态**（路径属于哪一族），
	// 不是"这个目录此刻存不存在"（跑测试的机器上没有那条 unit）。
	if strings.HasPrefix(cfTokenFileDefault, "/etc/") {
		t.Fatalf("默认导出路径落在 /etc 下（%s）：服务账号穿不过目录也写不进去 ⇒ 导出恒失败、执行体恒 skipped",
			cfTokenFileDefault)
	}
}

// B：保存＝密文落库、出栈零明文、导出文件写成且 0600。
func TestCFCacheSaveEncryptsAndExports(t *testing.T) {
	f := cfFixture(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "cf_token")
	cfUseFile(t, f, out)
	h := f.srv.cfMux()

	code, raw, body := cfCacheCall(t, h, cfCachePath, http.MethodPost, `{"token":"`+cfPlainTok+`"}`, f.super)
	if code != http.StatusOK || body["success"] != true {
		t.Fatalf("保存应 200/success，实得 %d body=%s", code, raw)
	}
	if body["changed"] != true {
		t.Fatalf("真换了凭据却报 changed=false（保存被当成空操作）：%s", raw)
	}
	if body["file_written"] != true {
		t.Fatalf("库已存但导出文件没写成，file_note=%v ⇒ 下一次发版仍会 skipped", body["file_note"])
	}
	// ① 库里必须是密文
	stored, err := f.st.GetConfig("cf_api_token")
	if err != nil || !strings.HasPrefix(stored, "enc:v1:") {
		t.Fatalf("凭据必须加密落库（enc:v1: 前缀），实得 %q", stored)
	}
	if strings.Contains(stored, cfPlainTok) {
		t.Fatalf("库里出现明文凭据")
	}
	if got := store.DecryptSecret(stored); got != cfPlainTok {
		t.Fatalf("库里那份解回来不是刚存的值（got=%q）⇒ 读侧与执行体拿到的是另一串", got)
	}
	// ② 出栈必须零明文，只回掩码
	if strings.Contains(raw, cfPlainTok) {
		t.Fatalf("响应体里出现明文凭据（明文进浏览器＝进代理日志与 trace）：%s", raw)
	}
	m, _ := body["masked"].(string)
	if m == "" || m == cfPlainTok || !strings.Contains(m, "****") {
		t.Fatalf("masked 必须是掩码形态，实得 %q", m)
	}
	// ③ 文件内容＝明文一把，模式 0600
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("导出文件没落盘: %v", err)
	}
	if strings.TrimSpace(string(data)) != cfPlainTok {
		t.Fatalf("导出文件内容不对（执行体拿这把去清缓存）：%q", string(data))
	}
	st, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat 导出文件失败: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0600 {
		t.Fatalf("导出文件模式必须是 0600（执行体按\"组/其他有可读位即拒读\"判），实得 %s", perm)
	}
	// ④ 审计留痕
	if n := cfTauditCount(t, f, "cf_cache_save"); n != 1 {
		t.Fatalf("cf_cache_save 审计应 1 条，实得 %d", n)
	}
}

// C＋D：掩码回写与留空都＝未改动（绝不把掩码写回库）。
func TestCFCacheMaskedAndBlankSaveAreNoOps(t *testing.T) {
	f := cfFixture(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "cf_token")
	cfUseFile(t, f, out)
	h := f.srv.cfMux()
	if _, _, b := cfCacheCall(t, h, cfCachePath, http.MethodPost, `{"token":"`+cfPlainTok+`"}`, f.super); b["changed"] != true {
		t.Fatalf("前置保存没落地：%v", b)
	}
	before, _ := f.st.GetConfig("cf_api_token")

	for _, body := range []string{`{"token":"cfut****efgh"}`, `{"token":""}`, `{"token":"   "}`} {
		code, raw, b := cfCacheCall(t, h, cfCachePath, http.MethodPost, body, f.super)
		if code != http.StatusOK {
			t.Fatalf("%s 应 200，实得 %d %s", body, code, raw)
		}
		if b["changed"] != false {
			t.Fatalf("%s 被当成改动 ⇒ 掩码/空值会覆盖真凭据", body)
		}
	}
	after, _ := f.st.GetConfig("cf_api_token")
	if after != before {
		t.Fatalf("库里凭据被掩码/空值改写（旧值 %q → 新值 %q）", before, after)
	}
}

// E：★ 热更新——再存一把，读侧与导出文件当场翻成新值（不重启、不换件）。
func TestCFCacheHotReloadTakesEffectNow(t *testing.T) {
	f := cfFixture(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "cf_token")
	cfUseFile(t, f, out)
	h := f.srv.cfMux()
	cfCacheCall(t, h, cfCachePath, http.MethodPost, `{"token":"`+cfPlainTok+`"}`, f.super)

	tok, src := f.srv.effectiveCFToken()
	if tok != cfPlainTok || src != "db" {
		t.Fatalf("第一把未生效（tok=%q src=%q）⇒ 热更新判据失去基线", tok, src)
	}
	if _, _, b := cfCacheCall(t, h, cfCachePath, http.MethodPost, `{"token":"`+cfPlainTok2+`"}`, f.super); b["file_written"] != true {
		t.Fatalf("第二把保存后导出文件没跟上：%v", b)
	}
	tok2, src2 := f.srv.effectiveCFToken()
	if tok2 != cfPlainTok2 || src2 != "db" {
		t.Fatalf("读侧没跟上（期望 %q，实得 %q src=%q）⇒ 所谓热更新是假的", cfPlainTok2, tok2, src2)
	}
	data, _ := os.ReadFile(out)
	if strings.TrimSpace(string(data)) != cfPlainTok2 {
		t.Fatalf("导出文件还是上一把（%q）⇒ 执行体会拿旧密钥去清缓存", string(data))
	}
}

// F：显式清空 ⇒ 库里空 **且** 导出文件被删。
func TestCFCacheClearRemovesExportedFile(t *testing.T) {
	f := cfFixture(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "cf_token")
	cfUseFile(t, f, out)
	h := f.srv.cfMux()
	cfCacheCall(t, h, cfCachePath, http.MethodPost, `{"token":"`+cfPlainTok+`"}`, f.super)
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("前置导出没写成: %v", err)
	}
	code, raw, b := cfCacheCall(t, h, cfCachePath, http.MethodPost, `{"clear":true}`, f.super)
	if code != http.StatusOK || b["changed"] != true {
		t.Fatalf("清空应 200/changed，实得 %d %s", code, raw)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("凭据已清但导出文件还在（%v）⇒ 执行体继续拿上一把密钥清缓存，比「没清」更坏", err)
	}
	if tok, src := f.srv.effectiveCFToken(); tok != "" || src != "none" {
		t.Fatalf("读侧仍认旧凭据（tok=%q src=%q）", tok, src)
	}
	if n := cfTauditCount(t, f, "cf_cache_clear"); n != 1 {
		t.Fatalf("cf_cache_clear 审计应 1 条，实得 %d", n)
	}
}

// G：env 占着生效位 ⇒ 不导出（一次界面保存不许改写运维 env 的语义）。
func TestCFCacheEnvOwnsEffectiveSlotAndSkipsExport(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "cfut_from_env_not_to_export")
	f := cfFixture(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "cf_token")
	cfUseFile(t, f, out)
	h := f.srv.cfMux()
	_, _, b := cfCacheCall(t, h, cfCachePath, http.MethodPost, `{"token":"`+cfPlainTok+`"}`, f.super)
	if b["env_overridden"] != true {
		t.Fatalf("env 占位必须被如实报出（否则运营以为保存即生效）：%v", b)
	}
	if b["file_written"] != false {
		t.Fatalf("env 生效时不许导出（把 env 的值落进文件＝让一次保存改写部署侧语义）")
	}
	if !strings.Contains(cfTstr(b["file_note"]), "环境变量") {
		t.Fatalf("file_note 必须点明原因（env 占位），实得 %v", b["file_note"])
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("导出文件被写出来了")
	}
}

// H：鉴权分流三态。
func TestCFCacheAuthSplit(t *testing.T) {
	f := cfFixture(t)
	h := f.srv.cfMux()
	for _, c := range []struct {
		name, path, method, tok string
		want                    int
	}{
		{"匿名 GET", cfCachePath, http.MethodGet, "", http.StatusUnauthorized},
		{"匿名 POST", cfCachePath, http.MethodPost, "", http.StatusUnauthorized},
		{"匿名 purge", cfPurgePath, http.MethodPost, "", http.StatusUnauthorized},
		{"租管 GET", cfCachePath, http.MethodGet, f.tenant, http.StatusForbidden},
		{"租管 purge", cfPurgePath, http.MethodPost, f.tenant, http.StatusForbidden},
	} {
		code, raw, _ := cfCacheCall(t, h, c.path, c.method, "{}", c.tok)
		if code != c.want {
			t.Fatalf("%s 应回 %d，实得 %d body=%s", c.name, c.want, code, raw)
		}
	}
	if code, _, _ := cfCacheCall(t, h, cfCachePath, http.MethodPatch, "", f.super); code != http.StatusMethodNotAllowed {
		t.Fatalf("未支持的方法应 405，实得 %d", code)
	}
}

// cfFakeCF 起一台只认 CF 协议的假 API 服务器，并记录收到的请求。
func cfFakeCF(t *testing.T, zoneBody, purgeBody string) (*httptest.Server, *[]string, *[]http.Header) {
	t.Helper()
	var paths []string
	var heads []http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		heads = append(heads, r.Header.Clone())
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/zones") && r.URL.RawQuery != "" {
			_, _ = w.Write([]byte(zoneBody))
			return
		}
		_, _ = w.Write([]byte(purgeBody))
	}))
	t.Cleanup(srv.Close)
	return srv, &paths, &heads
}

// I：purge 正测——先现查 zone、再打 purge_cache；UA 必须带可联系的产品址。
func TestCFCachePurgeDialsZoneThenPurge(t *testing.T) {
	f := cfFixture(t)
	api, paths, heads := cfFakeCF(t, cfZoneHeader, `{"success":true,"errors":[],"messages":[]}`)
	t.Setenv("CF_API_BASE_OVERRIDE", api.URL)
	// 让生效值来自库（清掉 env 那一档的干扰）
	t.Setenv("CLOUDFLARE_API_TOKEN", "")
	h := f.srv.cfMux()
	if _, _, b := cfCacheCall(t, h, cfCachePath, http.MethodPost, `{"token":"`+cfPlainTok+`"}`, f.super); b["changed"] != true {
		t.Fatalf("前置保存失败：%v", b)
	}
	code, raw, body := cfCacheCall(t, h, cfPurgePath, http.MethodPost, "", f.super)
	if code != http.StatusOK || body["ok"] != true {
		t.Fatalf("purge 应 200/ok，实得 %d %s", code, raw)
	}
	p := *paths
	if len(p) != 2 {
		t.Fatalf("应打两枪（先查 zone 再清），实得 %v", p)
	}
	if !strings.Contains(p[0], "/zones?name=lexicorn.cn") {
		t.Fatalf("第一枪必须按 zone 名现查 id（写死 id＝换 zone 后静默打错地方），实得 %q", p[0])
	}
	if !strings.Contains(p[1], "/zones/0123456789abcdef0123456789abcdef/purge_cache") {
		t.Fatalf("第二枪必须打到查到的那个 zone，实得 %q", p[1])
	}
	// ★ UA 判**形状**而不是字面域名：写死产品域会让换域即恒红，而同族真缺陷（缺联系方式）照样过。
	ua := (*heads)[0].Get("User-Agent")
	if !strings.Contains(ua, "https://") || !strings.Contains(ua, "@") {
		t.Fatalf("出站 UA 必须带可联系的产品址与邮箱（缺联系方式时 403/429 而零症状），实得 %q", ua)
	}
	if !strings.Contains(ua, "LangCrossOpsCFPurge") {
		t.Fatalf("UA 必须点名这一腿是谁（对端排障按它认），实得 %q", ua)
	}
	if a := (*heads)[0].Get("Authorization"); !strings.HasPrefix(a, "Bearer ") || strings.Contains(a, "\n") {
		t.Fatalf("Authorization 头必须是 Bearer 单行，实得 %q", a)
	}
	// 凭据不许出现在出参里
	if strings.Contains(raw, cfPlainTok) {
		t.Fatalf("purge 响应体里出现明文凭据")
	}
}

// J：两档失败必须分开报（"去核对读取权限" vs "去核对 Cache Purge: Edit"）。
func TestCFCachePurgeFailuresAreSplit(t *testing.T) {
	f := cfFixture(t)
	t.Setenv("CLOUDFLARE_API_TOKEN", "")
	t.Setenv("CF_API_BASE_OVERRIDE", "https://api.invalid.invalid")
	h := f.srv.cfMux()
	if _, _, b := cfCacheCall(t, h, cfCachePath, http.MethodPost, `{"token":"`+cfPlainTok+`"}`, f.super); b["changed"] != true {
		t.Fatalf("前置保存失败：%v", b)
	}
	// 没配凭据 ⇒ 400 一句可行动文案
	if _, _, b := cfCacheCall(t, h, cfCachePath, http.MethodPost, `{"clear":true}`, f.super); b["changed"] != true {
		t.Fatalf("清空失败：%v", b)
	}
	code, raw, _ := cfCacheCall(t, h, cfPurgePath, http.MethodPost, "", f.super)
	if code != http.StatusBadRequest {
		t.Fatalf("没有凭据时 purge 应 400，实得 %d %s", code, raw)
	}
	if !strings.Contains(raw, "Token") {
		t.Fatalf("400 文案必须点名缺什么（否则运维只能瞎猜）：%s", raw)
	}
}

// K：留痕只出状态，不许含凭据。
func TestCFCachePurgeLedgerCarriesNoSecret(t *testing.T) {
	f := cfFixture(t)
	api, _, _ := cfFakeCF(t, cfZoneHeader, `{"success":true}`)
	t.Setenv("CF_API_BASE_OVERRIDE", api.URL)
	t.Setenv("CLOUDFLARE_API_TOKEN", "")
	h := f.srv.cfMux()
	cfCacheCall(t, h, cfCachePath, http.MethodPost, `{"token":"`+cfPlainTok+`"}`, f.super)
	cfCacheCall(t, h, cfPurgePath, http.MethodPost, "", f.super)
	last, err := f.st.GetConfig("cf_purge_last")
	if err != nil || last == "" {
		t.Fatalf("purge 必须留痕（下一次开页要看得见），err=%v 值=%q", err, last)
	}
	if strings.Contains(last, cfPlainTok) || strings.Contains(last, "Bearer") {
		t.Fatalf("留痕里出现凭据材料：%s", last)
	}
	if !strings.Contains(last, `"ok":true`) || !strings.Contains(last, "lexicorn.cn") {
		t.Fatalf("留痕必须带结果与 zone：%s", last)
	}
}

// M：zone 名判据表驱动。
func TestCFZoneNameValidation(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"lexicorn.cn", true}, {"a.b.co", true}, {"例外的中文.cn", true},
		{"", false}, {"lexicorn", false}, {"https://lexicorn.cn", false},
		{"lexicorn.cn:8443", false}, {"lexicorn.cn/path", false}, {"a..b", false},
		{"user@lexicorn.cn", false}, {strings.Repeat("a", 300) + ".cn", false},
	} {
		if got := validCFZoneName(c.in); got != c.want {
			t.Fatalf("validCFZoneName(%q)=%v，期望 %v", c.in, got, c.want)
		}
	}
	// 脏值必须被写侧拒收（库里落脏值的表现是 purge 打了个查不到的地方，屏幕上只一次 failed）
	f := cfFixture(t)
	h := f.srv.cfMux()
	code, raw, _ := cfCacheCall(t, h, cfCachePath, http.MethodPost, `{"zone":"https://bad.example/x"}`, f.super)
	if code != http.StatusBadRequest {
		t.Fatalf("脏 zone 名应 400，实得 %d %s", code, raw)
	}
	if v, _ := f.st.GetConfig("cf_zone_name"); v != "" {
		t.Fatalf("被拒的脏值仍写进了库：%q", v)
	}
}

// N：导出失败不改判"保存失败"，但必须出声（库里是真值，文件只是投影）。
func TestCFCacheExportFailureIsLoudNotFatal(t *testing.T) {
	f := cfFixture(t)
	dir := t.TempDir()
	// 指向一个**不存在的子目录** ⇒ 按口径不许 mkdir，必须报错
	cfUseFile(t, f, filepath.Join(dir, "missing", "cf_token"))
	h := f.srv.cfMux()
	code, _, b := cfCacheCall(t, h, cfCachePath, http.MethodPost, `{"token":"`+cfPlainTok+`"}`, f.super)
	if code != http.StatusOK {
		t.Fatalf("写文件失败不该把保存判成失败（库里已落成），实得 %d", code)
	}
	if b["changed"] != true {
		t.Fatalf("库里应已写入，changed 却 false：%v", b)
	}
	if b["file_written"] != false {
		t.Fatalf("导出明明失败，file_written 却报 true（＝给失败背书）")
	}
	if !strings.Contains(cfTstr(b["file_note"]), "目录") {
		t.Fatalf("file_note 必须点明失败原因（导出目录不存在，不 mkdir），实得 %v", b["file_note"])
	}
	stored, _ := f.st.GetConfig("cf_api_token")
	if !strings.HasPrefix(stored, "enc:v1:") {
		t.Fatalf("密文没落库：%q", stored)
	}
}

// O：被拒的保存必须一个字节都不落地。
//
// ★ 这一条治的是"先写后验"的半套状态：旧形态先 SetConfig(cf_api_token) 再验 zone／导出路径，
// 一次 400 之后库里已经是**新凭据 + 旧 zone**——界面说"保存失败"，实际那台机器已经换了密钥。
func TestCFCacheRejectedSaveLeavesStoreUntouched(t *testing.T) {
	f := cfFixture(t)
	h := f.srv.cfMux()
	if _, _, b := cfCacheCall(t, h, cfCachePath, http.MethodPost, `{"token":"`+cfPlainTok+`"}`, f.super); b["changed"] != true {
		t.Fatalf("前置保存没落地：%v", b)
	}
	before, _ := f.st.GetConfig("cf_api_token")
	beforeZone, _ := f.st.GetConfig("cf_zone_name")

	// ① 新 token + 非法 zone
	code, raw, _ := cfCacheCall(t, h, cfCachePath, http.MethodPost,
		`{"token":"`+cfPlainTok2+`","zone":"https://bad.example/x"}`, f.super)
	if code != http.StatusBadRequest {
		t.Fatalf("脏 zone 应 400，实得 %d %s", code, raw)
	}
	if after, _ := f.st.GetConfig("cf_api_token"); after != before {
		t.Fatalf("被拒的保存把凭据改成了新值（半套状态：界面说失败，机器已换密钥）")
	}

	// ② 新 token + 相对导出路径（同样必须在写库之前被拦下）
	code2, raw2, _ := cfCacheCall(t, h, cfCachePath, http.MethodPost,
		`{"token":"`+cfPlainTok2+`","token_file":"relative/cf_token"}`, f.super)
	if code2 != http.StatusBadRequest {
		t.Fatalf("相对导出路径应 400，实得 %d %s", code2, raw2)
	}
	if after, _ := f.st.GetConfig("cf_api_token"); after != before {
		t.Fatalf("相对导出路径被拒后凭据仍被改写（校验顺序又有回退）")
	}

	// ③ 新 token 太短（同一批里最短的那条腿也要证明它排在写库之前）
	code3, raw3, _ := cfCacheCall(t, h, cfCachePath, http.MethodPost, `{"token":"short"}`, f.super)
	if code3 != http.StatusBadRequest {
		t.Fatalf("过短凭据应 400，实得 %d %s", code3, raw3)
	}
	if after, _ := f.st.GetConfig("cf_api_token"); after != before {
		t.Fatalf("过短凭据被拒后凭据仍被改写")
	}
	if afterZone, _ := f.st.GetConfig("cf_zone_name"); afterZone != beforeZone {
		t.Fatalf("被拒的保存改动了 zone（%q → %q）", beforeZone, afterZone)
	}
}

// P：换导出路径／填 none ⇒ 旧位置那份投影必须删掉。
//
// 为什么这不是洁癖：旧位置那份仍是**一把可用的密钥**，执行体下次读的是新配置的路径，
// 但任何读得到 /etc 的进程都能继续用旧那份去清缓存——界面上完全看不出异常。
func TestCFCachePathChangeRemovesOldProjection(t *testing.T) {
	f := cfFixture(t)
	dir := t.TempDir()
	oldP := filepath.Join(dir, "old_token")
	newP := filepath.Join(dir, "new_token")
	cfUseFile(t, f, oldP)
	h := f.srv.cfMux()

	if _, _, b := cfCacheCall(t, h, cfCachePath, http.MethodPost, `{"token":"`+cfPlainTok+`"}`, f.super); b["file_written"] != true {
		t.Fatalf("前置导出没写成：%v", b)
	}
	if _, err := os.Stat(oldP); err != nil {
		t.Fatalf("前置导出文件不在: %v", err)
	}

	// ① 换到新路径：旧文件必须消失、新文件带上当前生效值
	code, raw, b := cfCacheCall(t, h, cfCachePath, http.MethodPost, `{"token_file":"`+newP+`"}`, f.super)
	if code != http.StatusOK {
		t.Fatalf("换导出路径应 200，实得 %d %s", code, raw)
	}
	if b["file_written"] != true {
		t.Fatalf("换路径后新投影没写成：%v", b)
	}
	if _, err := os.Stat(oldP); !os.IsNotExist(err) {
		t.Fatalf("导出路径已换，旧位置那份投影还在（%v）⇒ 旧的可用密钥留在磁盘上", err)
	}
	data, err := os.ReadFile(newP)
	if err != nil || strings.TrimSpace(string(data)) != cfPlainTok {
		t.Fatalf("新位置内容不对（执行体拿这把去清缓存）：%q err=%v", string(data), err)
	}

	// ② 填 none 关掉导出：新位置那份也必须消失
	code3, raw3, b3 := cfCacheCall(t, h, cfCachePath, http.MethodPost, `{"token_file":"none"}`, f.super)
	if code3 != http.StatusOK {
		t.Fatalf("关闭导出应 200，实得 %d %s", code3, raw3)
	}
	if b3["file_written"] != false {
		t.Fatalf("导出已关，file_written 却报 true（＝给失败背书）")
	}
	if !strings.Contains(cfTstr(b3["file_note"]), "导出已关闭") {
		t.Fatalf("关闭导出必须回一句可读说明，实得 %v", b3["file_note"])
	}
	if _, err := os.Stat(newP); !os.IsNotExist(err) {
		t.Fatalf("填 none 后旧投影还在（%v）⇒ 执行体仍能读到一把可用密钥", err)
	}
}

// R：旧投影删不掉 ⇒ 出声但不判保存失败（新位置已写成，旧位置仍在，两条腿都要留读数）。
func TestCFCacheStaleProjectionRemovalFailureIsLoud(t *testing.T) {
	f := cfFixture(t)
	dir := t.TempDir()
	// 旧路径指向一个**非空目录**：os.Remove 必然失败（ENOTEMPTY），且与我们写的投影不是同一形态
	stale := filepath.Join(dir, "stale_dir")
	if err := os.Mkdir(stale, 0o700); err != nil {
		t.Fatalf("造旧路径失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stale, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatalf("造旧路径占位文件失败: %v", err)
	}
	cfUseFile(t, f, stale)
	newP := filepath.Join(dir, "new_token")
	h := f.srv.cfMux()

	code, raw, b := cfCacheCall(t, h, cfCachePath, http.MethodPost,
		`{"token":"`+cfPlainTok+`","token_file":"`+newP+`"}`, f.super)
	if code != http.StatusOK || b["changed"] != true {
		t.Fatalf("删旧投影失败不该把保存判成失败（库里已落成），实得 %d %s", code, raw)
	}
	if b["file_written"] != true {
		t.Fatalf("新位置应当写成，实得 %v（note=%v）", b["file_written"], b["file_note"])
	}
	if !strings.Contains(cfTstr(b["file_note"]), "旧导出文件删除失败") {
		t.Fatalf("旧投影删不掉必须写进 file_note（否则旧密钥留盘而屏幕全绿），实得 %v", b["file_note"])
	}
	if _, err := os.Stat(filepath.Join(stale, "keep")); err != nil {
		t.Fatalf("判据自伤：旧路径那份不该被删掉（它是个非空目录）：%v", err)
	}
	if _, err := os.Stat(newP); err != nil {
		t.Fatalf("新位置没写成: %v", err)
	}
}

// exportOrderProblems 判「先落库、后碰文件侧」这一条顺序（返回问题列表，空＝合规）。
//
// 只问一件事：函数体里**最后一个** SetConfig 必须排在 writeFileSecretLike 之前。
// 反向写法的坏法不是报错，而是"库里没落成、磁盘上已多一份投影"——屏幕上回的是失败，
// 执行体下一次却拿那份"没保存成功"的值去清缓存。
func exportOrderProblems(body string) []string {
	lastSet := strings.LastIndex(body, "s.Store.SetConfig(")
	fileWrite := strings.Index(body, "writeFileSecretLike(")
	if lastSet < 0 || fileWrite < 0 {
		return []string{"读不到 SetConfig 或 writeFileSecretLike（判据靶子搬家了，本条已失去射程）"}
	}
	if fileWrite < lastSet {
		return []string{"导出文件排在最后一个 SetConfig 之前 ⇒ 写库失败也会把值落到磁盘"}
	}
	return nil
}

// L：顺序锁——保存 handler 必须先落库、后写投影，同一判据再吃一枚变异与一次空输入。
func TestCFCacheWritesStoreBeforeFile(t *testing.T) {
	src := mustReadGoFileAt(t, "admin_cf_cache.go")
	i := strings.Index(src, "func (s *Server) handleAdminCFCacheSave")
	if i < 0 {
		t.Fatalf("读不到保存 handler（判据射程失效）")
	}
	end := strings.Index(src[i:], "\n// handleAdminCFCachePurge")
	if end < 0 {
		t.Fatalf("读不到保存 handler 的函数体边界（判据射程失效）")
	}
	body := src[i : i+end]
	if ps := exportOrderProblems(body); len(ps) > 0 {
		t.Fatalf("写侧顺序不合规：%s", strings.Join(ps, "；"))
	}
	mutated := "\t\tif err := writeFileSecretLike(p, eff); err != nil {\n" + body
	if ps := exportOrderProblems(mutated); len(ps) == 0 {
		t.Fatalf("反证不过：把写投影挪到最前面竟然判绿")
	}
	if ps := exportOrderProblems(""); len(ps) == 0 {
		t.Fatalf("判据自证不过：空函数体判绿")
	}
}

// 附加：三个键名必须只有一份字面量（跨包消费面靠这些字面量读，第二份迟早漂）。
func TestCFCacheKeyLiteralsAreSingleSourced(t *testing.T) {
	for _, key := range []string{"cf_api_token", "cf_zone_name", "cf_token_file", "cf_purge_last"} {
		n := strings.Count(mustReadGoFileAt(t, "admin_cf_cache.go"), `"`+key+`"`)
		if n == 0 {
			t.Fatalf("键 %s 在 admin_cf_cache.go 里读不到 ⇒ 常量表被改名，判据与实现分家", key)
		}
	}
	front := mustReadGoFileAt(t, "../../../frontend-react/src/api/ops.ts")
	for _, key := range []string{"token_file", "db_masked", "env_overridden", "purge_last"} {
		if !strings.Contains(front, key) {
			t.Fatalf("前端 ops.ts 缺字段 %s ⇒ 出栈契约两面不同名（前端只能自己猜第二份）", key)
		}
	}
}

// S：这个面上每一条**客户可见的中文**都必须翻得出英文（错误面 + 成功面的 `file_note`）。
//
// ★ 为什么这条必须单独存在：`i18n.Msg` 的兜底是「未命中就原样透传中文」——最坏的静默形态，
// 英文界面照常渲染、只是整句还是中文，而其它闸门全绿：
//   - `TestAPICnMessageLiteralsCovered` 只认 `apierrors.New(code, "字面量")` 那一形，
//     本文件的动态消息走 `fmt.Sprintf(...)`，它结构上扫不到；
//   - `file_note` 更不在 langWriter 的射程里（中间件只改写 JSON 的 message/error）。
//
// 判据两条：① 英文语境必须**翻出不同文本且不再含汉字**；② 中文语境必须原样透传
// （反向对照：把 Msg 改成无条件翻英会让 ② 红，防这条锁变成"只要不是原文就行"的空话）。
func TestCFCacheMessagesAreTranslatable(t *testing.T) {
	ctxEn := i18n.WithLang(context.Background(), "en")
	msgs := []string{
		"API Token 太短，请粘贴 Cloudflare 生成的完整串",
		"zone 名不合法（只允许域名形态，如 lexicorn.cn）",
		"导出路径必须是绝对路径",
		"还没有可用的 Cloudflare API Token（库内未配置，或密文解不开）",
		"仅支持 POST",
		fmt.Sprintf("取不到 zone id（HTTP %s）：请核对 zone 名与令牌的 Zone 读取权限", "403"),
		fmt.Sprintf("CF 未受理清理请求（HTTP %s）：请核对令牌的 Cache Purge: Edit 权限", "403"),
		fmt.Sprintf("导出已关闭（token_file=%s ⇒ 换源执行体只能靠 env 才真清）", cfTokenFileDisabled),
		"生效值是环境变量占的，未导出（撤掉 env 后在本页再保存一次即可导出库内值）",
		fmt.Sprintf("导出文件删除失败：%s", "permission denied"),
		fmt.Sprintf("旧导出文件删除失败：%s", "permission denied"),
		fmt.Sprintf("导出目录不存在：%s", "/nope"),
		fmt.Sprintf("导出目录不是目录：%s", "/nope"),
		fmt.Sprintf("临时名创建失败：%s", "no space left"),
		fmt.Sprintf("0600 设不上（文件系统不支持）：%s", "EPERM"),
		fmt.Sprintf("写入失败：%s", "no space left"),
		fmt.Sprintf("落盘失败：%s", "EIO"),
		fmt.Sprintf("关闭失败：%s", "EIO"),
		fmt.Sprintf("改名失败：%s", "EACCES"),
	}
	for _, zh := range msgs {
		en := i18n.Msg(ctxEn, zh)
		if en == zh {
			t.Errorf("英文语境未翻出：%q（补录 scripts/gen_i18n_catalog.py 后重新 --apply）", zh)
			continue
		}
		if containsHan(en) {
			t.Errorf("英文译文里仍含汉字：%q → %q", zh, en)
		}
		// 动态占位必须**真回填**（模板在、捕获没接上时会留下没被替换的整句或漏掉运行时值）
		if strings.Contains(zh, "HTTP 403") && !strings.Contains(en, "403") {
			t.Errorf("HTTP 码没进英文译文：%q → %q", zh, en)
		}
		if strings.Contains(zh, "/nope") && !strings.Contains(en, "/nope") {
			t.Errorf("运行时路径没进英文译文：%q → %q", zh, en)
		}
	}
	// ② 反向对照：中文语境原样透传
	ctxZh := i18n.WithLang(context.Background(), "zh")
	for _, zh := range msgs {
		if got := i18n.Msg(ctxZh, zh); got != zh {
			t.Fatalf("中文语境被改写了：%q → %q（Msg 只该在 en 语境翻译）", zh, got)
		}
	}
}

// cfTauditCount 数某个审计动作的留痕条数。
func cfTauditCount(t *testing.T, f *estCfgFixture, action string) int {
	t.Helper()
	var n int
	if err := f.st.DB().QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action=?`, action).Scan(&n); err != nil {
		t.Fatalf("读审计失败: %v", err)
	}
	return n
}

// cfTstr 把 interface{} 安全转成字符串（断言文案用，不参与判定）。
func cfTstr(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
