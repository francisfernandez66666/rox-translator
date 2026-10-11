// 本文件锁 P1③（2026-10-11 慢 SQL 批）：postgres 连接池兜底默认 10 idle / 30 分钟寿命。
// 两半射程缺一不可：
//  1. **守卫级**——applyPoolDefaults 真跑（假 setter 记参），不是只测 poolSettings 的纯判据
//     （AGENTS §三「判据级断言代替不了守卫级」那条形态）；
//  2. **接线级**——openPostgres 源码里真调 applyPoolDefaults，而 openSQLite **绝不许**调
//     （SQLite 的 :memory: 共享缓存库会被连接寿命销毁本体，兜底档搬过去＝测试库全体蒸发）。
package db

import (
	"os"
	"strings"
	"testing"
	"time"
)

// recSetter 记录三档 setter 的调用参数（-1 起步：一次调用都没发生与"设成 0"分得开，
// 否则恒空转的假 setter 也能让上面三条绿灯）。
type recSetter struct {
	open int
	idle int
	life time.Duration
}

func newRecSetter() *recSetter { return &recSetter{open: -1, idle: -1, life: -1} }

func (r *recSetter) SetMaxOpenConns(n int)              { r.open = n }
func (r *recSetter) SetMaxIdleConns(n int)              { r.idle = n }
func (r *recSetter) SetConnMaxLifetime(d time.Duration) { r.life = d }

func TestPoolDefaultsFallbackWhenUnset(t *testing.T) {
	r := newRecSetter()
	applyPoolDefaults(r, Config{})
	if r.open != 20 || r.idle != 10 || r.life != 30*time.Minute {
		t.Fatalf("env/配置都没给时的兜底档期望 {20,10,30m} 实际 {open:%d idle:%d life:%v}（旧形态是 Go 出厂的 {20?,2,∞}）", r.open, r.idle, r.life)
	}
}

func TestPoolDefaultsRespectExplicitConfig(t *testing.T) {
	// 显式配了值（env>库>代码默认的前两级由调用方传进 cfg）：语义必须一字不变。
	r := newRecSetter()
	applyPoolDefaults(r, Config{MaxOpenConns: 50, MaxIdleConns: 8, ConnMaxLifetimeMinutes: 5})
	if r.open != 50 || r.idle != 8 || r.life != 5*time.Minute {
		t.Fatalf("显值被兜底档覆盖: {open:%d idle:%d life:%v}", r.open, r.idle, r.life)
	}
}

func TestPoolDefaultsIdleNeverExceedsOpen(t *testing.T) {
	// 「配了 100 idle / 20 open」的运维读数必须在解析处就掰直，而不是留到排障时骗人。
	r := newRecSetter()
	applyPoolDefaults(r, Config{MaxOpenConns: 3, MaxIdleConns: 10, ConnMaxLifetimeMinutes: 0})
	if r.open != 3 || r.idle != 3 || r.life != 30*time.Minute {
		t.Fatalf("maxIdle 越界钳制期望 {3,3,30m} 实际 {open:%d idle:%d life:%v}", r.open, r.idle, r.life)
	}
}

func TestPoolDefaultsWiringOnlyInPostgres(t *testing.T) {
	src, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("读 db.go 失败: %v", err)
	}
	body := string(src)
	// 正向：openPostgres 必须真调 applyPoolDefaults（拆掉这行＝兜底档成纯函数摆设，现网仍是出厂 2/∞）。
	i := strings.Index(body, "func openPostgres(")
	j := strings.Index(body[i:], "\nfunc ")
	if i < 0 || j < 0 {
		t.Fatal("找不到 openPostgres 函数体（接线锁的射程依赖函数边界，先让它红得明白）")
	}
	if !strings.Contains(body[i:i+j], "applyPoolDefaults(") {
		t.Fatal("openPostgres 没调 applyPoolDefaults ⇒ 连接池兜底从接线退化回'什么都不设'")
	}
	// 负向：openSQLite 里绝不许出现 applyPoolDefaults / SetConnMaxLifetime（搬过去会把 :memory: 测试库按时销毁）。
	k := strings.Index(body, "func openSQLite(")
	l := strings.Index(body[k:], "\nfunc ")
	if k < 0 || l < 0 {
		t.Fatal("找不到 openSQLite 函数体")
	}
	if seg := body[k : k+l]; strings.Contains(seg, "applyPoolDefaults(") || strings.Contains(seg, "SetConnMaxLifetime") {
		t.Fatal("openSQLite 被接上连接池兜底 ⇒ :memory: 共享缓存库会随连接寿命被销毁")
	}
}
