// ============ main.go · 职责说明 ============
// cmd/server 包服务入口。
// 解析命令行参数（监听地址/前端目录/KB 索引/KB 缓存库）、加载配置、
// 依次初始化术语数据库、租户存储、SaaS 平台存储（admin 账号/默认三级包/余额账户）、
// 知识库向量索引、翻译引擎、评估器与模型路由策略，最后启动 HTTP 服务。
// =============================================
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"log"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	httppprof "net/http/pprof" // 注册 pprof Handler（仅经下方独立回环监听器暴露，外网不可达）
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"translator/internal/api"
	"translator/internal/auth"
	"translator/internal/billing"
	"translator/internal/config"
	"translator/internal/engine"
	"translator/internal/evals"
	"translator/internal/fileproc"
	"translator/internal/kb"
	"translator/internal/llm"
	"translator/internal/observability"
	"translator/internal/sensitive"
	"translator/internal/store"
	"translator/internal/tenant"

	// Redis 单例（阶段二）：REDIS_ADDR 非空即启用分布式锁/信号量/配额；空则降级进程内。
	"translator/internal/infra/redis"

	// PostgreSQL 驱动注册（P0-3）：当选型 DB_DRIVER=postgres 时由连接器(db.Open)使用。
	// 未启用时仅为 inert 依赖，不影响 SQLite 默认路径。
	_ "github.com/lib/pq"
)

