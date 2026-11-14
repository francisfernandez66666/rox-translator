// ============ envelope_gate_test.go · 职责说明 ============
// E4（§4.4.3 / §4.4.2 硬口径 3）：信封缺 tenant_id 必拒收（tid<=0），复用 mq.validateEnvelope 口径；
// 反证 M0295「允许 0 ⇒ 红」。三个入口（Publish / NewEnvelope / UnmarshalEnvelope）同一套 fail-closed。
// 本文件放 mq 包内（mq 目录此前无同名文件），可直接钉未导出的 validateEnvelope。
// ================================================
package mq

import (
	"context"
	"testing"
)

func TestEnvelopeRejectsMissingTenant(t *testing.T) {
	bus := NewBus()

	// ① 发布口：validateEnvelope 必须拒 tid=0（M0295 把 <=0 放宽成 <0 会让这里放行 ⇒ 红）。
	zero := Envelope{Header: EventHeader{TenantID: 0, EventID: "e-zero"}, Topic: TopicUserEvent}
	if err := bus.Publish(context.Background(), zero); err == nil {
		t.Fatal("E4 信封缺 tenant_id（tid=0）必须拒收；允许 0 即让缺租户事件流入总线（M0295）")
	}
	// 负租户同样拒收（不放宽成「只挡负」）。
	neg := Envelope{Header: EventHeader{TenantID: -1, EventID: "e-neg"}, Topic: TopicUserEvent}
	if err := bus.Publish(context.Background(), neg); err == nil {
		t.Fatal("E4 负租户 tid=-1 必须拒收")
	}

	// ② 构造口：NewEnvelope(tid<=0) 必须拒收。
	if _, err := NewEnvelope(TopicUserEvent, 0, "o", "e-new", "", nil); err == nil {
		t.Fatal("E4 NewEnvelope(tid=0) 必须拒收")
	}

	// ③ 反序列化口：UnmarshalEnvelope 对缺租户信封同口径拒收。
	rawZero, err := zero.Marshal()
	if err != nil {
		t.Fatalf("E4 序列化零租户信封失败: %v", err)
	}
	if _, err := UnmarshalEnvelope(rawZero); err == nil {
		t.Fatal("E4 UnmarshalEnvelope(tid=0) 必须拒收")
	}

	// ④ 下界自证：合法租户必须放行（不是把所有信封一律拒掉空转锁）。
	busOK := NewBus()
	if err := busOK.Subscribe(TopicUserEvent, func(context.Context, Envelope) error { return nil }); err != nil {
		t.Fatalf("E4 订阅失败: %v", err)
	}
	okEnv, err := NewEnvelope(TopicUserEvent, 7, "o", "e-ok", "", nil)
	if err != nil {
		t.Fatalf("E4 构造合法信封失败: %v", err)
	}
	if err := busOK.Publish(context.Background(), okEnv); err != nil {
		t.Fatalf("E4 合法租户 tid=7 必须放行，实得 %v", err)
	}
}
