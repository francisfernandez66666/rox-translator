package api

import (
	"encoding/json"
	"testing"
)

// TestOpenAPIV1JSONEmbedValid 嵌入式 OpenAPI 规范必须为合法 JSON（此前损坏致 /openapi/v1.json 输出坏文件）。
func TestOpenAPIV1JSONEmbedValid(t *testing.T) {
	var v map[string]interface{}
	if err := json.Unmarshal(openapiV1JSON, &v); err != nil {
		t.Fatalf("嵌入式 openapi.v1.json 非法: %v", err)
	}
	if _, ok := v["paths"]; !ok {
		t.Fatalf("缺少 paths")
	}
	if _, ok := v["components"]; !ok {
		t.Fatalf("缺少 components")
	}
}
