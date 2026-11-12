// ============ sales_mq_wiring.go · 职责说明 ============
// 三期 W2 组合根装配（mq 半边）：进程内事件总线**单例**与收件箱消费者，
// 唯一装配点＝NewServer（§25-c「闸门绿着而那条腿从没被走到」形态的防线——
// mq 包迁移后一直没有单例、没有消费者，能力在库在进程里却没人拨）。
//
// ★ 三条口径：
//  1. **单例在组合根**：mq 包刻意无全局单例（bus.go 文件头「订阅者一律由组合根
//     显式注册」），本文件的 wireSalesMQ 是主服务唯一的装配点，禁止在别处再 NewBus。
//  2. **收件箱幂等落账**：flow_drive 事件先 TryProcessInboxEvent 抢账（首胜方处理，
//     撞键败方直接放弃——幂等语义不是错误），W2 只落收件箱+完成标记；
//     真正的断点续跑动作随 W6 成交流程批在 MarkInboxProcessed **之前**插入，
//     不许绕过幂等抢账直接干活。
//  3. **SweepOnce 的发布侧同点装配**：state_machine 的 requeue 事件经
//     mq.NewFlowDrivePublisher 投回本总线（同进程 fan-out 到收件箱消费者）。
// ================================================
package api

import (
	"context"
	"time"

	"translator/internal/mq"
	"translator/internal/observability"
	"translator/internal/state_machine"
	"translator/internal/store"
)

// smSweeper 流程状态机巡检器的窄接口（生产＝state_machine.Service；测试注入计数桩）。
type smSweeper interface {
	SweepOnce(ctx context.Context, heartbeatTimeout time.Duration) int
}

// wireSalesMQ 在组合根装配总线单例、收件箱消费者与巡检器（NewServer 唯一调用点）。
func (s *Server) wireSalesMQ() {
	s.MQBus = mq.NewBus()
	bus := s.MQBus
	// 收件箱消费者：flow_drive 事件先进 inbox_events 幂等落账（首胜方处理）。
	if err := bus.Subscribe(mq.TopicFlowDrive, s.salesInboxConsumer); err != nil {
		observability.Error(context.Background(), "销售事件总线订阅失败", "error", err)
	}
	// 巡检器装配：cfg/pub 按 state_machine.NewService 的可 nil 口径传入；
	// pub 是 requeue 的发布侧（默认 requeue 关，关着不会触碰 pub，见 SweepOnce 文件头）。
	s.smSweeper = state_machine.NewService(s.Store, smSweepCfgReader{s.Store}, mq.NewFlowDrivePublisher(bus))
}

// salesInboxConsumer 收件箱消费者（mq.EventHandler 签名，经 bus.Subscribe 注册）。
func (s *Server) salesInboxConsumer(ctx context.Context, env mq.Envelope) error {
	first, err := s.Store.TryProcessInboxEvent(env.Header.TenantID, env.Header.EventID, env.Header.OneID, env.Topic)
	if err != nil {
		observability.Warn(ctx, "收件箱幂等落账失败", "event_id", env.Header.EventID, "error", err)
		return err
	}
	if !first {
		return nil // 已被处理过／并发败方：幂等语义，不是错误
	}
	// ★ W6 断点续跑（插在 MarkInboxProcessed **之前**，W2 注释预留的位置）：
	//   flow_drive 事件到达即刷新该 one_id 的 running 流程实例心跳——与 SweepOnce 的
	//   「心跳超时判卡死」闭环：卡死实例被 requeue 派发到收件箱消费＝证明它活着，
	//   下一轮巡检不再把它当卡死。幂等抢账仍由 TryProcessInboxEvent 保证（上方 first 闸），
	//   续跑动作不绕过幂等直接干活。
	if _, err := s.Store.TouchFlowInstanceByOneID(env.Header.TenantID, env.Header.OneID); err != nil {
		observability.Warn(ctx, "流程实例续跑心跳失败", "event_id", env.Header.EventID, "error", err)
	}
	// ★ W2 边界：落账即打完成标记。
	if _, err := s.Store.MarkInboxProcessed(env.Header.TenantID, env.Header.EventID); err != nil {
		observability.Warn(ctx, "收件箱完成标记失败", "event_id", env.Header.EventID, "error", err)
		return err
	}
	return nil
}

// smSweepCfgReader 把 store 的 GetConfig 适配成 state_machine.ConfigReader 的 GetBool。
// store.Store 没有 GetBool（只有 GetConfig）；这个小适配器是唯一的桥，禁止在 store
// 侧为此加方法（store 冻结纪律，§一·1）。
type smSweepCfgReader struct{ st *store.Store }

// GetBool 读配置键成布尔（"1"/"true"/"on" 为真；空串/其他按调用方给的缺省档）。
func (r smSweepCfgReader) GetBool(key string, def bool) bool {
	if r.st == nil {
		return def
	}
	v, _ := r.st.GetConfig(key)
	switch v {
	case "1", "true", "on":
		return true
	case "0", "false", "off":
		return false
	default:
		return def
	}
}

// smSweepEnabled 读巡检 kill-switch（sm_sweep_enabled）：只有显式配 "1" 才开。
// ★ 缺省关是二期 §14 Q3 判据的一半——测试环境不配置该键 ⇒ runSMSweepOnce 直接
// 返回、SweepOnce 调用计数恒 0（反证：把缺省档翻成 true 必须红，A05/M0212）。
func (s *Server) smSweepEnabled() bool {
	if s.Store == nil {
		return false
	}
	v, _ := s.Store.GetConfig("sm_sweep_enabled")
	return v == "1"
}

// runSMSweepOnce 流程状态机巡检单轮（watchdog「sm-sweep」槽每小时拨一次；
// ★ 不做 @startup 启动即跑——生产首跑由部署步显式拨一次，A05 静态锁钉住）。
// 心跳超时档 15 分钟：推进点会刷 heartbeat_ts，超过该档未见心跳即按卡死处理
// （SweepOnce 内部先收口终态脏数据；requeue 由 sm_sweep_requeue_enabled 另行控制，缺省仅记录）。
func (s *Server) runSMSweepOnce() {
	if !s.smSweepEnabled() || s.smSweeper == nil {
		return
	}
	ctx := context.Background()
	requeued := s.smSweeper.SweepOnce(ctx, 15*time.Minute)
	// 有回填才出声：每小时一轮的空扫打 INFO 会淹没真正的卡死读数（§一·3 观测口径）。
	if requeued > 0 {
		observability.Info(ctx, "流程状态机巡检完成（断点续跑回填）", "requeued", requeued)
	}
}
