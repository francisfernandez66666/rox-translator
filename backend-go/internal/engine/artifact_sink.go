// ============ artifact_sink.go · 职责说明 ============
// 交付产物事件（★ 2026-10-30 三事合一批 · 组5「工单交付两段式（md 先行）」）。
//
// 为什么用 ctx 上的 sink 而不是给 HandleFile 加参数：
//   - HandleFile 有 4 个生产调用点（api/stream.go 两处、service/ticket.go 两处）外加测试夹具；
//     加参数要动全部，而且下一次新增调用点还得"记得再传一遍"——漏传的后果是静默少一半产物。
//   - 与既有 WithProgressCallback 同族：不订阅的调用方（即时翻译链）拿到的是空函数，
//     事件层零开销、行为与改造前一致。
//
// ⚠️ 但 md 先行本身是**真实的写入顺序变更**（产物集合不变、顺序变了），它不能靠"零订阅"兜底：
// 顺序由 file.go 的写回分段保证、由 file_md_first_test.go 的事件序列锁住。
//
// 两条 kind 只有两档（AGENTS §一·14 / 方案 §5.1，别在各调用方自造第三档）：
//   - md       ：纯文案译文（预览 / 下载 md）；
//   - original ：该语种主产物（回写件 / 译后图 / 译后配音）。
//
// ========================================
package engine

import "context"

// 产物 kind 的两档值（与 store.TicketArtifactKind* 逐字同源；engine 不 import store）。
const (
	ArtifactKindMD       = "md"
	ArtifactKindOriginal = "original"
)

// Artifact 一件交付产物的事件载荷。
// 字段口径：
//   - FilePath：源上传件绝对路径（多文件工单据此把事件归到 ticket_files 的那一行）；
//   - Lang：目标语言；Kind：md|original（见上）；
//   - Path：产物磁盘绝对路径（**下载端点只从库里取这个值**，绝不接受客户端传路径）；
//   - Primary：「该语言『下载原格式』的目标件」——只有 original 会置真；
//     唯一例外是「md 与 original 指向同一文件」那一档（.md 输入件），由服务层收敛时置真；
//   - Degraded：降级交付（原格式没生成出来，只剩 md；音频/图片是"能力档"也走这一档表达）。
type Artifact struct {
	FilePath string
	Lang     string
	Kind     string
	Path     string
	Primary  bool
	Degraded bool
}

// artifactSinkKey 产物事件回调的 context 键（私有类型防跨包碰撞）。
type artifactSinkKey struct{}

// WithArtifactSink 注入产物事件回调。fn 为 nil 时原样返回（等同未订阅）。
func WithArtifactSink(ctx context.Context, fn func(Artifact)) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, artifactSinkKey{}, fn)
}

// artifactSinkFrom 读取产物事件回调；未注入返回空函数（调用方无需判空）。
func artifactSinkFrom(ctx context.Context) func(Artifact) {
	if fn, ok := ctx.Value(artifactSinkKey{}).(func(Artifact)); ok && fn != nil {
		return fn
	}
	return func(Artifact) {}
}

// artifactStage 一次 HandleFile 的产物事件发射器（绑定源上传件，负责去重）。
// 为什么需要这一类而不是直接调 sink：
//   - **同路径去重**：md 可能在「md 先行」段写一次、末尾 textOut 收集段又想发一次，
//     重复发会让 UI 出两行（B10/B6 的判据）；
//   - **同键去重但允许降级改写**：写回失败时要把已发的 md 标成 degraded（同一 (lang,kind,path)
//     再发一次、只有 degraded 变了）——服务层按唯一键 upsert，所以这是"更新一行"而不是"多一行"。
//     完全相同的第二次事件则直接丢弃。
type artifactStage struct {
	filePath string
	emit     func(Artifact)
	sent     map[string]bool
}

// newArtifactStage 构造发射器（sink 缺省为空函数，调用点无需判空）。
func newArtifactStage(ctx context.Context, filePath string) *artifactStage {
	return &artifactStage{filePath: filePath, emit: artifactSinkFrom(ctx), sent: map[string]bool{}}
}

// send 发一条产物事件；重复（同 kind/lang/path/primary/degraded）直接丢弃。
// 返回：这一条是否真的发了出去（便于单测/排障，不参与业务判定）。
func (st *artifactStage) emitOne(kind, lang, path string, primary, degraded bool) bool {
	if st == nil || path == "" {
		return false
	}
	key := kind + "\x00" + lang + "\x00" + path
	if primary {
		key += "\x00p"
	}
	if degraded {
		key += "\x00d"
	}
	if st.sent[key] {
		return false
	}
	st.sent[key] = true
	st.emit(Artifact{FilePath: st.filePath, Lang: lang, Kind: kind, Path: path, Primary: primary, Degraded: degraded})
	return true
}
