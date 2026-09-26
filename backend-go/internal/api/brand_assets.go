// ============================================================================
// internal/api/brand_assets.go — 品牌图（Logo / 首页背景）落静态件 + 只把 URL 注进 HTML
// （★ F-46，2026-09-26 批 I-9）
//
// 缺陷因果链（为什么这不是"性能优化"而是功能可用性缺陷）：
//
//	① 品牌图历史上以 **base64 dataURI 整串**存进 tenants.brand_logo / brand_home_bg
//	   与 system_config 的平台品牌；
//	② spa.go 的 serveIndexHTML 每次首页请求都把整包品牌 JSON 注进 </head> 前
//	   （injectBrandingScript → window.__BRANDING__）⇒ **HTML 体积 = 图片体积**，
//	   演示站实测首屏 2,080,056 B（其中 brand_home_bg 一项 2,065,807 字符）；
//	③ 更要紧的是：E15 设的写入上限（brandHomeBgMax=1200KB base64）只管新写，
//	   存量那行 2 MB 的值**永远降不下来**——租户在后台一改品牌就被 400「过大」拦住，
//	   等于该租户的品牌设置功能整体不可用（F-46 由"慢"升级为"坏"）。
//	④ 止血第 1 层（演示站背景图清空）已按用户指令执行并实测：公开页 2,080,056 B → 13,832 B；
//	   本文件是第 2 层机制修，保证**任何**租户再传大图也不会把首屏拖回去。
//
// 改法口径（与修复文档 §九 一致，两处收敛）：
//
//	· **读侧收敛到一个咽喉**：品牌字段只在 brandingPayload() 出栈前过 brandImageURL()，
//	  所以 /api/tenant/branding 与 SPA 注入两条面天然同源（不出现"接口给 URL、HTML 给 dataURI"）；
//	  存量库里仍是 dataURI 的行，**首次渲染时惰性落件并返回 URL**——不改数据也立刻止血，
//	  这正是"修读侧"的那一半（本轮 UAT 的共性教训：只修写侧等于没修）。
//	· **写侧不再存 dataURI**：保存时先转成 /brand/<名> 再入库，列里只留 URL。
//	· **兜底定义清楚**：任何解析不出可信图形的值（畸形 dataURI、非白名单格式、超硬上限）
//	  一律回落空串 = 前端用默认背景，**绝不**把半截字节或 HTML 当图片发出去；
//	  /brand/<名> 文件缺失回 404 JSON，**绝不**漏到 spa.go 的 "/" 兜底变成 200 整页 HTML
//	  （AGENTS §一·6「托管物只判 200 是无效断言」——这里从服务端一侧把那条陷阱堵死）。
//	· SVG 明确不进白名单：SVG 是**可带脚本的文档**，同源直出等于给租户一个 XSS 载荷位；
//	  要放就得配独立隔离域 + CSP，收益不抵风险，故品牌图只收 raster 四类。
//
// 闸门：internal/api/brand_assets_test.go（八组锁：dataURI→URL 等值、幂等复用、HTML 无 data:image、
// 首屏体积上限与缓存分档、存量 2 MB 值不再进 HTML、/brand 直出与 404/穿越负向、真 routes() 接线、
// 盘上坏件自愈）；另有一条部署后冒烟 deploy/smoke_brand_homepage.sh（A 首屏体积 / B 字段是地址 /
// C 全文无 data:image / D 件真取得到 / E 缺件如实 4xx，自带 --selftest 五例验判据本身有效）。
// 部署口径：本文件与 spa.go 同属**后端直出面** ⇒ 必须换 translator-server 二进制。
// ============================================================================
package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"translator/internal/observability"

	apierrors "translator/internal/errors"
)

// brandAssetPrefix 品牌静态件的公开 URL 前缀（注册串见 server.go，单测与本常量同源）。
const brandAssetPrefix = "/brand/"

