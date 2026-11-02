// ============ admin_cf_cache.go · 职责说明 ============
// Cloudflare 边缘缓存凭据与清理动作的**管理后台托管面**（★ 2026-11-02 用户令
// 「把 cf 的 token 配置也放在管理后台，平台管理员配置并可热更新」）。
//
// 背景：换源执行体 `deploy/swap_web_and_binary.sh` 的 purge 腿此前只认 env
// `CLOUDFLARE_API_TOKEN`，于是"每次发版都真清"寄托在**跑发版的那个人记得带 env** 上——
// 不带就静默 `CF_PURGE=skipped`，代价是边缘残留旧 HTML ≤2h（白屏窗口）。
// 本文件把凭据的**唯一管理面**搬进后台，并让执行体能读到同一份事实：
//
//	库内密文（system_config.cf_api_token，enc:v1:，复用 store.EncryptSecret）
//	        │ 保存成功后原子导出
//	        ▼
//	部署机上的 0600 文件（默认 /opt/translator/data/cf_token，路径本身也可配）
//	        │ 执行体的凭据解析口按 env > 文件 现读
//	        ▼
//	换源前/后的两次 purge_everything
//
// ★ 默认路径为什么落在 `data/` 而不是 `/etc/translator/`（★ 2026-11-02 现网实测改档）：
//
//	两个 unit 都以 `translator:translator` 跑且带 `ProtectSystem=full`，而 `/etc/translator`
//	是 `root:root 0750` ⇒ 服务既穿不过目录、也写不进去，导出那一跳**必然失败**，而屏幕上是
//	一次"看起来正常"的保存（库内密文对、徽标在场，只有 file_note 里那句写失败、执行体永远 skipped）。
//	`/opt/translator/data` 是 `translator:translator 0755` 且本来就在 ReadWritePaths 里，服务写得进去。
//
// ★ 为什么是"导出成文件"而不是"执行体调本机回环接口"：后者要新增一条**能把明文密钥吐出来**
//
//	的 HTTP 接口，那是一条全新的攻击面（哪怕限定 127.0.0.1 也要另配回环判据、审计、防日志泄漏）；
//	文件是执行体本来就要读的形态，权限模型由文件系统给（root 独占 0600），零新接口。
//
// ★ 为什么库里仍要存一份密文：热更新的"真值"必须在库里——界面能改、能读掩码、能在服务端
//
//	直接清一次缓存；文件只是**交付给 bash 执行体的那一份投影**。
//	两者不一致时必须能从读数看出来（所以保存响应带 file_written / file_note，
//	写文件失败＝"库里已换、执行体还没跟上"，下一次发版仍会 skipped——这正是本条要避免的静默形态）。
//
// 四条与全仓一致的硬口径：
//  1. **出栈永不明文**：读取只回 set + 掩码（maskKey）+ 生效来源，与 assist token /
//     llm_api_key 完全同形（〇-LK 的教训：明文进响应体＝进浏览器、进代理日志、进 Playwright trace）。
//  2. **留空=不修改、掩码=未改动**（IsSecretMasked 命中即跳过写库，绝不把掩码写回库），
//     清空必须显式 `clear=true`，避免把空串误当清除。
//  3. **先落库、落成了才碰进程侧/文件侧**（AGENTS §一·3 写侧纪律）：写库失败即返回错误，
//     文件一个字节都不动；反过来（先写文件再写库）会把"保存失败"写成"这台已经生效了"。
//  4. **现读现解密、不缓存**：purge 每次都从库里取当前生效值，管理员改完即用，不必重启、不必换件
//     （"热更新"的落点就在这一条；与 superAdminRecipients 的"现读现算"同一取向）。
//
// 出站 UA 走 `crawler.ContactUserAgent`（AGENTS §23：每条出站腿都必须带可联系的产品 UA，
// 且联系方式**不许挂在可选配置上**——403/429 时屏幕上零症状，最难查）。
//
// 鉴权：三个端点一律 `requireAdminUser`（超管，tenant_id=0 且 level≥4）；
// 未登录 401／等级不足 403 走 `writeAuthzError`（F-64③ 口径）。读、存、清三个动作都记审计。
// =============================================
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"translator/internal/crawler"
	apierrors "translator/internal/errors"
	"translator/internal/i18n"
	"translator/internal/observability"
	"translator/internal/store"
)

