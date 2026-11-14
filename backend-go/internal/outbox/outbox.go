// ============ outbox.go · 职责说明 ============
// 四期 W4 · 事件总线骨架（outbox + relay + 幂等 inbox + 第一个消费者）。
//
// 四条硬口径（施工 §4.4.2）：
//  1. outbox 与业务写同事务（不许「先写业务再补事件」）——见 writepoint.go。
//  2. 消费幂等键唯一 (topic, event_id)；ON CONFLICT DO NOTHING 后 0 行即跳过——见 consumer.go / migrate.go。
//  3. 信封必带 tenant_id，tid<=0 拒收（复用 mq.validateEnvelope / mq.UnmarshalEnvelope 口径）。
//  4. relay 只做投递、不做业务；失败重试与死信复用 queue 的 Reserve/MarkFailed，不新造第二套调度——见 relay.go。
//
// 依赖方向：本包只依赖 db / mq / queue / observability 四个基础包，**不 import store、不 import api**
// （store/outbox_migrate.go 与 api/lead_captured_outbox.go 单向薄委托进来，杜绝循环依赖）。
// ================================================
package outbox

import (
	"context"

	"translator/internal/mq"
	"translator/internal/queue"
)

// 主题与任务类型常量。业务写点只许用登记过的主题（RegisteredTopics 是白名单）。
const (
	// TopicLeadCaptured 第一个事件化派生：留资进销售 inbox / 通知 / 归因（§1.3 裁定 3 的「可事件化」栏）。
	// 钱与接待主链永不事件化，故这里刻意不登记扣费/答复/鉴权主题。
	TopicLeadCaptured = "lead.captured"

	// JobTypeOutboxDelivery relay 搬运进 queue 账本时用的任务类型（worker 按此分发到 consumer）。
	JobTypeOutboxDelivery = "outbox.delivery"

	// relayWorkerID 领取投递任务的 worker 标识（queue.Reserve 的租约持有者）。
	relayWorkerID = "outbox-relay"
)

// event_outbox 行的状态。pending=待 relay 搬运；relayed=已搬运进 queue 账本（行不删，留审计）。
const (
	StatusPending = "pending"
	StatusRelayed = "relayed"
)

// 投递任务的默认参数（与 queue.DefaultMaxAttempts / DefaultLeaseSec 同口径，不在本包另立第二把尺子）。
const (
	DeliveryMaxAttempts = queue.DefaultMaxAttempts
	DeliveryLeaseSec    = queue.DefaultLeaseSec
)

// RegisteredTopics 是本批登记的业务写点白名单（§4.4.3 E1「名单外的写点为零」的运行时守卫）：
// InsertOutboxEvent 拒收未登记主题，避免「新增业务写点却绕过 outbox 登记」被静默放过。
// 新增可事件化主题时必须显式在此登记并补一条断言用例，禁止绕过。
var RegisteredTopics = map[string]bool{
	TopicLeadCaptured: true,
}

// StoredEvent 待写入 event_outbox 的一行。Payload 是完整 mq.Envelope 的 JSON
// （头里必带 tenant_id / event_id / trace_id），relay 靠 mq.UnmarshalEnvelope 还原后投递。
type StoredEvent struct {
	Topic    string
	EventID  string
	TenantID int64
	OneID    string
	TraceID  string
	Payload  []byte
}

// DeliveryQueue 是 relay 依赖的投递账本窄接口：方法签名与 queue.Queue 的四个方法逐字一致，
// 因此真实 queue.Queue（如 queue.NewDirect(st.DB())）可零适配直接注入，测试可注入内存 fake。
// ★ relay 不自己记 attempts、不建死信表：重试/死信完全落在 queue.MarkFailed 的 CASE WHEN 语义里，
//
//	这就是「不新造第二套调度」——见 direct.go:MarkFailed。
type DeliveryQueue interface {
	Enqueue(ctx context.Context, jobType string, payload []byte, maxAttempts int) (int64, error)
	Reserve(ctx context.Context, workerID string, leaseSec int) (*queue.Job, error)
	MarkDone(ctx context.Context, jobID int64) error
	MarkFailed(ctx context.Context, jobID int64, errMsg string) error
}

// 编译期自证：真实 queue.Queue 的方法集必须始终覆盖 DeliveryQueue（防止两把尺子漂移）。
var _ DeliveryQueue = (queue.Queue)(nil)

// Deliverer 是 relay 的「投递」动作：签名与 mq.EventHandler / consumer.Handle 一致。
// 默认生产接 mq.Bus.Publish（fan-out 到已注册 consumer），测试直接注入 consumer.Handle。
// relay 只调它，绝不自己写 event_inbox / 读模型 / 发通知（「只做投递、不做业务」）。
type Deliverer func(ctx context.Context, env mq.Envelope) error
