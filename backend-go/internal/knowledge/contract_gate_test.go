package knowledge

import (
	"reflect"
	"testing"

	"translator/internal/knowledge/api"
	"translator/internal/knowledge/contract"
)

// KN1 契约三方等值（服务端实现 / 主服务客户端 / 契约包），逐字段 + 逐枚举 + 逐路径。
// ★ 客户端多一个字段/多一个枚举值/多一个路径（M0283）⇒ 与契约包不等 ⇒ 此处红。
func TestKN1ThreeFacesEqual(t *testing.T) {
	canonical := contract.CanonicalFace()
	client := ClientFace()
	server := api.ServerFace()

	if !reflect.DeepEqual(canonical, client) {
		t.Fatalf("KN1 三方等值失败：客户端面与契约包不等（客户端多/少字段或路径会在此红）")
	}
	if !reflect.DeepEqual(canonical, server) {
		t.Fatalf("KN1 三方等值失败：服务端面与契约包不等")
	}
	// 判据现算（不写死 DTO/枚举名单）：字段集、枚举集、路径集都必须非空。
	if len(canonical.DTOFields) == 0 || len(canonical.Enums) == 0 || len(canonical.Paths) != len(contract.EndpointPaths) {
		t.Fatalf("KN1 契约面为空/路径数不对: dto=%d enums=%d paths=%d",
			len(canonical.DTOFields), len(canonical.Enums), len(canonical.Paths))
	}
	if canonical.Version != contract.ContractVersion {
		t.Fatalf("KN1 契约版本不等: %s != %s", canonical.Version, contract.ContractVersion)
	}
	for name, fs := range canonical.DTOFields {
		if len(fs) == 0 {
			t.Fatalf("KN1 DTO %s 零字段（判据失明）", name)
		}
	}
	for name, vs := range canonical.Enums {
		if len(vs) == 0 {
			t.Fatalf("KN1 枚举 %s 零值（判据失明）", name)
		}
	}
}

// 反证判别力：客户端多一个字段 / 多一个枚举值，等值比较器必须判为不等（不是空转锁）。
func TestKN1MutantDiscriminatesExtraClientField(t *testing.T) {
	mutant := ClientFace()
	mutant.DTOFields["I18nListResponse"] = append(mutant.DTOFields["I18nListResponse"], "Extra|extra")
	if reflect.DeepEqual(contract.CanonicalFace(), mutant) {
		t.Fatalf("KN1 判据失明：客户端多一个字段却判等值（空转锁）")
	}
	mutant2 := ClientFace()
	mutant2.Enums["health_state"] = append(mutant2.Enums["health_state"], "bogus")
	if reflect.DeepEqual(contract.CanonicalFace(), mutant2) {
		t.Fatalf("KN1 判据失明：多一个枚举值却判等值")
	}
	mutant3 := ClientFace()
	mutant3.Paths = append(mutant3.Paths, "/internal/knowledge/bogus")
	if reflect.DeepEqual(contract.CanonicalFace(), mutant3) {
		t.Fatalf("KN1 判据失明：多一个路径却判等值")
	}
}
