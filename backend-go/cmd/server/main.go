package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
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

func main() {
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
	os.MkdirAll(uploadDir, 0o755)
	cfg.UploadDir = uploadDir

	distDir := *frontend
	if distDir == "" {
		distDir, _ = filepath.Abs(filepath.Join("..", "frontend", "dist"))
		if _, err := os.Stat(filepath.Join(distDir, "index.html")); err != nil {
			distDir = ""
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
		dbPath = cfg.DBPath
	}
	var openErr error
	db, openErr = kb.Open(dbPath)
	if openErr != nil {
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
		ts, _ = tenant.NewStore(db.RawDB())
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
			// 初始 admin 账号（admin/admin123）与默认三级包
			if err := st.EnsureAdmin(1, "admin", auth.PasswordHash("admin123"), "系统管理员"); err != nil {
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

	eng := engine.NewEngine(cfg, db, kbIndex, ts)

	// ★ evals 评估器（Judge 用 Online Key；可用 EVALS_JUDGE_KEY 覆盖）
	judgeKey := cfg.OnlineAPIKey
	if v := os.Getenv("EVALS_JUDGE_KEY"); v != "" {
		judgeKey = v
	}
	if st != nil {
		eng.Evals = evals.New(cfg, llm.NewClient(cfg), st, judgeKey)
	}

	// 加载模型路由策略（system_config.model_routes，admin 可热更新）
	if st != nil {
		if v, err := st.GetConfig("model_routes"); err == nil && v != "" {
			var routes []config.ProviderConfig
			if json.Unmarshal([]byte(v), &routes) == nil && len(routes) > 0 {
				cfg.ModelRoutes = routes
				log.Printf("模型路由策略已加载: %d 条", len(routes))
			}
		}
	}

	srv := api.NewServer(cfg, eng, db, distDir, st, ts)

	log.Printf("翻译助手 v2.0.0-go 服务已启动: http://localhost%s", *addr)
	s := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 30 * time.Second,
	}
	if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("HTTP 服务启动失败: %v", err)
	}
	_ = context.Background()
}