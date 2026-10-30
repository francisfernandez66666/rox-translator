// ============ formats.go · 职责说明 ============
// 翻译文件「准入格式」与「体积上限」的唯一事实源（★ 2026-10-14 用户令 #43：判据形状收成一把尺子）。
//
// 收拢前这份清单在仓里有**四份副本**，每份都带一句"与 xxx 一致"的注释：
//
//	① internal/api/upload.go        `translateExtWhitelist`（即时翻译上传、SSE 两条腿都读它）
//	② internal/api/tickets.go       函数体内联的 `allowed`（内部文件工单建单）
//	③ internal/api/api_openapi_tasks.go `openAPITaskExtWhitelist`（OpenAPI 文件批量建单）
//	④ internal/engine/file.go       函数体内联的 `allowedExt`（工单执行时的第二次格式判定）
//
// 四份的十二项逐字相同，所以现网没有症状——这正是要趁没症状收掉的那一族：
// 「一致」是靠注释维持的，而注释不会红灯。下一次给 .epub 开还原模式准入的人只会改其中一份，
// 表现是**同一份文件在上传口能建、在执行口被判不支持**（工单建起来了、跑一半失败），
// 比四处各改各的更坏的是它只坏在客户已经付了钱的那一步。历史同族事故：F-12 三口径打架、
// §一·11 三把尺子、§一·3④ 预估与实扣——形态都是"同一件事多处各算一遍"。
//
// 三条设计取向（改动前必读）：
// ① **清单只在这里长牙**：本文件是「哪些文件能建单」的唯一判据。
//
//	＝internal/api/upload_formats_gate_test.go L2 全表比对）。★ 2026-10-30 三事合一批是**第一次真的
//	改对外结论**（撤出 .srt/.vtt/.json/.yaml/.yml 五档，27 → 22），所以那句"一个字没变"已成历史——
//	改这一处＝改对外承诺：必须同批改 upload_formats_gate_test 的冻结句子、前端 translate.ts 与
//	12 语种 `tk.fileHint`、开放 API 三面文档，并在发版说明里点名；**只改一处就是
//	「上传口放行、执行口判不支持」的下一次**（本头注开头那一族）。
//	对外承诺的体积数字（OpenAPI 文档写 30MB、实际闸门 40MiB）不许改成互相靠拢——那是对外口径决策，
//	只把它的**方向**收成一条机械判据：对外承诺 ≤ 实际闸门（承诺不得高于守得住的那条线）。
//
// ② **交付方式（delivery）的字符串不进本包**：调用方自己判 `delivery == "text"`，
//
//	这里只收一个 bool。fileproc 不知道"还原/纯文案"是 HTTP 参数，
//	否则将来第三种交付方式要在这一层再抄一遍枚举。
//
// ③ **只读、不改**：`TranslateRestoreFormats` 由清单派生一次、全程只读。
//
//	旧②那份写法是"取本地 map 再往里塞 anydoc 键"，因为它每次都是新建的字面量才没出事；
//	收成共享常量后**同一段代码搬过来就是往全局里写**（纯文案模式一跑，
//	还原模式的闸门当场被抬起，且没有任何一处会报错）。所以这里给的是**判定函数**，
//	根本不提供可改的共享 map 给调用方拼。
//
// =============================================
package fileproc

import "strings"

// MaxUploadBytes 单个上传请求的体积闸门（40 MiB）。
//
// 唯一事实源（★ #43）：原先 internal/api 里有三份同值字面量
// （translateUploadMax / pdfUploadSizeLimit / openAPITaskMaxBytes），
// Caddy 那层还有第三处（`request_body max_size 40MB`，两份线上 conf ＋ scripts/bootstrap-demo.sh）。
// Go 侧改这一档必须同时动 Caddy，否则表现是"后端还没拒绝，反代先把请求掐了"，
// 而客户看到的是一句没有中文文案的 413/502——这一条由
// upload_formats_gate_test.go L4 现读三层做等值锁（跨层判据只能现读，写死数字第二天就过时）。
const MaxUploadBytes int64 = 40 << 20

// OpenAPIDocLimitMB 是 OpenAPI 文档面对外承诺的单请求总量（30MB）。
//
// ⚠️ 它**不是**闸门，只是文案里那一个数；真正的墙是 MaxUploadBytes。
// 本批不许动它的数值（改它就是改对外承诺，属用户决策），只锁方向：
// 对外承诺不得高于实际闸门（L5 判据）。要动就两个一起动、并按 §一·5 那条走发版——
// 先抬文档后抬闸门的那一段里，客户按文档的承诺上传，会被真实闸门拒在一个没有中文文案的位置上。
const OpenAPIDocLimitMB = 30

