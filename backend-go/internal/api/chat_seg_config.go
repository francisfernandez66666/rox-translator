// ============ 本文件职责中文说明 ============
// ★ DEF-P1-05 B2（2026-11-01 即时翻译段落能力）的两条运行期档位进管理台：
//
//	施工方案 §B2-2 明写「档位数字只走 env > system_config > 代码默认，且要给运营配」，
//	而 §19 九-4 那条裁决定了形态——**走现成的运行期档位面，不新造页面、不新开第二套 KV 编辑形态**。
//	所以本文件逐条照抄 `text_line_config.go` 的四件事：接口形状、鉴权档、回显口径、写侧拒收。
//
// 接口（仅平台超管）：
//   - GET  /api/admin/config/chat-segments  回显两档**生效值**（现读 chatSegTiers 那唯一读点）＋库内原值＋代码缺省＋合法区间
//   - POST /api/admin/config/chat-segments  保存 {"per_paragraph":1,"max_points":60} 或 {"reset":true}
//
// ★★ 这一族与逐行翻译那族**判据方向相反**，照抄前必须先看清这三条：
//
//	① **0 是一个真实档位**（那条族是"0＝一枪都不打"所以写侧拒收）：
//	   · `per_paragraph=0` ＝「关掉分段、这一单走整篇旧路径」——这是上线后要能按下去的那颗闸；
//	   · `max_points=0`    ＝「不设积分上限」（长文档一律允许分段）。
//	   所以写侧**不许**沿用"0/负数一律拒收"，否则运营关不掉分段、上限也清不掉。
//	② 正因 0 合法，**读侧两个键都必须走 `ConfigIntAllowZero`**：`ConfigInt` 把 ≤0 一律读成
//	   "库里没表态"而回代码缺省 ⇒ 写 0 等于没写，那颗闸按不下去（`chatSegTiers` 的注释同一条）。
//	③ 上界越界仍然拒收，且读侧钳位与接口拒收**必须是同一个数**（`engine.ChatSegmentMaxPtsLo/Hi`）；
//	   两侧各写一份迟早漂，漂了的表现两侧都"看起来对"（同 §一·5④「两把尺子」那一族）。
//
// 为什么回显要现读生效值而不是把库内字面量原样吐回去：同 text_line_config.go 的理由，
// 库里躺着 "abc" 或 999999999 时，界面若回显那个字面量，运营原样点一次保存＝把一个系统
// 从没在用过的数钉成正式配置；表单从此成为误导源。
// ==========================================
package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"translator/internal/engine"
	apierrors "translator/internal/errors"
	"translator/internal/store"
)

// chatSegCfgField 一条分段档位在管理台上的表述。
// zeroIsReal 记录「0 这一档在这一族里是不是合法值」——本族两条都是，但它必须**写在结构里**而不是
// 靠注释：下一位照抄 text_line_config.go 那套"0 拒收"时，判据在这里当场对不上（等值锁按值判）。
type chatSegCfgField struct {
	jsonField  string // 请求体里的字段名
	key        string // system_config 键名（唯一事实源＝engine 那两个常量，本文件不重抄字面量）
	def        int    // 代码缺省（与读侧同一个常量）
	lo, hi     int    // 合法区间（含端点）——写侧拒收与读侧钳位同一个区间
	zeroIsReal bool   // 0 是不是一个真实档位（本族＝true；逐行翻译那族＝false）
}

// chatSegCfgFields 两条可配档位：顺序＝表单展示顺序，也是"两项必须一次交齐"的判据来源。
// ⚠️ 区间的数值只由 engine 那一组常量决定，本表**引用**它们；两处各写一份数字＝两把尺子
//
//	（前端还有一份镜像，由 src/lib/chatSegTiers.crossSide.lock.test.ts 三方等值钉住）。
var chatSegCfgFields = []chatSegCfgField{
	{"per_paragraph", engine.CfgKeyChatPerParagraph, engine.ChatPerParagraphDefault, 0, 1, true},
	{"max_points", engine.CfgKeyChatSegmentMaxPts, engine.ChatSegmentMaxPtsDefault, engine.ChatSegmentMaxPtsLo, engine.ChatSegmentMaxPtsHi, true},
}

// handleAdminChatSegConfig GET/POST /api/admin/config/chat-segments —— 即时翻译分段档位超管口。
func (s *Server) handleAdminChatSegConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleAdminChatSegConfigGet(w, r)
	case http.MethodPost:
		s.handleAdminChatSegConfigSave(w, r)
	default:
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不允许"))
	}
}