// system_config 键名（唯一事实源；前端与执行体都按这些字面量读，改名要三处同步）。
const (
	cfAPITokenKey  = "cf_api_token"
	cfZoneKey      = "cf_zone_name"
	cfTokenFileKey = "cf_token_file"
	cfPurgeLastKey = "cf_purge_last"
)

// 出厂档：zone 与导出文件路径都留默认值，管理员只填密钥就能用。
const (
	cfZoneDefault = "lexicorn.cn"
	// cfTokenFileDefault 投影文件的出厂路径。★ 必须在**服务账号写得进去**的目录下：
	// 发版执行体是 root 跑，但写这个文件的是服务进程（unit 以 translator 跑，ProtectSystem=full），
	// 而 `/etc/translator` 是 root:root 0750 ⇒ 放那里＝导出恒失败、执行体恒 skipped（现网实测）。
	// 这个字面量另有跨侧等值锁（api 侧 gate 与 deploy/swap_web_and_binary.sh 的默认档逐字相等）。
	cfTokenFileDefault = "/opt/translator/data/cf_token"
	// cfTokenFileDisabled 关掉导出的显式哨兵值（填进面板的 token_file 框即可）。
	cfTokenFileDisabled = "none"
)

// cfAPIBase CF API 根址；留一个覆盖口是给夹具的（判据要吃真形状，不许为了测试改生产默认）。
const cfAPIBase = "https://api.cloudflare.com/client/v4"

// cfAPIBaseOverride 由测试夹具设置（环境变量），生产不配。
func cfAPIBaseFromEnv() string {
	if v := strings.TrimSpace(os.Getenv("CF_API_BASE_OVERRIDE")); v != "" {
		return v
	}
	return cfAPIBase
}

// cfCacheState 出栈形态（**永不含明文**）。
type cfCacheState struct {
	Source        string `json:"source"`         // env / db / none——生效来源，与 assist 同口径
	Set           bool   `json:"set"`            // 是否有生效值
	Masked        string `json:"masked"`         // 生效值的掩码
	DBMasked      string `json:"db_masked"`      // 库内密文解出来的掩码（env 覆盖时仍看得见库里是哪个）
	EnvOverridden bool   `json:"env_overridden"` // env 占着生效位＝库内值要等 env 撤掉才生效
	Zone          string `json:"zone"`           // 要清的 zone 名（现查 zone id，不写死 id）
	TokenFile     string `json:"token_file"`     // 导出给执行体的文件路径（空＝不导出）
	FilePresent   bool   `json:"file_present"`   // 那个文件此刻在不在磁盘上
	PurgeLast     string `json:"purge_last"`     // 上一次 purge 的读数留痕（JSON 文本，原样回显）
}

// effectiveCFToken 生效凭据与来源：env > 库内密文（与执行体的 env > 文件同序，两把尺子必须一把）。
//
// ⚠️ 密文存在但解不开（典型：JWT_SECRET 轮换后没重存）⇒ 明确回 none，**不许**静默给空串当"已配置"。
func (s *Server) effectiveCFToken() (string, string) {
	if v := strings.TrimSpace(os.Getenv("CLOUDFLARE_API_TOKEN")); v != "" {
		return v, "env"
	}
	raw, err := s.Store.GetConfig(cfAPITokenKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return "", "none"
	}
	tok := store.DecryptSecret(strings.TrimSpace(raw))
	if tok == "" {
		return "", "none"
	}
	return tok, "db"
}

