// ============ flowdrive.go · 职责说明 ============
// 二期 mq 核心层：把状态机巡检的 requeue 事件发到 flow_drive 主题。
// ★ 结构化鸭子类型实现 internal/state_machine 的 Publisher 接口
// （PublishFlowDrive(ctx, tenantID int64, oneID, instanceID, taskType string, payload map[string]any) error），
// 刻意 **不 import state_machine**：该包的 Publisher 是本地接口，反向 import 只会制造循环风险。
// 系统字段（租户/实例/任务类型）由本函数覆写，调用方 payload 不许伪造它们。
// ================================================
package mq

import (
	"context"
	"errors"
	"strings"
)

// FlowDrivePublisher 把断点续跑事件投递到进程内总线的发布器。
type FlowDrivePublisher struct {
	bus *Bus
}

// NewFlowDrivePublisher 构造发布器；bus 为 nil 时发布一律报错（不静默丢事件）。
// 参数：bus=进程内总线；返回：实现 state_machine.Publisher 的发布器。
func NewFlowDrivePublisher(bus *Bus) *FlowDrivePublisher {
	return &FlowDrivePublisher{bus: bus}
}

// PublishFlowDrive 发布一条 flow_drive 事件（满足 state_machine.Publisher 接口）。
// 参数：ctx=上下文；tenantID=显式租户；oneID=用户主键；instanceID=流程实例；
// taskType=任务类型；payload=附加字段；返回：投递错误。
func (p *FlowDrivePublisher) PublishFlowDrive(ctx context.Context, tenantID int64, oneID, instanceID string, taskType string, payload map[string]any) error {
	if p == nil || p.bus == nil {
		return errors.New("flow_drive 发布器未接总线")
	}
	if tenantID <= 0 {
		return errors.New("flow_drive 事件必须带有效租户（tid<=0 拒收）")
	}
	if strings.TrimSpace(instanceID) == "" {
		return errors.New("flow_drive 事件缺少流程实例标识")
	}
	if strings.TrimSpace(taskType) == "" {
		return errors.New("flow_drive 事件缺少任务类型")
	}
	body := make(map[string]any, len(payload)+4)
	for k, v := range payload {
		body[k] = v
	}
	// 系统字段最后覆写：调用方给的同名键不许冒充当另一个租户或另一个实例。
	body["tenant_id"] = tenantID
	body["one_id"] = oneID
	body["instance_id"] = instanceID
	body["task_type"] = taskType
	env, err := NewEnvelope(TopicFlowDrive, tenantID, oneID,
		"flowdrive_"+instanceID+"_"+NewEventID(), "", body)
	if err != nil {
		return err
	}
	return p.bus.Publish(ctx, env)
}

// 编译期接口自证：本发布器必须始终满足状态机 Publisher 的签名（不靠运行时断言）。
var _ interface {
	PublishFlowDrive(ctx context.Context, tenantID int64, oneID, instanceID string, taskType string, payload map[string]any) error
} = (*FlowDrivePublisher)(nil)
