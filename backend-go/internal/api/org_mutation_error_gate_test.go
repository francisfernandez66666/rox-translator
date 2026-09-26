// ============================================================================
// api/org_mutation_error_gate_test.go — 组织写操作错误分档「文案漂移」机制闸
// （★ F-64② 收尾定夺，2026-09-26 批 I-10）
//
// 背景：orgs.go 的移动/删除两个接口把 store 侧的失败交给 orgMutationError 分档，
// 而该分档函数是**按中文文案子串**判语义的（跨租户 403／不存在 404／成环 400／
// 根组织不可动 409／其余 500）。这样做是有意的取舍：
//
//	· 本批不得动 store（AGENTS §一·1 冻结口径 + 改动面控制），而错误语义此刻只存在于
//	  iam.Store 的 fmt.Errorf 字面量里，没有 sentinel error 可供 errors.Is；
//	· 子串匹配的代价是「文案一改，语义就静默降级成 500」——500 会污染 5xx 告警，
//	  且前端把「拖错了地方」显示成「服务异常」，用户会反复重试一个永远不可能成功的操作。
//
// 于是把这份脆弱性换成断言：本用例**真起一个内存库、真调 store 的 MoveOrg/DeleteOrg
// 拿到真实错误对象**，再喂给 orgMutationError 逐条核对状态码。这样
//
//	① 现在这四档映射是对的（正向锁）；
//	② 任何人改了 iam/store.go 里的错误文案而没同步分档表，这里立刻红灯（漂移闸）；
//	③ 服务端真故障仍然落 500 且文案被脱敏（反向锁，防「什么都判成 4xx」的过度修复）。
//
// 与「纯字符串表驱动」的差别：后者只锁住我今天抄下来的这几句文案，改原文案时它跟着改
// 就永远绿；这里锁的是「store 实际吐什么 → 接口实际回什么」这条真实链路。
//
// 方言：自钉 SQLite 内存库并显式钉死 config.C（AGENTS.md §一·4），
// 命名共享缓存 DSN 保证连接池多连接同库。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestOrgMutationError
// ============================================================================
package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	apierrors "translator/internal/errors"
	"translator/internal/store"
)