// cfConfigValueOr 读一个非密钥配置项，取不到就回默认值（"读不到"与"配了空串"在这一档等价，都按默认走）。
func (s *Server) cfConfigValueOr(key, def string) string {
	if s.Store == nil {
		return def
	}
	v, err := s.Store.GetConfig(key)
	if err != nil {
		return def
	}
	if v = strings.TrimSpace(v); v == "" {
		return def
	}
	return v
}

// cfTokenFilePath 导出路径现值。
//
// ⚠️ 库里**不做"键不存在 vs 空串"的区分**（那需要第二个读接口，而这两个形态在本档语义相同：
// 都没表态）。要显式关掉导出请填哨兵 `none`——"关掉"必须是一个被写下来的决定，
// 而不是"没读到"被读成"关掉了"（AGENTS §一·3⑤ 那条同族：没读到与没配必须不同形，
// 这里则是让"没配"落到一个确定的默认上，把关闭动作做成显式值）。
func (s *Server) cfTokenFilePath() string {
	v := strings.TrimSpace(s.cfConfigValueOr(cfTokenFileKey, cfTokenFileDefault))
	if v == cfTokenFileDisabled {
		return ""
	}
	return v
}

// cfStateOf 组装出栈形态。参数 tok/src＝生效凭据与来源。
func (s *Server) cfStateOf(tok, src string) cfCacheState {
	_, dbMask := s.llmKeyState(cfAPITokenKey)
	path := s.cfTokenFilePath()
	last, _ := s.Store.GetConfig(cfPurgeLastKey)
	return cfCacheState{
		Source:        src,
		Set:           tok != "",
		Masked:        maskKey(tok),
		DBMasked:      dbMask,
		EnvOverridden: strings.TrimSpace(os.Getenv("CLOUDFLARE_API_TOKEN")) != "",
		Zone:          s.cfConfigValueOr(cfZoneKey, cfZoneDefault),
		TokenFile:     path,
		FilePresent:   path != "" && fileExists(path),
		PurgeLast:     strings.TrimSpace(last),
	}
}

// fileExists 只看"在不在"，不跟随符号链接的目标属性（导出路径若被指到别处，present 仍算 true）。
func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// cfNoteText 把一句「给客户看的说明」按当前界面语种翻成英文（中文语境原样返回）。
//
// ★ 为什么必须在这里自己翻、不能指望 langWriter 中间件：那个中间件只改写 JSON 的
// `message` / `error` 两个字段（服务 4xx/5xx 的错误面），而 `file_note` 挂在**成功响应**上。
// 不翻的后果不是报错，而是"英文界面配好了凭据、弹出一句中文"——只有真去英文界面点一次才看得见。
func cfNoteText(ctx context.Context, s string) string {
	if s == "" {
		return ""
	}
	return i18n.Msg(ctx, s)
}

// cfNotesText 把多条说明拼成一句（分隔符也随语种）。
//
// 必须**逐条翻再拼**：整串拼好再翻会因为中间夹着运行时 err 文本而整条匹配不上词条，
// 结果是一条都翻不出来（而屏幕上看不出"翻译漏了"，只看到中文）。
func cfNotesText(ctx context.Context, notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	out := make([]string, 0, len(notes))
	for _, n := range notes {
		out = append(out, i18n.Msg(ctx, n))
	}
	sep := "；"
	if i18n.LangFrom(ctx) == "en" {
		sep = "; "
	}
	return strings.Join(out, sep)
}

