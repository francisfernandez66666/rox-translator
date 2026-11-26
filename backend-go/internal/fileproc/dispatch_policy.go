// ============ dispatch_policy.go · 职责说明 ============
// 工单粒度的派发决策器（2026-11-26，《施工方案_工单动态派发与上传闸门抬升_20261125.md》§3.1）。
//
// 一条工单在开始处理时**只问一次**：整单交广州机（Dispatch=true），还是整单留主站。
// 决策结果在工单生命周期内锁定——语言循环、块重试、超限接手都不许重判，
// 否则会出现"一半在广州一半在主站"的混合形态，产物落点、计费、清理全乱。
//
// fail-closed：任何读数缺失/不可信/无余量，一律 local。宁可主站慢一点，也不派错一单。
// =============================================
package fileproc

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
)

// lastPolicyReason 最近一次工单派发决策的 Reason（只出状态词，供 /api/health 展示）。
// 用 atomic.Value 无锁读；未决策过时为空串，健康面归 "off"。
var lastPolicyReason atomic.Value

// LastPolicyReason 健康面读口（只出词）。
func LastPolicyReason() string {
	v := lastPolicyReason.Load()
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// NotePolicyReason 决策出口统一留痕（决策器是纯函数，留痕由编排层显式调用）。
func NotePolicyReason(reason string) { lastPolicyReason.Store(reason) }

// DispatchDecision 工单派发决策（只出 bool 与状态词，不含任何主机/路径/凭据）。
type DispatchDecision struct {
	Dispatch bool   // true=整单交广州机做完；false=整单主站做完
	Reason   string // 状态词，可进日志/健康面；不得含 host/key/path/IP
}

// 决策表九档的 Reason 词（对外排障契约，测试逐字钉住）。
const (
	policyOff             = "off"
	policyRemoteUnready   = "remote_unready"
	policyBelowTier       = "below_tier"
	policyOverCapacity    = "over_capacity"
	policyRemoteBusy      = "remote_busy"
	policyRemoteDiskLow   = "remote_disk_low"
	policyMainHasRoom     = "main_has_room"
	policyMainPressured   = "main_pressured"
	policyRemoteIdle      = "remote_idle"
	policyBalancedLocal   = "balanced_local"
)

// remoteCapacity 决策器内部读数（与 RemoteCapacitySnapshot 字段一一对应）。
type remoteCapacity struct {
	MemAvailableMB int64
	DiskFreeMB     int64
	WorkDirUsedMB  int64
	CPULoad1       float64
	CPUQuotaUsed   float64
}

// RemoteCapacitySnapshot 广州机读数快照（导出供 engine 编排层构造）。
// 字段与决策器内部 remoteCapacity 一一对应；转换用显式赋值，杜绝两套结构悄悄漂移。
type RemoteCapacitySnapshot struct {
	MemAvailableMB int64
	DiskFreeMB     int64
	WorkDirUsedMB  int64
	CPULoad1       float64
	CPUQuotaUsed   float64 // cgroup v2 cpu.stat 采样窗内的配额占用率（口径 13）
}

// ToInternal 转成决策器内部读数。
func (s RemoteCapacitySnapshot) ToInternal() remoteCapacity {
	return remoteCapacity{
		MemAvailableMB: s.MemAvailableMB,
		DiskFreeMB:     s.DiskFreeMB,
		WorkDirUsedMB:  s.WorkDirUsedMB,
		CPULoad1:       s.CPULoad1,
		CPUQuotaUsed:   s.CPUQuotaUsed,
	}
}

// RemoteCapacityNow 从最近一次 probe 快照取广州动态读数（不现拨网络，沿用 stale-while-revalidate 视图）。
// 缺字段一律 -1（fail-closed）；CPUQuotaUsed 由调用方另填（cpu.stat 采样在 engine 侧做）。
func RemoteCapacityNow() RemoteCapacitySnapshot {
	rc := RemoteCapacitySnapshot{MemAvailableMB: -1, DiskFreeMB: -1, WorkDirUsedMB: -1, CPULoad1: -1, CPUQuotaUsed: -1}
	if !DispatchEnabled() {
		return rc
	}
	p, err := displayProbe()
	if err != nil || p == nil {
		return rc
	}
	if p.MemAvailableMB != nil {
		rc.MemAvailableMB = *p.MemAvailableMB
	}
	if p.DiskFreeMB != nil {
		rc.DiskFreeMB = *p.DiskFreeMB
	}
	if p.WorkDirUsedMB != nil {
		rc.WorkDirUsedMB = *p.WorkDirUsedMB
	}
	if p.CPULoad1 != nil {
		rc.CPULoad1 = *p.CPULoad1
	}
	return rc
}

// LocalAvailPct 主站可用内存比例（0..100，读 /proc/meminfo 的 MemAvailable/MemTotal）。
// 读不到返回 -1（fail-closed：决策器拿到负值会判 local）。
func LocalAvailPct() float64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return -1
	}
	defer f.Close()
	var total, avail int64
	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total, _ = strconv.ParseInt(fields[1], 10, 64)
		case "MemAvailable:":
			avail, _ = strconv.ParseInt(fields[1], 10, 64)
		}
	}
	if total <= 0 {
		return -1
	}
	return float64(avail) / float64(total) * 100
}

