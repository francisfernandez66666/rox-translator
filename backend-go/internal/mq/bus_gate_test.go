// ============ 本文件职责中文说明 ============
// 二期 mq 核心层行为锁：订阅者 fan-out、panic 隔离、无订阅者只出声不报错、
// 信封 fail-closed、flow_drive 事件形状（含系统字段不许被 payload 伪造）。
// 每把锁配一枚变异体，射程自证＝订阅者数量下限与反序列化回读。
// ========================================
package mq

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// testEnvelope 构造一条合法测试信封（租户 1、主题 flow_drive）。
// 参数：t=测试句柄；eventID=幂等键；返回：信封。
func testEnvelope(t *testing.T, eventID string) Envelope {
	t.Helper()
	env, err := NewEnvelope(TopicFlowDrive, 1, "one-1", eventID, "trace-1", map[string]any{"k": "v"})
	if err != nil {
		t.Fatalf("测试信封构造失败: %v", err)
	}
	return env
}

// B1：同一主题的多个订阅者必须各收到一次，且载荷可读。
func TestBusPublishesToAllSubscribers(t *testing.T) {
	bus := NewBus()
	var mu sync.Mutex
	seen := map[string]int{}
	for _, name := range []string{"a", "b"} {
		name := name
		if err := bus.Subscribe(TopicFlowDrive, func(_ context.Context, env Envelope) error {
			mu.Lock()
			defer mu.Unlock()
			seen[name]++
			return nil
		}); err != nil {
			t.Fatalf("订阅失败: %v", err)
		}
	}
	if got := bus.SubscriberCount(TopicFlowDrive); got != 2 {
		t.Fatalf("B1 射程自证失败：订阅者应为 2，实得 %d", got)
	}
	if err := bus.Publish(context.Background(), testEnvelope(t, "ev-1")); err != nil {
		t.Fatalf("B1：合法信封分发不许报错，实得 %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if seen["a"] != 1 || seen["b"] != 1 {
		t.Fatalf("B1：两个订阅者必须各收到一次，实得 %v", seen)
	}
}

// B2：订阅者 panic 必须被隔离、出声、转成错误，且另一个订阅者仍收到事件。
func TestBusPanicHandlerIsIsolated(t *testing.T) {
	bus := NewBus()
	got := 0
	if err := bus.Subscribe(TopicFlowDrive, func(_ context.Context, _ Envelope) error {
		panic("订阅者故意崩溃")
	}); err != nil {
		t.Fatalf("订阅失败: %v", err)
	}
	if err := bus.Subscribe(TopicFlowDrive, func(_ context.Context, _ Envelope) error {
		got++
		return nil
	}); err != nil {
		t.Fatalf("订阅失败: %v", err)
	}
	err := bus.Publish(context.Background(), testEnvelope(t, "ev-panic"))
	if err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("B2：panic 必须转成可辨错误，实得 %v", err)
	}
	if got != 1 {
		t.Fatalf("B2：panic 不许连累其它订阅者，另一个收到 %d 次", got)
	}
	// 总线仍可用：再发一条，健康订阅者继续收到。
	if err := bus.Publish(context.Background(), testEnvelope(t, "ev-after")); err == nil {
		t.Fatal("B2：panic 订阅者仍在册时第二次发布必须继续报错，不许被静默移除")
	}
	if got != 2 {
		t.Fatalf("B2：总线 panic 后必须仍可分发，健康订阅者累计应 2 次，实得 %d", got)
	}
}

// B3：无订阅者发布不报错（但实现必须 Warn 出声，由源码锁另判）。
func TestBusNoSubscriberDoesNotFail(t *testing.T) {
	bus := NewBus()
	if bus.SubscriberCount(TopicUserEvent) != 0 {
		t.Fatal("B3 夹具不干净：不该有订阅者")
	}
	env, err := NewEnvelope(TopicUserEvent, 1, "one-1", "ev-empty", "", map[string]any{})
	if err != nil {
		t.Fatalf("信封构造失败: %v", err)
	}
	if err := bus.Publish(context.Background(), env); err != nil {
		t.Fatalf("B3：无订阅者不许把事件当失败上抛，实得 %v", err)
	}
}