// chatSegCfgEffective 现读两档**生效值**（走 chatSegTiers 那唯一读点）＋库内原值＋代码缺省＋合法区间。
// GET 与 POST 后回显共用这一个函数——否则"保存成功却回显旧值"只能靠人眼发现
// （§一·3 那条「改完即生效要真改第二次验」）。
func (s *Server) chatSegCfgEffective() map[string]interface{} {
	on, maxPts := s.chatSegTiers()
	live := map[string]int{"per_paragraph": 0, "max_points": int(maxPts)}
	if on {
		live["per_paragraph"] = 1
	}
	eff := map[string]interface{}{}
	stored := map[string]string{}
	defs := map[string]int{}
	ranges := map[string][2]int{}
	for _, f := range chatSegCfgFields {
		raw := ""
		if s.Store != nil {
			raw, _ = s.Store.GetConfig(f.key)
		}
		stored[f.key] = raw
		eff[f.jsonField] = live[f.jsonField]
		defs[f.jsonField] = f.def
		ranges[f.jsonField] = [2]int{f.lo, f.hi}
	}
	return map[string]interface{}{
		"success":  true,
		"tiers":    eff,    // 生效值（读侧钳位/AllowZero 之后真正在用的那一档）
		"stored":   stored, // 库内原值（与生效值不同 ⇒ 界面标脏，运营看得见"我存的是什么"）
		"defaults": defs,
		"ranges":   ranges,
		// zero_semantics 是**对外契约**（写进内置文档前先想清楚）：这一族 0 是真实档位。
		// 出栈它，是为了让"照抄那族 0 拒收"的改法在测试里当场对不上，而不是靠注释。
		"zero_semantics": "zero_is_a_real_tier",
	}
}

// handleAdminChatSegConfigGet 回显两档。鉴权与保存口同一档（平台级档位，读面也不给租户管理员看）。
func (s *Server) handleAdminChatSegConfigGet(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireChatSegAdmin(w, r); err != nil {
		return
	}
	writeJSON(w, 200, s.chatSegCfgEffective())
}

// handleAdminChatSegConfigSave 校验先行、两项都过才整批落库（半套参数＝一条腿分段一条腿整篇）。
func (s *Server) handleAdminChatSegConfigSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireChatSegAdmin(w, r)
	if err != nil {
		return
	}
	// 指针字段：nil＝没提交这一项（与"提交了 0"必须可区分——本族 0 是合法档，
	// 用值判"填没填"会把"关掉分段"读成"缺参数"）。
	// ⚠️ `submitted` 必须在 **Decode 之后**建：map 存的是指针**值**，Decode 给字段赋的是新指针，
	//    先建表会把三枚 nil 抄进去 ⇒ 每个请求都判"缺少 xxx"，接口按不下去（首跑真踩形态，留此注释防回退）。
	var req struct {
		PerParagraph *int `json:"per_paragraph"`
		MaxPoints    *int `json:"max_points"`
		Reset        bool `json:"reset"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation,
			`请求格式错误：需为 {"per_paragraph":1,"max_points":60}（0 是合法档位）`))
		return
	}
	submitted := map[string]*int{"per_paragraph": req.PerParagraph, "max_points": req.MaxPoints}
	// —— 恢复缺省：写成空串，读侧遇空回代码缺省 ⇒ 与"从未配过"完全同形，
	//    不留 0 这种"把开关按下又被当成没表态"的中间态。
	if req.Reset {
		for _, f := range chatSegCfgFields {
			if err := s.Store.SetConfig(f.key, ""); err != nil {
				s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "恢复缺省失败："+err.Error()))
				return
			}
		}
		s.Store.LogAudit(s.effTenant(r, u), u.ID, "chat_seg_config_reset", "system_config",
			"即时翻译分段两档已清空，回落代码缺省 per_paragraph=1 max_points="+strconv.Itoa(engine.ChatSegmentMaxPtsDefault))
		writeJSON(w, 200, s.chatSegCfgEffective())
		return
	}
	vals := map[string]int{}
	for _, f := range chatSegCfgFields {
		p := submitted[f.jsonField]
		if p == nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation,
				"两条档位必须一次提交，缺少 "+f.jsonField))
			return
		}
		v := *p
		if v < f.lo || v > f.hi {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation,
				f.jsonField+" 必须落在 "+strconv.Itoa(f.lo)+" ~ "+strconv.Itoa(f.hi)+
					"（0 是合法档位：per_paragraph=0＝走整篇，max_points=0＝不设积分上限）"))
			return
		}
		vals[f.key] = v
	}
	for _, f := range chatSegCfgFields {
		if err := s.Store.SetConfig(f.key, strconv.Itoa(vals[f.key])); err != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "保存失败："+err.Error()))
			return
		}
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "chat_seg_config_save", "system_config",
		"即时翻译分段档位已保存 per_paragraph="+strconv.Itoa(vals[engine.CfgKeyChatPerParagraph])+
			" max_points="+strconv.Itoa(vals[engine.CfgKeyChatSegmentMaxPts]))
	writeJSON(w, 200, s.chatSegCfgEffective())
}

// requireChatSegAdmin 平台级档位鉴权：只走 requireAdminUser 这一道共享闸（与 est-tokens／text-lines 同档）。
// 为什么不给租户管理员：这两条改的是**所有租户每一单**即时翻译的执行形态与成本上限——
// 给租户配等于"A 租户能把 B 租户的流式形态改掉"，方向与租户隔离相反。
// ⚠️ 这里刻意**不写**第二条 `!auth.IsSuperAdmin(u)` 自造 403：text_line_config.go 的注释记过那次归因，
//
//	requireAdminUser 里的 iam.RequireRole(u,4) 已经带了"等级够但挂在具体租户同样拒"那一腿，
//	第二条分支永不成立＝给走不到的路径写判据（§一·8）。"只有平台超管能配"由那一道共享闸保证。
func (s *Server) requireChatSegAdmin(w http.ResponseWriter, r *http.Request) (*store.User, error) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		s.writeAuthzError(w, r, err)
		return nil, err
	}
	return u, nil
}