// handleAdminCFCache 是 /api/admin/ops/cf-cache 的方法分派口（GET 读态／POST 保存）。
//
// 注册只挂这一个入口的理由：路由表里同一前缀挂两个 handler＝鉴权与审计迟早分叉；
// 方法不支持一律走 writeError＋ErrMethodNotAllowed（AGENTS §一·8 的错误返回口径）。
func (s *Server) handleAdminCFCache(w http.ResponseWriter, r *http.Request) {
	// ★ 鉴权必须在方法闸之前：匿名打错方法若先撞 405，就等于对"这条路由认不认身份"给了第二个答案
	//   （全路由 401/403 一致性锁按 132 条探针逐条比，这一族是唯一能抓住它的地方）。
	if _, err := s.requireAdminUser(r); err != nil {
		s.writeAuthzError(w, r, err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.handleAdminCFCacheGet(w, r)
	case http.MethodPost:
		s.handleAdminCFCacheSave(w, r)
	default:
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
	}
}

// handleAdminCFCacheGet GET /api/admin/ops/cf-cache —— 读状态（只回掩码）。
//
// ★ 审计：读取虽只回掩码仍留痕（谁在何时看过平台级凭据的状态），与 assist token 同口径。
func (s *Server) handleAdminCFCacheGet(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		s.writeAuthzError(w, r, err)
		return
	}
	tok, src := s.effectiveCFToken()
	st := s.cfStateOf(tok, src)
	s.Store.LogAudit(0, u.ID, "cf_cache_read", "system_config", cfAPITokenKey)
	writeJSON(w, 200, map[string]interface{}{"success": true,
		"source": st.Source, "set": st.Set, "masked": st.Masked, "db_masked": st.DBMasked,
		"env_overridden": st.EnvOverridden, "zone": st.Zone, "token_file": st.TokenFile,
		"file_present": st.FilePresent, "purge_last": st.PurgeLast})
}