// 硬上限：只防「把 100 MB 字符串塞进品牌字段」这一类失控，
// 业务侧的软引导仍在 validateBrandPayloads（新上传按 420KB/1200KB base64 拦）。
// 读侧为什么要更宽：存量行里可能有**上限生效之前**写进去的 2 MB 值，
// 读侧直接拒绝等于「租户的品牌图凭空消失」，那是在用另一个缺陷修这个缺陷。
const (
	brandAssetMaxB64Chars = 16 * 1024 * 1024 // dataURI 的 base64 段字符数上限
	brandAssetMaxBytes    = 12 * 1024 * 1024 // 解码后字节上限
)

// brandImageExts 图片格式白名单：mime → 落盘扩展名（同时是直出路由认的后缀）。
// 键小写比对；svg 刻意缺席，理由见文件头。
var brandImageExts = map[string]string{
	"image/png":  "png",
	"image/jpeg": "jpg",
	"image/jpg":  "jpg",
	"image/webp": "webp",
	"image/gif":  "gif",
}

// brandImageMimes 扩展名 → Content-Type（直出路由用，与上面那张表**互为反向**，
// 新增格式必须两处同改，单测锁这条一致性，免得出现「能落件却 404」的半截支持）。
var brandImageMimes = map[string]string{
	"png":  "image/png",
	"jpg":  "image/jpeg",
	"webp": "image/webp",
	"gif":  "image/gif",
}

// brandMagicMatches 落盘前的字节魔数核验：声明的 mime 与实际字节不符即拒绝。
// 为什么值得多这几行：品牌字段是**管理台可写**的，写侧校验与读侧解析之间历史上
// 出现过「换个名字就行」的偏差（F-44/F-55 同族），这里让「自称 PNG 就得是 PNG」。
func brandMagicMatches(ext string, b []byte) bool {
	has := func(p []byte) bool { return len(b) >= len(p) && string(b[:len(p)]) == string(p) }
	switch ext {
	case "png":
		return has([]byte("\x89PNG\r\n\x1a\n"))
	case "jpg":
		return has([]byte("\xff\xd8\xff"))
	case "gif":
		return has([]byte("GIF87a")) || has([]byte("GIF89a"))
	case "webp":
		return len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP"
	}
	return false
}

// brandHeadMagicOK 只读文件头 12 字节判魔数（供"复用前自检"用）。
// 为什么单列一个小函数而不是直接 os.ReadFile 全量比对：复用路径每帧首页都会走，
// 读满 12 MB 再比对等于把省下来的带宽又花回磁盘；12 字节足够覆盖
// png/jpg/gif/webp 四类的前缀（webp 需要 RIFF…WEBP 两段，正好 12 字节）。
// 读不出来（文件消失/权限）也回 false ⇒ 上层按缺件处理并重写，宁可多写一次也不发坏件。
func brandHeadMagicOK(ext, path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 12)
	n, err := f.Read(head)
	if err != nil && n == 0 {
		return false
	}
	return brandMagicMatches(ext, head[:n])
}

// sanitizeBrandOwner 把归属前缀（租户号 / 平台）收敛成文件名安全串。
func sanitizeBrandOwner(owner string) string {
	out := make([]rune, 0, len(owner))
	for _, r := range strings.ToLower(owner) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			out = append(out, r)
		}
	}
	s := strings.Trim(string(out), "-_")
	if s == "" {
		return "brand"
	}
	return s
}

// isBrandAssetURLName 判断一个文件名是否**已经是本站品牌件路径**（写侧回落后再读、
// 或运维直接把 URL 填进品牌字段这两种情形）：只认根相对路径，
// 明确拒绝 "//host"（协议相对 URL 会被浏览器解析到任意外站）。
func isRootRelativeBrandPath(v string) bool {
	return strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//") && !strings.Contains(v, "\\")
}

