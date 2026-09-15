// ============ opskey · 职责说明 ============
// API Key 运维小工具（生产服务器 root 经 secrets.env 环境变量连库执行）：
//
//	opskey -create -tid 1 -user 4 -name capacity-loadtest [-perms translate] [-limit 0]
//	opskey -list -tid 1
//	opskey -revoke <api_key_id> -tid 1
//
// 用途：压测/集成演练需要一次性明文 Key 时签发与回收（管理端 Reveal 需会话，
// 本工具仅运维现场使用；创建输出的明文只显示一次，回收即时生效于鉴权链路）。
// 注意：需 `set -a; . /etc/translator/secrets.env; set +a` 后执行（依赖 DB_*/JWT_SECRET）。
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"time"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/store"

	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

// main 命令行入口：解析模式开关后初始化配置与存储，再按 resetpw/grant/create/revoke/list 分发。
func main() {
	// 参数定义：三个 Key 生命周期操作（create/list/revoke）+ 额度发放 grant + 灾备用 resetpw，公共定位参数 -tid/-user
	create := flag.Bool("create", false, "签发新 Key")
	list := flag.Bool("list", false, "列出租户全部 Key")
	revoke := flag.Int64("revoke", 0, "按 id 回收 Key（配 -tid）")
	grant := flag.Bool("grant", false, "发放额度 Grant（-tid -amount [-kind trial] [-days 30] [-source ops]）")
	amount := flag.Int64("amount", 0, "Grant token 数")
	kind := flag.String("kind", "trial", "Grant 类型（trial/topup/paid…）")
	days := flag.Int("days", 30, "Grant 有效天数")
	source := flag.String("source", "opskey", "Grant 备注")
	resetpw := flag.Int64("resetpw", 0, "重置指定用户口令（★ 仅灾备/演练 scratch 库使用，严禁在生产执行）")
	pw := flag.String("pw", "", "新口令（配 -resetpw）")
	tid := flag.Int64("tid", 0, "租户 ID")
	userID := flag.Int64("user", 0, "归属用户 ID（-create 必填）")
	name := flag.String("name", "ops", "Key 名称")
	perms := flag.String("perms", "translate", "权限范围 all/translate/kb/billing")
	limit := flag.Int64("limit", 0, "每日调用上限（0=不限）")
	flag.Parse()

	// 先初始化全局配置（store 迁移的方言判定读 config.C.DatabaseDriver）
	cfg := config.Default()
	if cfg.DatabaseDSN == "" {
		log.Fatal("DB_DSN 未设置：请在生产服务器 `set -a; . /etc/translator/secrets.env; set +a` 后执行")
	}
	driver := "postgres"
	if cfg.DatabaseDriver != "postgres" {
		driver = "sqlite"
	}
	db, err := sql.Open(driver, cfg.DatabaseDSN)
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}
	defer db.Close()
	st, err := store.New(db)
	if err != nil {
		log.Fatalf("存储初始化失败: %v", err)
	}

	// 按互斥模式分发：重置口令 → 发放额度 → 签发 Key → 回收 Key → 列表；resetpw 直连 UPDATE 仅限演练库
	switch {
	case *resetpw > 0:
		if *pw == "" {
			log.Fatal("-resetpw 需要 -pw")
		}
		hash := auth.PasswordHash(*pw)
		if _, err := st.DB().Exec("UPDATE users SET password_hash=$1 WHERE id=$2", hash, *resetpw); err != nil {
			log.Fatalf("重置口令失败: %v", err)
		}
		fmt.Println("password reset for user", *resetpw)
	// 额度发放：CreateQuotaGrant 按 -days 计算到期时间，-source 留痕（审计/对账用）
	case *grant:
		if *tid <= 0 || *amount <= 0 {
			log.Fatal("-grant 需要 -tid 与 -amount")
		}
		if err := st.CreateQuotaGrant(*tid, *kind, *amount, time.Now().AddDate(0, 0, *days), *source, 0); err != nil {
			log.Fatalf("发放失败: %v", err)
		}
		fmt.Printf("granted %d tokens (%s, %dd) to tenant %d\n", *amount, *kind, *days, *tid)
	case *create:
		if *tid <= 0 || *userID <= 0 {
			log.Fatal("-create 需要 -tid 与 -user")
		}
		plain, err := st.CreateAPIKey(*tid, *userID, *name, *perms, *limit)
		if err != nil {
			log.Fatalf("签发失败: %v", err)
		}
		fmt.Println(plain) // 仅运维现场一次性输出（压测脚本经管道直接消费，勿落日志）
	case *revoke > 0:
		if err := st.SetAPIKeyStatus(*revoke, *tid, "disabled"); err != nil {
			log.Fatalf("回收失败: %v", err)
		}
		fmt.Println("disabled", *revoke)
	case *list:
		ks, err := st.ListAPIKeys(*tid)
		if err != nil {
			log.Fatalf("查询失败: %v", err)
		}
		for _, k := range ks {
			fmt.Printf("%d\t%s\t%s\t%s\n", k.ID, k.Name, k.Status, k.KeyPrefix)
		}
	default:
		log.Fatal("用法见文件头注释（-create/-list/-revoke）")
	}
}
