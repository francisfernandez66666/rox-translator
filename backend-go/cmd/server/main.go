// ============ 本文件职责中文说明 ============
// 服务入口：解析命令行参数（监听地址/前端目录/KB 索引/KB 缓存库）、加载配置、
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
	"translator/internal/kb"
	"translator/internal/llm"
	"translator/internal/observability"
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
	addr := flag.String("addr", ":8787", "HTTP 监听地址")
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
			log.Printf("术语数据库已打开(PostgreSQL): %s", config.C.DatabaseDSN)
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
				log.Printf("[init] 未配置 ADMIN_INIT_PASSWORD，已生成随机初始密码: %s（请登录后立即修改）", initPwd)
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
			log.Printf("[init-db] PostgreSQL schema 已初始化: %s", config.C.DatabaseDSN)
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
	eng.NPZPath = npzPath // 向量索引文件路径（重建时写回）
	if st != nil {
		eng.St = st
	}

	// 加载模型路由策略（system_config.model_routes，admin 可热更新）
	if st != nil {
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
	eng.LLM.OnUsage = srv.ChargeUsageRealtime

	// ★ 性能优化 B2/B3：启动实时计量批量落库（把逐 LLM 调用的写事务合并为周期批量），
	//   彻底消除并发翻译下的 SQLITE_BUSY。
	billing.InitGlobalSink(srv.Bill)

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