// brandImageURL 把品牌图字段收敛成「可以放心注进 HTML 的 URL」。
// 参数：ctx 仅用于结构化告警；owner 文件命名前缀（例 t12-logo / platform-home-bg）；
// val 库里的原值（dataURI / http(s) URL / 根相对路径 / 空）。
// 返回：URL 或空串（空=无品牌图，前端回落默认背景）。**任何情况下都不会返回 dataURI。**
func (s *Server) brandImageURL(ctx context.Context, owner, val string) string {
	v := strings.TrimSpace(val)
	if v == "" {
		return ""
	}
	low := strings.ToLower(v)
	if strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://") || isRootRelativeBrandPath(v) {
		return v // 已是 URL：原样透出，不再加工（幂等，避免二次落件）
	}
	if !strings.HasPrefix(low, "data:") {
		observability.Warn(ctx, "brand_image_untrusted", "owner", owner, "len", len(v))
		return "" // 既非 URL 也非 dataURI：没有可信图形来源，按「未配置」处理
	}
	comma := strings.Index(v, ",")
	if comma <= 0 || comma == len(v)-1 {
		observability.Warn(ctx, "brand_image_malformed", "owner", owner)
		return ""
	}
	meta, payload := strings.ToLower(v[len("data:"):comma]), v[comma+1:]
	// meta 形如 image/png;base64（浏览器 dataURL 一律带 ;base64）
	if !strings.HasSuffix(meta, ";base64") {
		observability.Warn(ctx, "brand_image_not_base64", "owner", owner, "meta", meta)
		return ""
	}
	mime := strings.SplitN(meta, ";", 2)[0]
	ext, ok := brandImageExts[mime]
	if !ok {
		observability.Warn(ctx, "brand_image_unsupported_mime", "owner", mime)
		return ""
	}
	if len(payload) > brandAssetMaxB64Chars {
		observability.Warn(ctx, "brand_image_too_large", "owner", owner, "b64_chars", len(payload))
		return ""
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		if data, err = base64.RawStdEncoding.DecodeString(payload); err != nil {
			observability.Warn(ctx, "brand_image_b64_decode_failed", "owner", owner, "err", err.Error())
			return ""
		}
	}
	if len(data) == 0 || len(data) > brandAssetMaxBytes {
		observability.Warn(ctx, "brand_image_bad_size", "owner", owner, "bytes", len(data))
		return ""
	}
	if !brandMagicMatches(ext, data) {
		observability.Warn(ctx, "brand_image_magic_mismatch", "owner", owner, "mime", mime)
		return ""
	}
	sum := sha256.Sum256(data)
	name := fmt.Sprintf("%s-%s.%s", sanitizeBrandOwner(owner), hex.EncodeToString(sum[:])[:16], ext)
	dir := s.brandAssetDir()
	if dir == "" {
		return "" // 落盘目录不可用：宁可回落默认背景，也不把 dataURI 塞回 HTML
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		observability.Warn(ctx, "brand_image_mkdir_failed", "dir", dir, "err", err.Error())
		return ""
	}
	p := filepath.Join(dir, name)
	// 内容寻址：同名即同哈希，理论上"存在且尺寸对"就能复用（省掉每帧首页重算 SHA-256）。
	// 但盘上文件可能被人工挪动/磁盘写坏（本轮自检就是用"同尺寸改写"造出来的这个态），
	// 所以复用前再验一次**头部魔数与扩展名相符**——只读前 12 字节，代价可忽略；
	// 一旦不符就按"缺件"处理，走下面的重写路径自愈。尺寸门 + 魔数门两把一起才既快又不放过坏件。
	if st, e := os.Stat(p); e == nil && st.Size() == int64(len(data)) && brandHeadMagicOK(ext, p) {
		return brandAssetPrefix + name
	}
	// 先写临时件再 rename：避免并发首屏读到半截文件（写一半就改名的原子性来自 rename）
	tmp := fmt.Sprintf("%s.tmp-%d", p, len(data))
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		observability.Warn(ctx, "brand_image_write_failed", "path", p, "err", err.Error())
		return ""
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		observability.Warn(ctx, "brand_image_rename_failed", "path", p, "err", err.Error())
		return ""
	}
	return brandAssetPrefix + name
}