// main 服务启动入口。
func main() {
	// ★ 性能优化（不换库 Phase A3）：低配机器（1G）上给 Go 运行时的软内存上限留足余量给
	//   文档转换子进程（pdf2docx/LibreOffice）。未显式设置 GOMEMLIMIT 时默认 650Mi，
	//   部署可经环境变量覆盖（见 deploy/systemd/prod.conf）。
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(650 << 20)
	}
	// ★ 结构化日志（工作流 C）：以 slog JSON 日志器替换默认 logger，全链路带 trace_id。
	slog.SetDefault(observability.NewLogger())
	// 命令行参数：监听地址、前端静态目录、KB 向量索引与 KB 缓存库路径
	addr := flag.String("addr", "127.0.0.1:8787", "HTTP 监听地址（★ S1：开发默认回环；对外部署须显式绑定且 DB_DRIVER=postgres）")
	frontend := flag.String("frontend", "", "前端 dist 目录（默认相对路径 ./frontend/dist）")
	kbNpz := flag.String("kb", "", "知识库 .npz 文件路径；留空则不加载")
	kbDB := flag.String("kbdb", "", "知识库 SQLite 缓存路径（默认 <npz 同名>.db）")
	initDB := flag.Bool("init-db", false, "仅初始化数据库 schema（建表/扩展/默认数据）后退出，用于 PostgreSQL 切流前置")
	flag.Parse()

	cfg := config.Default()
	// ★ 生产密钥强校验（评审整改 D2）：REQUIRE_PROD_SECRETS=1 时（systemd drop-in 显式开启），
	//   三把关键凭证任一缺失即拒绝启动——杜绝「随机兜底密钥」静默上线
	//   （JWT 默认值可伪造 token；ADMIN_TOKEN 随机则支付回调头注入永远对不上）。
	if os.Getenv("REQUIRE_PROD_SECRETS") == "1" {
		var missing []string
		for _, k := range []string{"JWT_SECRET", "ADMIN_INIT_PASSWORD", "ADMIN_TOKEN"} {
			if os.Getenv(k) == "" {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			log.Fatalf("[init] 生产密钥强校验已开启（REQUIRE_PROD_SECRETS=1），缺少环境变量: %s；请参照部署指南 §六 配置后重启", strings.Join(missing, ", "))
		}
	}
	// ★ S1（2026-09-12 决策：存储统一 PostgreSQL，SQLite 退出生产）：
	//   生产强拒 PG 外选项——REQUIRE_PROD_SECRETS=1 或 APP_ENV=prod/production 时 SQLite 直接 FATAL；
	//   非回环监听（对外可达）同样禁止 SQLite；仅回环监听允许 SQLite 作为开发/测试便捷态（大声告警）。
	if config.C.DatabaseDriver != "postgres" {
		envFlag := strings.ToUpper(strings.TrimSpace(os.Getenv("APP_ENV")))
		prodMark := os.Getenv("REQUIRE_PROD_SECRETS") == "1" || envFlag == "PROD" || envFlag == "PRODUCTION"
		if prodMark {
			log.Fatalf("[config] S1 拒绝启动：生产标记（REQUIRE_PROD_SECRETS/APP_ENV）已开启但 DB_DRIVER=%s；生产唯一受支持方言为 postgres，请设置 DB_DRIVER + DB_DSN 后重启", config.C.DatabaseDriver)
		}
		if !isLoopbackListen(*addr) {
			log.Fatalf("[config] S1 拒绝启动：监听地址 %s 非回环（对外可达）而 DB_DRIVER=%s；SQLite 仅限本机开发/测试（-addr 127.0.0.1:8787），对外部署必须 DB_DRIVER=postgres", *addr, config.C.DatabaseDriver)
		}
		log.Printf("[config] ⚠️ 开发模式 SQLite（回环 %s）：生产统一 PostgreSQL，SQLite 不享受并发行锁语义（扣费/队列按 SQLite 写锁兜底），禁止对外部署", *addr)
	}

	// ★ 加载可执行目录 / 项目根的 config.json（model 字段）
	exeDir, _ := filepath.Abs(filepath.Dir(os.Args[0]))
	cfg.LoadConfigFromJSON(exeDir)
	uploadDir, _ := filepath.Abs(cfg.UploadDir)
	os.MkdirAll(uploadDir, 0o755) // 确保上传目录存在
	cfg.UploadDir = uploadDir

	// ★ 阶段二：Redis 单例初始化（REDIS_ADDR 非空启用分布式能力，空则降级进程内实现）。
	redis.Init(cfg.RedisAddr, cfg.RedisPassword)
	if redis.Enabled() {
		if err := redis.Ping(); err != nil {
			log.Printf("[init] 警告: Redis 探活失败（%v），分布式能力降级为进程内实现", err)
		} else {
			log.Printf("[init] Redis 已启用: %s", cfg.RedisAddr)
		}
	}

	// 解析前端 dist 目录（默认 ../frontend/dist，存在 index.html 才启用）
	distDir := *frontend
	if distDir == "" {
		distDir, _ = filepath.Abs(filepath.Join("..", "frontend", "dist"))
		if _, err := os.Stat(filepath.Join(distDir, "index.html")); err != nil {
			distDir = "" // dist 不存在则禁用静态托管
		}
	} else {
		abs, err := filepath.Abs(distDir)
		if err == nil {
			distDir = abs
		}
	}

	var db *kb.KBDatabase
	var kbIndex *kb.Index

	// 打开术语数据库
	dbPath := *kbDB
	if dbPath == "" {
		dbPath = cfg.DBPath // 回退配置默认路径
	}
	var openErr error
	db, openErr = kb.Open(dbPath)
	if openErr != nil {
		// 打开失败仅告警，不阻塞启动（后续功能受限）
		log.Printf("警告: 术语数据库打开失败: %v", openErr)
		db = nil
	} else {
		if config.C.DatabaseDriver == "postgres" {
			log.Printf("术语数据库已打开(PostgreSQL): %s", config.MaskDSN(config.C.DatabaseDSN))
		} else {
			log.Printf("术语数据库已打开(SQLite): %s", dbPath)
		}
		// ★ 多租户迁移：tm_segments 加 tenant_id 列 + 既有数据归入 rox
		if err := db.EnsureTenantMigration(); err != nil {
			log.Printf("警告: 租户迁移失败: %v", err)
		}
	}

	// ★ 租户存储（含默认租户 rox）
	var ts *tenant.Store
	if db != nil {
		ts, _ = tenant.NewStore(db.RawDB()) // 共享同一 SQLite 连接
		if ts != nil {
			if id, err := ts.EnsureDefault(); err != nil {
				log.Printf("警告: 默认租户初始化失败: %v", err)
			} else {
				log.Printf("默认租户 rox 就绪 (id=%d)", id)
			}
		}
	}

	// ★ SaaS 平台存储（users/tickets/kb包/计费/审计/系统配置等）
	var st *store.Store
	if db != nil {
		var storeErr error
		st, storeErr = store.New(db.RawDB())
		if storeErr != nil {
			log.Printf("[init] 存储初始化失败（Store 将为 nil，登录等接口不可用）: %v", storeErr)
		}
		if st != nil {
			// 初始 admin 账号（密码来自 ADMIN_INIT_PASSWORD，未配置则随机生成并打印）
			initPwd := os.Getenv("ADMIN_INIT_PASSWORD")
			if initPwd == "" {
				initPwd = genRandomPass(12)
				// ★ B1（2026-09-12 日志脱敏）：随机初始密码不再打印 stdout（journalctl/云日志
				//   长期留存=凭据泄漏面），改写 0600 本机文件，日志仅提示路径。
				pwdFile := filepath.Join(cfg.UserDataDir, "admin-initial-password.txt")
				if werr := os.WriteFile(pwdFile, []byte(initPwd+"\n"), 0o600); werr != nil {
					log.Printf("[init] ⚠️ 未配置 ADMIN_INIT_PASSWORD 且初始密码文件写入失败(%v)：无法登录，请设置 ADMIN_INIT_PASSWORD 后重启", werr)
				} else {
					log.Printf("[init] 未配置 ADMIN_INIT_PASSWORD：随机初始超管密码已写入 %s（chmod 600，登录后立即修改并删除该文件）", pwdFile)
				}
			}
			adminEmail := os.Getenv("ADMIN_EMAIL")
			if err := st.EnsureAdmin(1, "admin", auth.PasswordHash(initPwd), "系统管理员", adminEmail); err != nil {
				log.Printf("警告: admin 账号初始化失败: %v", err)
			}
			_ = st.EnsureBalance(0)
			_ = st.EnsureDefaultPackages(0)
			if err := st.EnsureDefaultPackages(1); err != nil {
				log.Printf("警告: 默认 KB 包初始化失败: %v", err)
			}
			if err := st.EnsureBalance(1); err != nil {
				log.Printf("警告: 默认余额账户初始化失败: %v", err)
			}
		}
	}

	// ★ 阶段一 PG 切流落地点：--init-db 仅初始化 schema（建表/默认数据）后退出，
	// 供 cutover 脚本在迁移前一次性建立 PG 表结构（含 pgvector 列）。
	if *initDB {
		if config.C.DatabaseDriver == "postgres" {
			log.Printf("[init-db] PostgreSQL schema 已初始化: %s", config.MaskDSN(config.C.DatabaseDSN))
		} else {
			log.Printf("[init-db] SQLite schema 已初始化: %s", cfg.DBPath)
		}
		log.Println("[init-db] 退出（未启动 HTTP 服务）")
		os.Exit(0)
	}

	// 加载知识库向量索引 (.npz)
	npzPath := *kbNpz
	if npzPath != "" {
		idx, err := kb.LoadNPZ(npzPath)
		if err != nil {
			log.Printf("警告: 知识库索引加载失败 %s: %v", npzPath, err)
		} else {
			kbIndex = idx
			log.Printf("知识库向量索引已加载: %d 条", len(idx.IDs))
		}
	}

	// 创建翻译引擎（挂载 DB 与向量索引）
	eng := engine.NewEngine(cfg, db, kbIndex, ts)
	// ★ S8 敏感词兑底闸：装载平台词包（文件缺失/空=闸口关闭，热加载免重启）
	eng.Sensitive = sensitive.New(cfg.SensitiveWordsFile)
	log.Printf("[sensitive] 词包 %s 词条数=%d（0=兑底闸未启用）", cfg.SensitiveWordsFile, eng.Sensitive.Count())
	eng.NPZPath = npzPath // 向量索引文件路径（重建时写回）
	if st != nil {
		eng.St = st
	}

	// 加载模型路由策略（system_config.model_routes，admin 可热更新）
	if st != nil {
		// ★ 存量迁移（S0-S1 安全整改）：历史明文的供应商 Key 一次性加密回写，
		//   消除「备份/psql 顺手导出即泄 Key」的敞口（D3 只保证了新保存路径）。
		if v, err := st.GetConfig("model_routes"); err == nil && v != "" {
			var raw []config.ProviderConfig
			if json.Unmarshal([]byte(v), &raw) == nil {
				dirty := false
				for i := range raw {
					if raw[i].APIKey != "" && !strings.HasPrefix(raw[i].APIKey, store.SecretEncPrefix) {
						raw[i].APIKey = store.EncryptSecret(raw[i].APIKey)
						dirty = true
					}
				}
				if dirty {
					if b, e := json.Marshal(raw); e == nil && st.SetConfig("model_routes", string(b)) == nil {
						log.Printf("[init] model_routes 存量明文密钥已加密回写")
					}
				}
			}
		}
		if v, err := st.GetConfig("model_routes"); err == nil && v != "" {
			var routes []config.ProviderConfig
			if json.Unmarshal([]byte(v), &routes) == nil && len(routes) > 0 {
				// ★ 库内为 enc:v1: 密文（评审整改 D3）：水合时解密；解密失败的路由打告警跳过
				alive := make([]config.ProviderConfig, 0, len(routes))
				for _, rt := range routes {
					dec := store.DecryptSecret(rt.APIKey)
					if dec == "" && strings.HasPrefix(rt.APIKey, store.SecretEncPrefix) {
						log.Printf("[init] 路由 %s(%s) 密钥解密失败（疑 JWT_SECRET 轮换未同步），该路由停用", rt.Provider, rt.Model)
						continue
					}
					rt.APIKey = dec
					alive = append(alive, rt)
				}
				cfg.ModelRoutes = alive
				log.Printf("模型路由策略已加载: %d 条", len(alive))
			}
		}
		// ★ 启动水合：全局 Key 为占位符且主路由带真实密钥时回填，
		// 修复「面板保存过真实 Key 但引擎兜底仍用占位符」的断链
		if cfg.OnlineAPIKeyIsPlaceholder || cfg.OnlineAPIKey == "" {
			for _, r := range cfg.ModelRoutes {
				if r.APIKey != "" && !strings.HasPrefix(r.APIKey, "sk-****") {
					cfg.OnlineAPIKey = r.APIKey
					cfg.OnlineAPIKeyIsPlaceholder = false
					log.Printf("全局 API Key 已从主路由水合（provider=%s model=%s）", r.Provider, r.Model)
					break
				}
				// ★ 后台可配 LLM Key 启动水合（2026-08-27，并入「全局模型」tab 的一部分）：
				//   后台在 /api/admin/models/save 中把翻译/向量密钥以密文落库到 system_config，
				//   此处启动时优先读取这些库内配置并覆盖（环境变量与 model_routes 的）默认值，
				//   实现「后台设置优先、重启后仍生效」。
				if v, _ := st.GetConfig("online_api_key"); v != "" {
					if dec := store.DecryptSecret(v); dec != "" {
						cfg.OnlineAPIKey = dec
						cfg.OnlineAPIKeyIsPlaceholder = false
						log.Println("[llmkey] 已从后台配置水合 在线翻译 Key")
					}
				}
				if v, _ := st.GetConfig("online_api_base"); v != "" {
					cfg.OnlineAPIBase = v
				}
				if v, _ := st.GetConfig("online_model"); v != "" {
					cfg.OnlineModel = v
				}
				if v, _ := st.GetConfig("embed_api_key"); v != "" {
					if dec := store.DecryptSecret(v); dec != "" {
						cfg.EmbedAPIKey = dec
						log.Println("[llmkey] 已从后台配置水合 Embedding Key")
					}
				}
				if v, _ := st.GetConfig("embed_api_base"); v != "" {
					cfg.EmbedAPIBase = v
				}
			}

		}
	}

	// ★ evals 评估器（Judge 用 Online Key；可用 EVALS_JUDGE_KEY 覆盖）。
	// 占位 Key 传空 → 评估器自动禁用，避免必失败的空转调用。
	judgeKey := cfg.OnlineAPIKey
	if cfg.OnlineAPIKeyIsPlaceholder {
		judgeKey = ""
	}
	if v := os.Getenv("EVALS_JUDGE_KEY"); v != "" {
		judgeKey = v // 环境变量优先覆盖
	}
	if st != nil {
		eng.Evals = evals.New(cfg, llm.NewClient(cfg), st, judgeKey)
	}

	// 创建 HTTP 服务
	srv := api.NewServer(cfg, eng, db, distDir, st, ts)

	// ★ 边工作边计费：将实时扣费钩子挂到引擎的 LLM 客户端。
	// 每次 chat/embed 调用产生真实 token 用量后立即扣减租户余额，余额不足即中止翻译，
	// 覆盖即时翻译 / 翻译工单 / OpenAPI 三类入口，杜绝后置计费被取消绕过的白嫖。
	// ★ H7：usage 回调双写——计费 + 路由 token 成本统计（动态权重数据源）
	prevUsage := srv.ChargeUsageRealtime
	eng.LLM.OnUsage = func(ctx context.Context, model string, prompt, completion int64) error {
		eng.ObserveRouteTokens(model, prompt, completion)
		return prevUsage(ctx, model, prompt, completion)
	}
	// ★ H2 术语约束解码能力探测：所有存活主/备 chat 路由均声明 supports_constraints
	//   才启用事前注入（任一不支持即整链路降级 H1，避免 x_term_constraints 打到普通端点）。
	if eng.LLM != nil && len(config.C.ModelRoutes) > 0 {
		supportAll := true
		for _, rt := range config.C.ModelRoutes {
			if rt.APIBase == "" {
				continue
			}
			if !rt.SupportsConstraints {
				supportAll = false
				break
			}
		}
		eng.LLM.SupportsConstraints = supportAll
		if supportAll {
			log.Printf("[init] H2 术语约束解码已启用（全部 %d 条路由声明 supports_constraints）", len(config.C.ModelRoutes))
		}
	}
	// ★ 评估器（evals）使用独立 LLM client：同样挂上计费钩子，
	//   否则 Judge（初翻评估/校对评估）调用不进入 usage_ledger（调用了但白嫖）。
	if eng.Evals != nil {
		eng.Evals.LLM.OnUsage = srv.ChargeUsageRealtime
	}

	// ★ 性能优化 B2/B3：启动实时计量批量落库（把逐 LLM 调用的写事务合并为周期批量），
	//   彻底消除并发翻译下的 SQLITE_BUSY。
	billing.InitGlobalSink(srv.Bill)

	// 文件处理依赖健康检查（Python/LibreOffice）
	fileproc.CheckHealth()

	log.Printf("能言 v2.0.0-go 服务已启动: http://localhost%s", *addr)
	s := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 30 * time.Second, // 请求头读取超时防慢速攻击
	}

	// 优雅停机：监听 SIGTERM/SIGINT，先停止接收新请求并等待在途请求完成（最多 10 秒）再退出。
	// 保障：systemd restart / 手动重启时正在进行的翻译任务不会被强行掐断，SQLite 数据一致。
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
		sig := <-quit
		log.Printf("收到退出信号 %v，正在优雅停机…", sig)
		billing.DefaultSink.Stop() // ★ 性能优化 B2/B3：停机前完成最终批量落库
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			log.Printf("优雅停机超时，强制退出: %v", err)
			s.Close()
		}
		log.Println("服务已安全退出")
	}()

	// pprof 诊断端点（三期）：仅绑定本机回环地址，外网/反代不可达。
	// 用途：内存增长定位（curl 127.0.0.1:18787/debug/pprof/heap > heap.out 后 go tool pprof 分析）。
	// 端口可经 PPROF_ADDR 覆盖；设为 off 关闭。
	pprofAddr := os.Getenv("PPROF_ADDR")
	if pprofAddr == "" {
		pprofAddr = "127.0.0.1:18787"
	}
	if pprofAddr != "off" {
		go func() {
			mux := http.NewServeMux()
			mux.HandleFunc("/debug/pprof/", httppprof.Index)
			mux.HandleFunc("/debug/pprof/cmdline", httppprof.Cmdline)
			mux.HandleFunc("/debug/pprof/profile", httppprof.Profile)
			mux.HandleFunc("/debug/pprof/symbol", httppprof.Symbol)
			mux.HandleFunc("/debug/pprof/trace", httppprof.Trace)
			srv := &http.Server{Addr: pprofAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
			log.Printf("pprof 诊断端点已启动（仅本机）: http://%s/debug/pprof/", pprofAddr)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("pprof 端点启动失败（不影响主服务）: %v", err)
			}
		}()
	}

	if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("HTTP 服务启动失败: %v", err)
	}
}

// genRandomPass 生成 n 位随机密码（大写+小写+数字混合，初始密码临时用）。
func genRandomPass(n int) string {
	if n < 8 {
		n = 8
	}
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	out := make([]byte, n)
	for i := range out {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			out[i] = 'a'
			continue
		}
		out[i] = charset[idx.Int64()]
	}
	return string(out)
}

// isLoopbackListen 判断监听地址是否仅绑定回环（127.0.0.1/[::1]/localhost:port）。
// 空 host（":8787"）视为全网卡监听 → 非回环。★ S1 生产判定用。
func isLoopbackListen(addr string) bool {
	host := addr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host = addr[:i]
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	if hps, err := net.LookupHost(host); err == nil && len(hps) > 0 {
		for _, ip := range hps {
			if parsed := net.ParseIP(ip); parsed == nil || !parsed.IsLoopback() {
				return false
			}
		}
		return true
	}
	return false
}
