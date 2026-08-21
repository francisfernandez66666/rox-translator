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
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"translator/internal/api"
	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/engine"
	"translator/internal/evals"
	"translator/internal/kb"
	"translator/internal/llm"
	"translator/internal/store"
	"translator/internal/tenant"
)

// main 服务启动入口。
func main() {
	// 命令行参数：监听地址、前端静态目录、KB 向量索引与 KB 缓存库路径
	addr := flag.String("addr", ":8787", "HTTP 监听地址")
	frontend := flag.String("frontend", "", "前端 dist 目录（默认相对路径 ./frontend/dist）")
	kbNpz := flag.String("kb", "", "知识库 .npz 文件路径；留空则不加载")
	kbDB := flag.String("kbdb", "", "知识库 SQLite 缓存路径（默认 <npz 同名>.db）")
	flag.Parse()

	cfg := config.Default()
	// ★ 加载可执行目录 / 项目根的 config.json（model 字段）
	exeDir, _ := filepath.Abs(filepath.Dir(os.Args[0]))
	cfg.LoadConfigFromJSON(exeDir)
	uploadDir, _ := filepath.Abs(cfg.UploadDir)
	os.MkdirAll(uploadDir, 0o755) // 确保上传目录存在
	cfg.UploadDir = uploadDir

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
		log.Printf("术语数据库已打开: %s", dbPath)
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
		st, _ = store.New(db.RawDB())
		if st != nil {
			// 初始 admin 账号（密码来自 ADMIN_INIT_PASSWORD，未配置则随机生成并打印）
			initPwd := os.Getenv("ADMIN_INIT_PASSWORD")
			if initPwd == "" {
				initPwd = genRandomPass(12)
				log.Printf("[init] 未配置 ADMIN_INIT_PASSWORD，已生成随机初始密码: %s（请登录后立即修改）", initPwd)
			}
			if err := st.EnsureAdmin(1, "admin", auth.PasswordHash(initPwd), "系统管理员"); err != nil {
				log.Printf("警告: admin 账号初始化失败: %v", err)
			}
			if err := st.EnsureDefaultPackages(1); err != nil {
				log.Printf("警告: 默认 KB 包初始化失败: %v", err)
			}
			if err := st.EnsureBalance(1); err != nil {
				log.Printf("警告: 默认余额账户初始化失败: %v", err)
			}
		}
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
	if st != nil {
		eng.St = st
	}

	// ★ evals 评估器（Judge 用 Online Key；可用 EVALS_JUDGE_KEY 覆盖）
	judgeKey := cfg.OnlineAPIKey
	if v := os.Getenv("EVALS_JUDGE_KEY"); v != "" {
		judgeKey = v // 环境变量优先覆盖
	}
	if st != nil {
		eng.Evals = evals.New(cfg, llm.NewClient(cfg), st, judgeKey)
	}

	// 加载模型路由策略（system_config.model_routes，admin 可热更新）
	if st != nil {
		if v, err := st.GetConfig("model_routes"); err == nil && v != "" {
			var routes []config.ProviderConfig
			if json.Unmarshal([]byte(v), &routes) == nil && len(routes) > 0 {
				cfg.ModelRoutes = routes // 覆盖默认路由策略
				log.Printf("模型路由策略已加载: %d 条", len(routes))
			}
		}
	}

	// 创建 HTTP 服务
	srv := api.NewServer(cfg, eng, db, distDir, st, ts)

	log.Printf("翻译助手 v2.0.0-go 服务已启动: http://localhost%s", *addr)
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
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			log.Printf("优雅停机超时，强制退出: %v", err)
			s.Close()
		}
		log.Println("服务已安全退出")
	}()

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