// RemoteSlotsFree 远端并发闸余量（非阻塞；<=0 表示忙）。
// 用 select 探测，不占名额——真正领取名额发生在 DispatchRun 内。
func RemoteSlotsFree() int {
	g := remoteGateChan()
	select {
	case g <- struct{}{}:
		<-g
		return cap(g) - len(g)
	default:
		return 0
	}
}

// DecideTicketDispatch 由两侧实时读数决定这一单去哪边做完（整单锁定，调用一次）。
//
// 参数：
//   localAvailPct 主站可用内存比例（0..100，由注入函数现算）
//   inputs/pages/payloadLen 与 DispatchEligible 同口径
//   remoteSlotsFree 远端并发闸余量（<=0 判忙）
//   rc 广州机动态读数
func DecideTicketDispatch(localAvailPct float64, inputs []string, pages int,
	payloadLen int64, remoteSlotsFree int, rc remoteCapacity) DispatchDecision {
	local := func(reason string) DispatchDecision { return DispatchDecision{false, reason} }
	remote := func(reason string) DispatchDecision { return DispatchDecision{true, reason} }

	// 规则 1：总闸关 ⇒ local（off）；远端读数不可信 ⇒ local（remote_unready）
	// 两者分开报，排障时一眼能分清是"没开闸"还是"闸开着但对端读数坏了"。
	if !DispatchEnabled() {
		return local(policyOff)
	}
	if !rc.ready() {
		return local(policyRemoteUnready)
	}
	// 规则 2：不够格（体积/页数/格式白名单）
	if !DispatchEligible(inputs, pages, payloadLen) {
		return local(policyBelowTier)
	}
	// 规则 2b：超出派发能力上限（200 MiB 能上传，不代表广州跑得动）
	if maxBytesFor(inputs, payloadLen) > dispatchMaxPayloadBytes() {
		return local(policyOverCapacity)
	}
	// 规则 3：并发闸无余量
	if remoteSlotsFree <= 0 {
		return local(policyRemoteBusy)
	}
	// 规则 4：磁盘底线（共盘，留 2G 给量化）
	if rc.DiskFreeMB < 0 || rc.DiskFreeMB < 2048 {
		return local(policyRemoteDiskLow)
	}

	// 规则 5：广州内存紧且主站有空间 ⇒ local
	if (rc.MemAvailableMB < 0 || rc.MemAvailableMB < 1024) && localAvailPct >= 30 {
		return local(policyMainHasRoom)
	}

	// 规则 6：主站吃紧，且广州有基本余量 ⇒ dispatch
	if localAvailPct < 20 && rc.MemAvailableMB >= 1024 && rc.DiskFreeMB >= 2048 {
		return remote(policyMainPressured)
	}

	// 规则 6b：广州明显空闲（cgroup 配额占用低＋内存/盘宽裕），主站不紧 ⇒ dispatch
	// 口径 13 关键：CPU 判据读 fpd.slice 的 cpu.stat（rc.CPUQuotaUsed），不读整机 load。
	if rc.CPUQuotaUsed >= 0 && rc.CPUQuotaUsed <= 0.5 &&
		rc.MemAvailableMB >= 2048 && rc.DiskFreeMB >= 4096 && localAvailPct >= 20 {
		return remote(policyRemoteIdle)
	}

	// 规则 7：其余一律 local
	return local(policyBalancedLocal)
}

// ready 判广州读数是否可信：任何关键字段缺失（-1）都判不就绪。
// ★ 与 Selftest *int 同口径：缺键解成 nil 后由调用方映射为 -1，不许当 0 用。
func (r remoteCapacity) ready() bool {
	return r.MemAvailableMB >= 0 && r.DiskFreeMB >= 0 && r.WorkDirUsedMB >= 0
}

// maxBytesFor 取本单体积：有输入件按最大输入文件，无输入件（payload 腿）按 payload 长度。
// 与 DispatchEligible 的体积腿同口径，不另造尺子。
func maxBytesFor(inputs []string, payloadLen int64) int64 {
	var maxSize int64
	for _, p := range inputs {
		if fi, err := os.Stat(p); err == nil && fi.Size() > maxSize {
			maxSize = fi.Size()
		}
	}
	if maxSize > payloadLen {
		return maxSize
	}
	return payloadLen
}