// handleAdminCFCacheSave POST /api/admin/ops/cf-cache —— 保存凭据／zone／导出路径。
//
// 三条语义（一个都不许省）：
//   - `clear=true` ⇒ 显式清空密钥（并尽力删掉导出文件，让执行体从 ok 翻回 skipped 是**可见的**）；
//   - 留空／掩码 ⇒ 未改动：既不写库也不动文件（把掩码写回库＝凭据被静默改坏，历史踩坑）；
//   - 先落库、后写文件：写库失败即 500 且文件一字未动；写文件失败**不改判保存失败**（库里是真值），
//     但必须回 `file_written=false` ＋ 一句 `file_note`，界面据此显示
//     「库内已保存，执行体那一份还没跟上 ⇒ 下次发版仍不会真清」。
//
// ★ 2026-11-02 复核补两条（都是"半套状态"那一族）：
//   - **先把三道校验做完再写库**：被拒的保存必须一个字节都没落地（旧形态先写 token 再验 zone，
//     一次 400 会留下"新凭据 + 旧 zone"的半套配置）；
//   - **换路径／填 none 必须先撤旧投影**：旧位置那份仍是可用密钥，留着它等于换个地方继续清缓存，
//     而界面上看不出任何异常。删失败要进 `file_note` 并记 WARN（不判保存失败）。
func (s *Server) handleAdminCFCacheSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		Token     string `json:"token"`
		Zone      string `json:"zone"`
		TokenFile string `json:"token_file"`
		Clear     bool   `json:"clear"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求体格式错误"))
		return
	}
	tok := strings.TrimSpace(req.Token)
	zone := strings.TrimSpace(req.Zone)
	tf := strings.TrimSpace(req.TokenFile)

	if req.Clear {
		if err := s.Store.SetConfig(cfAPITokenKey, ""); err != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
			return
		}
		// 清掉导出文件：留着它，执行体会继续拿**上一把**密钥清缓存——那比"没清"更坏
		// （屏幕上是一次成功，实际清的是旧凭据能清的东西）。删除失败必须出声。
		note := ""
		if p := s.cfTokenFilePath(); p != "" {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				note = fmt.Sprintf("导出文件删除失败：%s", err.Error())
				observability.Warn(r.Context(), "CF 凭据已清空但导出文件没删掉（执行体仍会读到旧密钥）",
					"path", p, "error", err.Error())
			}
		}
		s.Store.LogAudit(0, u.ID, "cf_cache_clear", "system_config", cfAPITokenKey)
		eff, src := s.effectiveCFToken()
		st := s.cfStateOf(eff, src)
		writeJSON(w, 200, map[string]interface{}{"success": true, "changed": true,
			"file_written": note == "", "file_note": cfNoteText(r.Context(), note),
			"source": st.Source, "set": st.Set, "masked": st.Masked, "db_masked": st.DBMasked,
			"env_overridden": st.EnvOverridden, "zone": st.Zone, "token_file": st.TokenFile,
			"file_present": st.FilePresent})
		return
	}

	changed := tok != "" && !store.IsSecretMasked(tok)

	// ★ 三道校验必须全部排在**第一个 SetConfig 之前**：token／zone／导出路径是同一次保存的三个面，
	//   交错"先写后验"会让一次被拒的保存留下半套状态（典型：token 已换新、zone 仍是旧的），
	//   而界面回的是 400 ⇒ 下一位排查时按"这次没保存"去找，永远找不到那半套已生效的值。
	if changed && len(tok) < 8 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "API Token 太短，请粘贴 Cloudflare 生成的完整串"))
		return
	}
	if zone != "" && !validCFZoneName(zone) {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "zone 名不合法（只允许域名形态，如 lexicorn.cn）"))
		return
	}
	// tf 允许填哨兵 none 关掉导出；其余一律要求绝对路径（相对路径＝"下一次发版清到哪台机器"没人知道）
	if tf != "" && tf != cfTokenFileDisabled && !filepath.IsAbs(tf) {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "导出路径必须是绝对路径"))
		return
	}

	// 旧投影路径要在落库**之前**记下来：改成 none 或换到另一个路径之后，旧位置那份仍是一把
	// 可用的密钥——留着它，执行体（或任何读得到 /etc 的进程）会继续拿上一把去清缓存，
	// 比"没配"更坏（屏幕上是一次成功，实际清的是旧凭据能清的东西）。
	oldPath := s.cfTokenFilePath()

	if changed {
		if err := s.Store.SetConfig(cfAPITokenKey, store.EncryptSecret(tok)); err != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
			return
		}
		s.Store.LogAudit(0, u.ID, "cf_cache_save", "system_config", cfAPITokenKey)
	}
	if zone != "" {
		if err := s.Store.SetConfig(cfZoneKey, zone); err != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
			return
		}
	}
	// 导出路径本身不是密钥，但要走同一道"先落库"的门：库里没落成就不碰磁盘。
	pathChanged := false
	if tf != "" && tf != oldPath {
		if err := s.Store.SetConfig(cfTokenFileKey, tf); err != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
			return
		}
		pathChanged = true
	}

	// ★ 落库全部成功之后才碰磁盘；且**先撤旧位置、再写新位置**（顺序反了会有一瞬两份可用密钥）。
	// 旧文件删不掉不判保存失败（库里是真值），但必须出声——它是一把仍在生效的凭据。
	var notes []string
	if pathChanged && oldPath != "" {
		if err := os.Remove(oldPath); err != nil && !os.IsNotExist(err) {
			notes = append(notes, fmt.Sprintf("旧导出文件删除失败：%s", err.Error()))
			observability.Warn(r.Context(), "CF 导出路径已改，但旧位置的投影文件没删掉（旧密钥仍可被读到）",
				"path", oldPath, "error", err.Error())
		}
	}

	// 把**当前生效值**导出给执行体。
	// env 占着生效位时刻意**不导出 env 那份**：env 是部署侧显式配置，把它落到文件里
	// 等于让一次界面保存改写了运维的 env 语义（而且 env 撤掉后文件还在冒充"库里的值"）。
	fileWritten := false
	eff, src := s.effectiveCFToken()
	if p := s.cfTokenFilePath(); p != "" && eff != "" && src == "db" {
		if err := writeFileSecretLike(p, eff); err != nil {
			notes = append(notes, err.Error())
			observability.Warn(r.Context(), "CF 凭据已存库，但导出给换源执行体的文件没写成",
				"path", p, "error", err.Error())
		} else {
			fileWritten = true
		}
	} else if p == "" {
		notes = append(notes, fmt.Sprintf("导出已关闭（token_file=%s ⇒ 换源执行体只能靠 env 才真清）", cfTokenFileDisabled))
	} else if src == "env" {
		notes = append(notes, "生效值是环境变量占的，未导出（撤掉 env 后在本页再保存一次即可导出库内值）")
	}
	fileNote := cfNotesText(r.Context(), notes)

	st := s.cfStateOf(eff, src)
	writeJSON(w, 200, map[string]interface{}{"success": true, "changed": changed,
		"file_written": fileWritten, "file_note": fileNote,
		"source": st.Source, "set": st.Set, "masked": st.Masked, "db_masked": st.DBMasked,
		"env_overridden": st.EnvOverridden, "zone": st.Zone, "token_file": st.TokenFile,
		"file_present": st.FilePresent})
}

// writeFileSecretLike 原子写一把密钥：临时名 + rename + 0600 + 目录必须已存在。
//
// 三条口径：
//   - 模式 **0600 是判据不是礼貌**：换源执行体按"组/其他有可读位即拒读"（644 的凭据＝挂在
//     磁盘上给所有进程看，读成功才是缺陷），所以这里写不出 0600 就**报错**，不许降级成 0644 交付；
//   - 目录不存在 ⇒ 明确报错，**不 mkdir**：凭空创建部署根下的 data 目录会把"这台机器怎么长这样"
//     变成一次界面保存的副作用；
//   - 同目录临时名 + rename ⇒ 执行体永远不会读到半截内容。
func writeFileSecretLike(path, content string) error {
	dir := filepath.Dir(path)
	st, err := os.Stat(dir)
	if err != nil {
		return &cfFileError{msg: fmt.Sprintf("导出目录不存在：%s", dir)}
	}
	if !st.IsDir() {
		return &cfFileError{msg: fmt.Sprintf("导出目录不是目录：%s", dir)}
	}
	tmp, err := os.CreateTemp(dir, ".cf_token.tmp*")
	if err != nil {
		return &cfFileError{msg: fmt.Sprintf("临时名创建失败：%s", err.Error())}
	}
	name := tmp.Name()
	defer os.Remove(name) // rename 成功后这行是空操作；失败时不留半截文件
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return &cfFileError{msg: fmt.Sprintf("0600 设不上（文件系统不支持）：%s", err.Error())}
	}
	if _, err := tmp.WriteString(content + "\n"); err != nil {
		tmp.Close()
		return &cfFileError{msg: fmt.Sprintf("写入失败：%s", err.Error())}
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return &cfFileError{msg: fmt.Sprintf("落盘失败：%s", err.Error())}
	}
	if err := tmp.Close(); err != nil {
		return &cfFileError{msg: fmt.Sprintf("关闭失败：%s", err.Error())}
	}
	if err := os.Rename(name, path); err != nil {
		return &cfFileError{msg: fmt.Sprintf("改名失败：%s", err.Error())}
	}
	return nil
}

// cfFileError 导出文件的失败类型（一句人可读的原因，进 file_note 与 WARN，不进日志之外的面）。
type cfFileError struct{ msg string }

// Error 实现 error 接口：文案面向运维（进 file_note 与 WARN），不含凭据。
func (e *cfFileError) Error() string { return e.msg }

// validCFZoneName zone 名的窄判据：必须像域名，且**不许**带 scheme／路径／端口。
// 脏值落库的后果不是报错，而是 purge 打到一个查不到 zone 的地方（屏幕上一次 failed 而已）。
func validCFZoneName(z string) bool {
	if z == "" || len(z) > 253 {
		return false
	}
	if strings.ContainsAny(z, "/ \\ @ : \t\n") {
		return false
	}
	dots := strings.Count(z, ".")
	if dots < 1 {
		return false
	}
	for _, label := range strings.Split(z, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, ch := range label {
			ok := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch >= 0x80
			if !ok {
				return false
			}
		}
	}
	return true
}

// handleAdminCFCachePurge POST /api/admin/ops/cf-cache/purge —— 服务端直接清一次边缘缓存。
//
// 这一条是"热更新"的另一半：管理员在后台配好密钥之后，**不必发版**就能把边缘清一次
// （改完 Caddy／Cache Rule、或换源后没带 token 那种现场，都能当场消掉 ≤2h 的旧 HTML 窗口）。
// 三条口径：
//   - zone id **现查**（写死 id＝换 zone 后静默打错地方）；
//   - 失败只回状态与 HTTP 码，**绝不把响应体原样回显**（CF 的错误信息里可能带 account/zone 标识）；
//   - 结果写进 cf_purge_last 留痕（谁清的、成没成、什么时候），下一次开页就能看见。
func (s *Server) handleAdminCFCachePurge(w http.ResponseWriter, r *http.Request) {
	// 鉴权先、方法闸后（同上：匿名永远先拿到 401 这个诚实答案）。
	u, err := s.requireAdminUser(r)
	if err != nil {
		s.writeAuthzError(w, r, err)
		return
	}
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 POST"))
		return
	}
	tok, src := s.effectiveCFToken()
	if tok == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation,
			"还没有可用的 Cloudflare API Token（库内未配置，或密文解不开）"))
		return
	}
	zoneName := s.cfConfigValueOr(cfZoneKey, cfZoneDefault)
	zoneID, code, err := cfZoneID(r.Context(), tok, zoneName)
	last := map[string]interface{}{
		"at": time.Now().UTC().Format(time.RFC3339), "zone": zoneName,
		"source": src, "http_code": code,
	}
	if err != nil {
		last["ok"] = false
		last["stage"] = "zone_lookup"
		s.recordCfPurgeLast(r.Context(), last)
		s.Store.LogAudit(0, u.ID, "cf_cache_purge", "system_config", cfZoneKey)
		s.writeError(w, r, apierrors.New(apierrors.ErrUpstreamUnavailable,
			fmt.Sprintf("取不到 zone id（HTTP %s）：请核对 zone 名与令牌的 Zone 读取权限", code)))
		return
	}
	okNow, code2 := cfPurgeEverything(r.Context(), tok, zoneID)
	last["ok"] = okNow
	last["stage"] = "purge"
	last["zone_id_len"] = len(zoneID)
	s.recordCfPurgeLast(r.Context(), last)
	s.Store.LogAudit(0, u.ID, "cf_cache_purge", "system_config", cfZoneKey)
	if !okNow {
		observability.Warn(r.Context(), "CF 边缘缓存清理未成功", "http_code", code2, "zone", zoneName)
		s.writeError(w, r, apierrors.New(apierrors.ErrUpstreamUnavailable,
			fmt.Sprintf("CF 未受理清理请求（HTTP %s）：请核对令牌的 Cache Purge: Edit 权限", code2)))
		return
	}
	observability.Info(r.Context(), "CF 边缘缓存已清理（后台手动）", "zone", zoneName, "by_uid", u.ID)
	writeJSON(w, 200, map[string]interface{}{"success": true, "ok": true,
		"zone": zoneName, "http_code": code2, "source": src})
}

// recordCfPurgeLast 把一次 purge 的读数留痕进 system_config（失败只出声，不影响本次请求判定）。
// ⚠️ 留痕里**只有状态与 HTTP 码**，绝不写 zone id 之外的标识，更绝不写凭据。
func (s *Server) recordCfPurgeLast(ctx context.Context, m map[string]interface{}) {
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	if err := s.Store.SetConfig(cfPurgeLastKey, string(b)); err != nil {
		observability.Warn(ctx, "CF purge 留痕写库失败", "error", err.Error())
	}
}

// cfZoneID 现查 zone id（GET /zones?name=）。返回值只可能是 32 位 hex 或空。
func cfZoneID(ctx context.Context, tok, zoneName string) (string, string, error) {
	url := cfAPIBaseFromEnv() + "/zones?name=" + zoneName
	body, code, err := cfCall(ctx, "GET", url, tok, "")
	if err != nil {
		return "", code, err
	}
	id := extractFirstZoneID(body)
	if id == "" {
		return "", code, &cfFileError{msg: "响应里没有 zone id"}
	}
	return id, code, nil
}

// cfPurgeEverything 打 purge_cache（purge_everything:true）。回 (是否受理, HTTP 码)。
func cfPurgeEverything(ctx context.Context, tok, zoneID string) (bool, string) {
	url := cfAPIBaseFromEnv() + "/zones/" + zoneID + "/purge_cache"
	body, code, err := cfCall(ctx, "POST", url, tok, `{"purge_everything":true}`)
	if err != nil {
		return false, code
	}
	return strings.Contains(body, `"success":true`), code
}

// cfCall 一次 CF API 调用：统一带 Authorization／UA／超时，并把响应体限长。
//
// ★ UA 走 `crawler.ContactUserAgent`——全仓联系方式只有一个事实源（AGENTS §23），
//
//	在这儿再拼一遍邮件地址就是第二把尺子；对端靠 UA 认客户端，UA 缺联系方式时
//	症状是 403/429 加一句零信息量的失败，而不是"没清缓存"。
func cfCall(ctx context.Context, method, url, tok, payload string) (string, string, error) {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var rd io.Reader
	if payload != "" {
		rd = strings.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(cctx, method, url, rd)
	if err != nil {
		return "", "000", err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", crawler.ContactUserAgent("LangCrossOpsCFPurge"))
	if payload != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := cfHTTPClient().Do(req)
	if err != nil {
		return "", "000", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return string(b), strconv.Itoa(resp.StatusCode), nil
}

// extractFirstZoneID 从 CF 的 zones 列表响应里取第一个 result.id（32 位 hex）。
// 手写小解析器：这份响应用来判"有没有这个 zone"，字段少且稳定，引第三方 JSON 树反而多一个失败面。
func extractFirstZoneID(body string) string {
	i := strings.Index(body, `"result":[`)
	if i < 0 {
		return ""
	}
	rest := body[i:]
	for len(rest) > 0 {
		k := strings.Index(rest, `"id":"`)
		if k < 0 {
			return ""
		}
		rest = rest[k+len(`"id":"`):]
		end := strings.Index(rest, `"`)
		if end < 0 || end > 64 {
			return ""
		}
		id := rest[:end]
		if len(id) == 32 && isHex(id) {
			return id
		}
		// 不是 hex 的 id 直接失败（宁可不判，也不拿一个陌生字段当 zone id 去打 purge）
		return ""
	}
	return ""
}

// isHex 判 32 位十六进制（CF 的 zone id 形状）；不合规就当"没拿到 id"，不拿陌生字段去打 purge。
func isHex(s string) bool {
	for _, ch := range s {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
			return false
		}
	}
	return true
}

// cfHTTP 出站客户端单例：25s 上限比请求内的 20s 上下文预算再宽一档，保证"谁先取消"永远是上下文那一侧。
var cfHTTP = &http.Client{Timeout: 25 * time.Second}

// cfHTTPClient 取那一个客户端（收在一个函数里，夹具要换 Transport 时只有一个落点）。
func cfHTTPClient() *http.Client { return cfHTTP }