// B4：信封缺租户／幂等键／主题一律拒收（fail-closed）。
func TestBusRejectsInvalidEnvelope(t *testing.T) {
	bus := NewBus()
	cases := []struct {
		name string
		env  Envelope
	}{
		{"无租户", Envelope{Header: EventHeader{TenantID: 0, EventID: "e"}, Topic: TopicFlowResult}},
		{"无幂等键", Envelope{Header: EventHeader{TenantID: 1}, Topic: TopicFlowResult}},
		{"无主题", Envelope{Header: EventHeader{TenantID: 1, EventID: "e"}}},
	}
	for _, c := range cases {
		if err := bus.Publish(context.Background(), c.env); err == nil {
			t.Fatalf("B4：%s 必须拒收，却放行了", c.name)
		}
	}
}

// B5：flow_drive 事件形状完整，且系统字段覆写调用方伪造值。
func TestFlowDriveEnvelopeShapeAndSystemFields(t *testing.T) {
	bus := NewBus()
	var captured Envelope
	if err := bus.Subscribe(TopicFlowDrive, func(_ context.Context, env Envelope) error {
		captured = env
		return nil
	}); err != nil {
		t.Fatalf("订阅失败: %v", err)
	}
	pub := NewFlowDrivePublisher(bus)
	err := pub.PublishFlowDrive(context.Background(), 7, "one-7", "inst-7", "requeue",
		map[string]any{"tenant_id": int64(999), "instance_id": "forged", "extra": "x"})
	if err != nil {
		t.Fatalf("flow_drive 发布失败: %v", err)
	}
	if captured.Topic != TopicFlowDrive || captured.Header.TenantID != 7 || captured.Header.OneID != "one-7" {
		t.Fatalf("B5：事件头形状不对: %+v", captured.Header)
	}
	if !strings.HasPrefix(captured.Header.EventID, "flowdrive_inst-7_") {
		t.Fatalf("B5：事件幂等键必须带实例标识，实得 %s", captured.Header.EventID)
	}
	var body map[string]any
	if err := json.Unmarshal(captured.Payload, &body); err != nil {
		t.Fatalf("B5：载荷反序列化失败: %v", err)
	}
	if body["tenant_id"].(float64) != 7 || body["instance_id"] != "inst-7" || body["task_type"] != "requeue" {
		t.Fatalf("B5：系统字段必须覆写伪造值，实得 %v", body)
	}
	if body["extra"] != "x" {
		t.Fatalf("B5：调用方附加字段不许丢，实得 %v", body)
	}
	if _, err := captured.Marshal(); err != nil {
		t.Fatalf("B5：信封序列化失败: %v", err)
	}
	raw, _ := captured.Marshal()
	if _, err := UnmarshalEnvelope(raw); err != nil {
		t.Fatalf("B5：信封回读必须过同一套校验: %v", err)
	}
}

// B6：发布器空参/未接总线必须拒收，不许静默丢事件。
func TestFlowDriveRejectsInvalidArguments(t *testing.T) {
	if err := NewFlowDrivePublisher(nil).PublishFlowDrive(context.Background(), 1, "o", "i", "requeue", nil); err == nil {
		t.Fatal("B6：未接总线必须报错")
	}
	pub := NewFlowDrivePublisher(NewBus())
	if err := pub.PublishFlowDrive(context.Background(), 0, "o", "i", "requeue", nil); err == nil {
		t.Fatal("B6：tid<=0 必须拒收")
	}
	if err := pub.PublishFlowDrive(context.Background(), 1, "o", "", "requeue", nil); err == nil {
		t.Fatal("B6：缺实例标识必须拒收")
	}
	if err := pub.PublishFlowDrive(context.Background(), 1, "o", "i", "", nil); err == nil {
		t.Fatal("B6：缺任务类型必须拒收")
	}
}

// B7：订阅入参与事件幂等键的基本射程（防失明）。
func TestSubscribeAndEventIDHaveTeeth(t *testing.T) {
	bus := NewBus()
	if err := bus.Subscribe("", func(context.Context, Envelope) error { return nil }); err == nil {
		t.Fatal("B7：空主题订阅必须拒收")
	}
	if err := bus.Subscribe(TopicUserEvent, nil); err == nil {
		t.Fatal("B7：nil 回调必须拒收")
	}
	ids := map[string]bool{}
	for i := 0; i < 500; i++ {
		id := NewEventID()
		if ids[id] || id == "" {
			t.Fatalf("B7：事件幂等键重复或为空: %q", id)
		}
		ids[id] = true
	}
}