// TranslateFormatOrder 还原文件模式（delivery=restore，默认档）准入的扩展名，**顺序即出栈文案顺序**。
//
// 清单本身就是旧四份副本的并集（四份逐字相同，所以并集＝任一份）。
// 刻意写成有序切片而不是只留 map：出栈文案那句「docx/xlsx/pptx/pdf/…」必须与它同源，
// 而 map 遍历顺序在 Go 里是随机的——拿 map 拼文案会让同一句错误提示每次不一样。
//
// ★ 2026-10-15 P1a/P2 扩展：追加图片格式（.png/.jpg/.jpeg/.bmp/.tif/.tiff/.webp/.heic）
// 与音频格式（.mp3/.m4a/.wav/.ogg/.flac/.aac/.opus）。
// 图片经 OCR 提取文字、音频经 ASR 转写文字后投喂翻译引擎（上游适配器定位，见《改造方案_OCR与ASR接入》），
// 交付形态：图片→同容器译后图＋译文 .md、音频→同容器译后配音＋译文 .md（**两件**，AGENTS §一·14 本批改档）。
// 准入即代表「本系统接收该格式建单」；OCR/ASR 不可用（OcrAvailable()/AsrAvailable()==false）时
// 提取层返回友好错误，不把整单判死（译文第一：识别/转写为空才失败）。
// ★ 2026-10-30 三事合一批：`.srt/.vtt/.json/.yaml/.yml` **撤出准入**（两种交付模式都撤，27 → 22 项）。
// 这五个都是纯文本容器，客户要翻的从来是里面的文字，走文件导入只多付一遍落盘/识别/写回的成本；
// 正确路径是直接粘贴到文本翻译，所以拒绝时**必须给这条出路**（见下面 RejectHint），不许只回一句「不支持」。
// ⚠️ 这一族是**对外准入决策**：upload_formats_gate_test.go 的 restore12Frozen 已同步为 22 项，
// 四处冻结句子与 FormatHintBase 断言一并更新——改这份清单必须先动那条锁（逼出显式决策）。
// ⚠️ translateFormats.lock.test.ts 的 P0 下限（还原 <12）**不用跟着改**：它是"比清单短一大截"的射程探测器，
// 不是项数的镜像；把下限抬到贴近项数，下一次合法撤除就会把闸门自己判成"锚点漂了"（AGENTS §一·14）。
var TranslateFormatOrder = []string{
	".docx", ".xlsx", ".pptx", ".pdf", // .pdf 有原位回写（原版式覆盖→版式重建→纯文案三级链），交付形态判据见 engine.writebackDelivery
	".txt", ".csv",
	".md",
	".png", ".jpg", ".jpeg", ".bmp", ".tif", ".tiff", ".webp", ".heic", // P1a：图片格式，经 OCR 投喂翻译引擎
	".mp3", ".m4a", ".wav", ".ogg", ".flac", ".aac", ".opus", // P2：音频格式，经 ASR 转写投喂翻译引擎
}

// retiredTextContainers 是 2026-10-30 撤出准入的那五个纯文本容器。
// ⚠️ 它们**不在** TranslateFormatOrder 里——这份名单只为把「为什么被拒」说清楚而存在：
// 判据与出路必须同处，否则将来有人把 .srt 加回准入时，拒绝分支会剩下一句永远不该再出现的文案。
var retiredTextContainers = map[string]bool{
	".srt": true, ".vtt": true, ".json": true, ".yaml": true, ".yml": true,
}

// PlainTextContainerHint 撤除格式的出路句（**对外文案唯一事实源**，前端校验与后端三口逐字一致）。
const PlainTextContainerHint = "这一类是纯文本容器，请直接粘贴内容到文本翻译"

// IsRetiredTextContainer 判一个扩展名是否属于「本批撤出的纯文本容器」（用于给可行动出路，不是判准入）。
// 参数 ext: 已小写归一的扩展名（含点）。
func IsRetiredTextContainer(ext string) bool {
	return retiredTextContainers[strings.ToLower(strings.TrimSpace(ext))]
}

// RejectHint 组装一句完整的「不支持」提示（建单三口的唯一出口）。
// 参数 ext: 已小写归一的扩展名（含点）；textMode: 是否纯文案模式；
// lead: 各调用点自己的前导措辞（形如 `不支持的格式: a.doc` 或 `不支持的格式`）。
// 三个分支：撤除的五个容器 ⇒ 出路句；纯文案模式 ⇒ 清单＋anydoc 样本；其余 ⇒ 清单句。
// 清单两段都**现算**（FormatHintBase/FormatHintText），所以改档位这句话自己跟着变，
// 不会再长出「代码改了、提示还在承诺旧格式」的第二形态（AGENTS §一·14）。
func RejectHint(ext string, textMode bool, lead string) string {
	if IsRetiredTextContainer(ext) {
		return lead + "（" + PlainTextContainerHint + "）"
	}
	if textMode {
		return lead + "（纯文案模式支持 " + FormatHintBase() + " 及 " + FormatHintText() + "）"
	}
	return lead + "（仅支持 " + FormatHintBase() + "）"
}