// newOrgMutationFixture 装配一棵真实组织树：租户 tid=1 下 root→orgA→deptB，
// 另建租户 2 的 orgC（跨租户用例用）。返回 store 与四个节点 ID。
func newOrgMutationFixture(t *testing.T) (*store.Store, int64, int64, int64, int64) {
	t.Helper()
	pinSqliteDialect(t)
	sqlDB, err := sql.Open("sqlite", fmt.Sprintf("file:orgmut_%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	st, err := store.New(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	const tid, otherTid = int64(1), int64(2)
	root, err := st.EnsureRootOrg(tid, "分档探针租户")
	if err != nil {
		t.Fatalf("EnsureRootOrg: %v", err)
	}
	orgA, err := st.CreateOrg(tid, 0, "研发部", store.OrgTypeOrg)
	if err != nil {
		t.Fatalf("CreateOrg(orgA): %v", err)
	}
	deptB, err := st.CreateOrg(tid, orgA.ID, "前端组", store.OrgTypeDept)
	if err != nil {
		t.Fatalf("CreateOrg(deptB): %v", err)
	}
	if _, err := st.EnsureRootOrg(otherTid, "隔壁租户"); err != nil {
		t.Fatalf("EnsureRootOrg(other): %v", err)
	}
	orgC, err := st.CreateOrg(otherTid, 0, "外部组织", store.OrgTypeOrg)
	if err != nil {
		t.Fatalf("CreateOrg(orgC): %v", err)
	}
	return st, root.ID, orgA.ID, deptB.ID, orgC.ID
}

// TestOrgMutationErrorMapping 用 store 真实吐出的错误对象锁住四档语义映射 + 故障兜底档。
func TestOrgMutationErrorMapping(t *testing.T) {
	st, rootID, orgAID, deptBID, orgCID := newOrgMutationFixture(t)
	req := httptest.NewRequest("POST", "/api/admin/orgs/move", nil)

	cases := []struct {
		name     string
		err      error // 由真实 store 调用产生，不允许手写文案
		wantCode apierrors.ErrorCode
		why      string // 红灯时直接可读的因果说明
	}{
		{
			name: "被移节点归属别的租户",
			// 拿租户 1 的节点去问租户 2：store 的租户归属守卫
			err:      st.MoveOrg(2, orgAID, 0),
			wantCode: apierrors.ErrForbidden,
			why:      "跨租户改结构＝越权语义（403），改参数绕不过去；判成 400 会提示「请检查输入」而这不是输入问题",
		},
		{
			name:     "目标父节点归属别的租户",
			err:      st.MoveOrg(1, deptBID, orgCID),
			wantCode: apierrors.ErrForbidden,
			why:      "挂到别人租户的节点下同样是越权（403）",
		},
		{
			name:     "目标父节点不存在",
			err:      st.MoveOrg(1, deptBID, 999999),
			wantCode: apierrors.ErrNotFound,
			why:      "父节点查不到＝资源态问题（404），重试不会自己变好",
		},
		{
			name:     "移动成环（挂到自己的孩子下面）",
			err:      st.MoveOrg(1, orgAID, deptBID),
			wantCode: apierrors.ErrValidation,
			why:      "父节点落在自己子树里＝父节点不合法（400），客户端换个目标即可修",
		},
		{
			name:     "根组织不可移动",
			err:      st.MoveOrg(1, rootID, orgAID),
			wantCode: apierrors.ErrConflict,
			why:      "载荷没错、错在对象当前状态（409）；判 400 会误导管理员反复改表单",
		},
		{
			name:     "根组织不可删除",
			err:      st.DeleteOrg(rootID),
			wantCode: apierrors.ErrConflict,
			why:      "同上一条：根组织=租户本身，属状态冲突（409）",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.err == nil {
				t.Fatalf("store 调用本该失败却返回 nil——守卫失效或用例构造有误（%s）", c.why)
			}
			ae := orgMutationError(req, c.err)
			if ae == nil {
				t.Fatalf("orgMutationError 返回 nil")
			}
			if ae.Code != c.wantCode {
				t.Fatalf("语义分档错：store 错误=%q → 期望 %s，实得 %s。\n%s\n"+
					"若你改过 iam/store.go 的错误文案，这是文案漂移闸在响：要么把新文案补进 "+
					"orgMutationError 的分档表，要么把 store 文案改回来（AGENTS §一·8 要求状态码按语义取）。",
					c.err.Error(), c.wantCode, ae.Code, c.why)
			}
			// 对外文案必须仍是 store 那句业务话（分档不许顺手改写文案）
			if ae.Message != errText(c.err) {
				t.Fatalf("分档函数改写了对外文案：%q → %q（应原样透出，前端与 UAT 断言依赖这句）",
					errText(c.err), ae.Message)
			}
			if ae.HTTPStatus() < 400 || ae.HTTPStatus() >= 600 {
				t.Fatalf("错误状态码不诚实：HTTP %d", ae.HTTPStatus())
			}
		})
	}
}

// TestOrgMutationErrorInternalFallback 反向锁：真·服务端故障必须落 500 且内部细节不外露。
// 没有这一条，「把什么都判成 4xx」的过度修复也能让上面那组用例全绿。
func TestOrgMutationErrorInternalFallback(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/admin/orgs/move", nil)
	// 造一条带内部特征的驱动级错误（子串扫描口径见 errmsg.go leakMarkers：含 "driver" 即脱敏）
	drvErr := errors.New("driver: bad connection")
	ae := orgMutationError(req, drvErr)
	if ae.Code != apierrors.ErrInternal {
		t.Fatalf("存储层故障应判 500/INTERNAL，实得 %s（判成 4xx 会把服务端故障伪装成用户输入问题）", ae.Code)
	}
	if ae.HTTPStatus() != 500 {
		t.Fatalf("内部故障状态码应为 500，实得 %d", ae.HTTPStatus())
	}
	// 脱敏：响应文案里不能出现驱动/SQL 痕迹，只能是人话那句
	for _, leak := range []string{"driver", "bad connection", "sql"} {
		if containsCI(ae.Message, leak) {
			t.Fatalf("内部实现细节泄漏到对外文案：%q（含 %q）", ae.Message, leak)
		}
	}
}

// errText 取错误的对外文案口径（与 orgMutationError 内部一致，便于「分档不改文案」的等值断言）。
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// containsCI 大小写无关子串判断（脱敏反向锁用：驱动错误名的真实大小写不止一种写法）。
func containsCI(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}
