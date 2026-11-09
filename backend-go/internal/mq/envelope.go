// ============ envelope.go · 职责说明 ============
// 二期六包平移第三刀（mq 核心层）：标准事件信封与四类固定主题。
// ★ 迁入即换：外仓的 time.Time 一律改成 TEXT RFC3339 UTC（能言时间口径），
// 租户 ID 改成显式 int64（fail-closed），事件 ID 带随机分量防多实例撞键
// （撞了会让 inbox 幂等把新事件误判成「已处理」）。
// 本文件只放纯类型与序列化，不碰数据库、不启协程、不注册全局单例。
// ================================================
package mq

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// 四类固定主题常量（与外仓主题面等名，便于后续 flow/chatflow 平移对齐）。
const (
	// TopicUserEvent 上行行为事件：业务层 → 流程引擎与 CDP 消费者。
	TopicUserEvent = "user_event"
	// TopicFlowDrive 下行驱动事件：流程引擎 → 业务系统（任务、触达、断点续跑）。
	TopicFlowDrive = "flow_drive"
	// TopicFlowResult 结果回流事件：业务系统 → 编排层。
	TopicFlowResult = "flow_result"
	// TopicTenantCfgEvent 配置流事件：配置中心 → 各引擎热加载。
	TopicTenantCfgEvent = "tenant_cfg_event"
)

// EventHeader 事件头：租户、用户、事件唯一键、事件时间与链路号。
// 时间列是字符串而非 time.Time，主仓全部时间列按 TEXT RFC3339 UTC 落库。
type EventHeader struct {
	TenantID  int64  `json:"tenant_id"`
	OneID     string `json:"one_id"`
	EventID   string `json:"event_id"`
	EventTime string `json:"event_time"`
	TraceID   string `json:"trace_id"`
}

// Envelope 标准事件信封：头＋主题＋分区键＋载荷字节。
type Envelope struct {
	Header  EventHeader `json:"header"`
	Topic   string      `json:"topic"`
	Key     string      `json:"key"`
	Payload []byte      `json:"payload"`
}

// EventHandler 消费侧回调（订阅者按主题注册，由组合根显式装配）。
type EventHandler func(ctx context.Context, env Envelope) error

// NewEventID 生成带随机分量的事件唯一键（hex 串，冲突概率趋零）。
// 返回：形如 ev_<unixnano>_<random> 的稳定长度字符串。
func NewEventID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// 随机源不可用时退回时间＋计数外的确定性串，绝不返回空串（空串会让幂等键失效）。
		return "ev_" + time.Now().UTC().Format("20060102150405.000000000")
	}
	return "ev_" + hex.EncodeToString(b)
}

// NewEnvelope 构造一条可投递信封，租户与时间由本函数统一钉死，调用方不许伪造。
// 参数：ctx=请求上下文（仅取 trace，不持有）；topic=四类主题之一；tid=显式租户；
// oneID=用户主键；eventID=幂等键；payload=任意可序列化对象；返回信封与错误。
func NewEnvelope(topic string, tid int64, oneID, eventID, traceID string, payload any) (Envelope, error) {
	if tid <= 0 {
		return Envelope{}, errors.New("事件信封必须带有效租户（tid<=0 一律拒收）")
	}
	if strings.TrimSpace(topic) == "" {
		return Envelope{}, errors.New("事件信封缺少主题")
	}
	if strings.TrimSpace(eventID) == "" {
		return Envelope{}, errors.New("事件信封缺少幂等键 event_id")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, errors.New("事件载荷序列化失败: " + err.Error())
	}
	return Envelope{
		Header: EventHeader{
			TenantID:  tid,
			OneID:     oneID,
			EventID:   eventID,
			EventTime: time.Now().UTC().Format(time.RFC3339),
			TraceID:   traceID,
		},
		Topic:   topic,
		Key:     oneID,
		Payload: raw,
	}, nil
}

// Marshal 把信封序列化成 JSON（供审计与后续跨进程总线复用）。
// 参数：无（值接收者）；返回：字节与错误。
func (e Envelope) Marshal() ([]byte, error) { return json.Marshal(e) }

// UnmarshalEnvelope 从 JSON 还原信封，并按同一套 fail-closed 口径校验。
// 参数：raw=JSON 字节；返回：信封与错误。
func UnmarshalEnvelope(raw []byte) (Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return Envelope{}, errors.New("事件信封反序列化失败: " + err.Error())
	}
	if e.Header.TenantID <= 0 || strings.TrimSpace(e.Header.EventID) == "" || strings.TrimSpace(e.Topic) == "" {
		return Envelope{}, errors.New("事件信封缺租户、幂等键或主题，拒收")
	}
	return e, nil
}