// AnydocHintSample 纯文案模式（delivery=text）额外准入的那一族的**展示样本**。
//
// ⚠️ 它是样本而不是全集：AnydocFormats 还有 docm/pps/pot/pptm/ppsx/ppsm/xlsm/xlsb，
// 出栈文案只举最常见的八个（沿用旧措辞，本批不改对外句子）。
// 这条一样本必须由判据保证"举出来的每一个都真在准入名单里"
// （L3 反向锁：样本里出现一个 AnydocFormats 没有的名字＝文案在承诺一个收不进来的格式）。
const AnydocHintSample = "doc/xls/ppt/odt/ods/odp/rtf/epub"

// translateFormats 由 TranslateFormatOrder 派生（不写第二份字面量）。
// 包内私有：外部一律走 IsTranslateFormat，避免拿到可写的共享 map（见头注③）。
var translateFormats = func() map[string]bool {
	m := make(map[string]bool, len(TranslateFormatOrder))
	for _, e := range TranslateFormatOrder {
		m[e] = true
	}
	return m
}()

// imageFormatSet 图片格式集合（P1a，经 OCR 投喂翻译引擎），与 TranslateFormatOrder 尾部图片项**逐字同源**。
// 单独成 set 是为了让 IsImageFormat / 交付形态判定 / image_heavy 等调用点不必再抄一份扩展名列表。
// ⚠️ 与 TranslateFormatOrder 尾部图片项必须完全一致：本文件 capabilities 同步锁（grep 不直观，
// 由 upload_formats_gate_test.go 的 restore12Frozen 与 L2 行为表一并守）。
var imageFormatSet = func() map[string]bool {
	s := map[string]bool{}
	for _, e := range []string{".png", ".jpg", ".jpeg", ".bmp", ".tif", ".tiff", ".webp", ".heic"} {
		s[e] = true
	}
	return s
}()

// IsImageFormat 判扩展名是否为图片格式（P1a 经 OCR 投喂翻译引擎）。
func IsImageFormat(ext string) bool {
	return imageFormatSet[ext]
}

// audioFormatSet 音频格式集合（P2，经 ASR 转写投喂翻译引擎），与 TranslateFormatOrder 尾部音频项**逐字同源**。
// 单独成 set 是为了让 IsAudioFormat / 交付形态判定等调用点不必再抄一份扩展名列表。
var audioFormatSet = func() map[string]bool {
	s := map[string]bool{}
	for _, e := range []string{".mp3", ".m4a", ".wav", ".ogg", ".flac", ".aac", ".opus"} {
		s[e] = true
	}
	return s
}()

// IsAudioFormat 判扩展名是否为音频格式（P2 经 ASR 转写投喂翻译引擎）。
func IsAudioFormat(ext string) bool {
	return audioFormatSet[ext]
}

// IsTranslateFormat 判一个扩展名在该交付方式下是否准入。
// 参数 ext: **已小写归一**的扩展名（含点，如 ".docx"；归一由调用方做，
// 因为各调用点手上是 filepath.Ext(hdr.Filename)，大小写与点都在那儿现成）；
// textMode: 是否纯文案模式（delivery=text），true 时额外放行 anydoc 独占格式。
// 返回: 准入为 true。
func IsTranslateFormat(ext string, textMode bool) bool {
	if translateFormats[ext] {
		return true
	}
	return textMode && AnydocFormats[ext]
}

// TranslateRestoreFormatsCopy 给「接口签名要求一个 map」的调用方用（现下只有 parseUpload），
// 每次**新建一份**返回——共享的那份绝不外借，见头注③：调用方一旦往里补键，
// 抬起来的是全站共享的闸门，而且一行错误都不会有。
func TranslateRestoreFormatsCopy() map[string]bool {
	m := make(map[string]bool, len(translateFormats))
	for k, v := range translateFormats {
		m[k] = v
	}
	return m
}

// FormatHintBase 出栈文案里的「支持的格式」清单（还原模式那一句的全部实质内容）。
// 返回形如 docx/xlsx/pptx/pdf/txt/csv/md/png/jpg/…。
//
// 一处归一（沿用旧措辞，不是本批新决策）：剥掉前导点（客户看的是文件后缀，不是代码里的键）。
// ⚠️ 旧写法里那条「.yml 不单列」的跳过已随 2026-10-30 的准入撤除一起退役——
// 清单里不再有 .yml，留着它就是把一段死逻辑当判据供着（AGENTS §一·14「注释/代码点名的锚点必须存在」）。
func FormatHintBase() string {
	out := make([]string, 0, len(TranslateFormatOrder))
	for _, e := range TranslateFormatOrder {
		out = append(out, e[1:])
	}
	joined := ""
	for i, e := range out {
		if i > 0 {
			joined += "/"
		}
		joined += e
	}
	return joined
}

// FormatHintText 纯文案模式那一句里「额外准入」部分的样本清单（与旧措辞逐字一致）。
// 返回 AnydocHintSample 原样（这一档单独成函数，是为了让"文案与名单同源"这件事
// 在调用点上只有一处写法，而不是各处再拼一遍字符串常量）。
func FormatHintText() string {
	return AnydocHintSample
}