// brandAssetOwner 静态件归属前缀（读侧与写侧共用同一命名口径，别两处各拼一套）：
// 平台根（tid≤0）用 platform-<字段>，租户用 t<租户号>-<字段>。
func brandAssetOwner(tid int64, field string) string {
	if tid <= 0 {
		return "platform-" + field
	}
	return fmt.Sprintf("t%d-%s", tid, field)
}

// brandImagesForWrite 写侧收敛：把待保存的两张图转成静态件 URL（库里从此只存 URL）。
// 与读侧的分工：读侧对存量**宽容**（转不出就回落默认，不能让老域名的品牌凭空消失），
// 写侧对**本次上传**严格（传了内容却转不出可信图形就 400 拒收）——
// 静默把非空输入转成空串等于替租户删掉了品牌图，那是另一种"状态码诚实、内容说谎"。
// 返回：(logoURL, homeBgURL, 错误说明)；错误说明为空即通过。
func (s *Server) brandImagesForWrite(ctx context.Context, tid int64, logo, homeBg string) (string, string, string) {
	l := s.brandImageURL(ctx, brandAssetOwner(tid, "logo"), logo)
	if strings.TrimSpace(logo) != "" && l == "" {
		return "", "", "品牌 Logo 不是可识别的图片（仅支持 PNG/JPG/WEBP/GIF 的 dataURI 或图片地址）"
	}
	b := s.brandImageURL(ctx, brandAssetOwner(tid, "home-bg"), homeBg)
	if strings.TrimSpace(homeBg) != "" && b == "" {
		return "", "", "首页背景图不是可识别的图片（仅支持 PNG/JPG/WEBP/GIF，请先压缩后上传）"
	}
	return l, b, ""
}

// brandAssetDir 品牌静态件根目录（<UserDataDir>/brand）；配置缺失时返回空串（调用侧按「不落件」处理）。
func (s *Server) brandAssetDir() string {
	if s == nil || s.Cfg == nil {
		return ""
	}
	base := strings.TrimSpace(s.Cfg.UserDataDir)
	if base == "" {
		return ""
	}
	return filepath.Join(base, "brand")
}

// handleBrandAsset GET/HEAD /brand/<名> —— 品牌静态件直出（公开只读、长缓存）。
// 必须注册在 "/" 兜底之前（ServeMux 前缀更长者优先），否则缺件会被 spa.go 的
// SPA 回退成 200 整页 HTML：那是「状态码诚实、内容说谎」的托管物陷阱，
// <img> 拿到 HTML 只会碎图，运维排查时还看不出问题。
func (s *Server) handleBrandAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "品牌静态件仅支持 GET"))
		return
	}
	name := strings.TrimPrefix(r.URL.Path, brandAssetPrefix)
	// 只认**单层**文件名：含 / 或 \ 或以 . 开头（隐藏件/相对段）一律 404，
	// 落盘路径再用 filepath.Base 二次收敛，双保险防穿越。
	if name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") || len(name) > 200 {
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "品牌静态件不存在"))
		return
	}
	dot := strings.LastIndex(name, ".")
	ext := strings.ToLower(strings.TrimPrefix(name[dot:], "."))
	mime, ok := brandImageMimes[ext]
	if !ok || dot <= 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "品牌静态件不存在"))
		return
	}
	dir := s.brandAssetDir()
	base := filepath.Base(name) // 二次收敛（防御式：上面已禁斜杠，这里保证即便规则被改动也只取最后一段）
	p := filepath.Join(dir, base)
	data, err := os.ReadFile(p)
	if err != nil || len(data) == 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "品牌静态件不存在或已被清理"))
		return
	}
	if !brandMagicMatches(ext, data) {
		// 盘上字节与扩展名不符（人工挪件/磁盘被别的进程写过）：如实报错，不把坏字节当图发
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "品牌静态件内容异常"))
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// 文件名含内容哈希 ⇒ 同 URL 必同字节 ⇒ 可以放心 immutable（与 /assets/ 同范式）
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(data)
}
