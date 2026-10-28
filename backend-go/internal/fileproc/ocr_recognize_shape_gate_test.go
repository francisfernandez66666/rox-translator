// ============ 本文件职责中文说明 ============
// 识别脚本 `ocr_recognize.py` 的**框粒度形状锁**（★ 2026-10-28 图片版式回退批，方案 §5 断言 A1）。
//
// 为什么必须有这条锁：今天凌晨的质量链批加过「同行合并」（`_unclip_merge`/`_merge_fragments`）与
// 「960→1600 双档重跑」，同一张 2408px 界面截图的前后对照实证它们是**回归源**——
// 版式还原完全以「框」为输入（每框独立按原墨迹高定字号），多元素被并进单框 ⇒ 字号压扁、
// 各框合并程度不一 ⇒ 用户报的「字体大大小小、版式完全不还原」。摘掉容易，**长回来**才是问题：
// 下一位为了治某个错字再把合并写回来，界面上只会看到「图又花了」，而 diff 里是一行看起来无害的阈值。
//
// 四条腿（判据函数两侧各喂一次真件与变异件，证明它不是空转）：
//
//	N1 负锁：合并/双档那五个名字在**代码行**里零命中（注释里写它们属于记录事故，允许，所以先剥整行注释）；
//	N2 正锁：保留件必须还在（`_det_boxes`/`_order_boxes`/`_crop_with_pad`/`DET_BOX_THRESH`/
//	     `REC_CROP_PAD_RATIO`/`CLS_THRESH`）——它们治的正是用户认过的旧错字（图标读成「の」、
//	     末尾竖笔被切成「manua」），摘掉它们＝把旧缺陷放回来；
//	N3 契约腿：`_image_result` 的出栈体必须含 text/boxes/confs/confidence/skipped_pages 五键
//	     （Go 清洗层按 confs 分档、按 skipped_pages 出声）；
//	N4 射程自证：把 `_merge_fragments` 写回一份**临时副本**必须让 N1 判红、
//	     把 `_det_boxes` 从副本里删掉必须让 N2 判红——两枚变异都不碰仓库真件（AGENTS §三 那条纪律）。
//
// ============================================
package fileproc

import (
	"os"
	"strings"
	"testing"
)

// ocrShapeFile 被锁的识别脚本（与本测试同目录，随 sync_fileproc_assets.sh 落部署机）。
const ocrShapeFile = "ocr_recognize.py"

// ocrShapeNegative 框粒度回归源：合并与双档那几件的名字，代码行里一个都不许出现。
var ocrShapeNegative = []string{
	"_unclip_merge",
	"_merge_fragments",
	"DET_MAX_SIDE_HIGH",
	"DET_MIN_LINE_H",
	"FRAG_GAP_RATIO",
	"auto_side",
}

// ocrShapePositive 保留件：治旧错字的几件与逐元素框的骨架，缺任何一件即红。
var ocrShapePositive = []string{
	"def _det_boxes",
	"def _order_boxes",
	"def _crop_with_pad",
	"DET_UNCLIP_RATIO",
	"DET_BOX_THRESH",
	"REC_CROP_PAD_RATIO",
	"CLS_THRESH",
}

// ocrShapeContractKeys `_image_result` 出栈体的必备键（Go 侧 parseOcrJSON 的输入契约）。
var ocrShapeContractKeys = []string{`"text"`, `"boxes"`, `"confs"`, `"confidence"`, `"skipped_pages"`}

// ocrShapeStripComments 剥掉**整行注释**（Python 的 `#` 注释），只留代码与 docstring。
// 判负锁必须先剥注释：摘掉合并这件事本身要写在注释里交代原因（本仓纪律：为什么不许写回来），
// 不剥注释＝把自己的说明文字判成缺陷（AGENTS §一·5 那族「注释冒充实现」的老坑）。
func ocrShapeStripComments(src string) string {
	out := make([]string, 0, 64)
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// ocrShapeJudge 出两类读数：负锁命中项、正锁缺失项、契约缺失键。
// 纯函数、吃字符串——N4 的两枚变异喂副本进来即可复用同一把尺子（判据级与守卫级同一实现）。
func ocrShapeJudge(code string) (negHits, posMissing, contractMissing []string) {
	for _, n := range ocrShapeNegative {
		if strings.Contains(code, n) {
			negHits = append(negHits, n)
		}
	}
	for _, p := range ocrShapePositive {
		if !strings.Contains(code, p) {
			posMissing = append(posMissing, p)
		}
	}
	// 契约腿只在 `_image_result` 函数体范围内数键（整文件数会被别的字典字面量喂饱）。
	i := strings.Index(code, "def _image_result")
	if i < 0 {
		return negHits, append(posMissing, "def _image_result"), contractMissing
	}
	body := code[i:min(len(code), i+900)]
	for _, k := range ocrShapeContractKeys {
		if !strings.Contains(body, k) {
			contractMissing = append(contractMissing, k)
		}
	}
	return negHits, posMissing, contractMissing
}

// readOcrShapeFile 读识别脚本真件（测试与仓库同源，不拿部署机副本当判据对象）。
func readOcrShapeFile(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(ocrShapeFile)
	if err != nil {
		t.Fatalf("读不到 %s（被移动或删除？这条锁的射程就没了）: %v", ocrShapeFile, err)
	}
	return string(b)
}

func TestOcrRecognizeBoxesStayPerElement(t *testing.T) {
	src := readOcrShapeFile(t)
	neg, posMissing, contract := ocrShapeJudge(ocrShapeStripComments(src))
	if len(neg) > 0 {
		t.Fatalf("合并或双档残留（框粒度回归源被写回来了）：%s", strings.Join(neg, ", "))
	}
	if len(posMissing) > 0 {
		t.Fatalf("保留件缺失（治旧错字的件被摘走）：%s", strings.Join(posMissing, ", "))
	}
	if len(contract) > 0 {
		t.Fatalf("输出契约缺键（Go 清洗层读不到这一维）：%s", strings.Join(contract, ", "))
	}
}

// TestOcrRecognizeShapeMutationsBreakTheirLegs 射程自证：两条负向腿各自喂一枚变异副本必须判红。
// 摘掉这两枚，上面那条绿灯就分不清「框真的逐元素」与「锁压根没在看文件」。
func TestOcrRecognizeShapeMutationsBreakTheirLegs(t *testing.T) {
	src := readOcrShapeFile(t)
	code := ocrShapeStripComments(src)

	// 变异 1：把合并函数写回代码行 ⇒ 负锁必须抓到 `_merge_fragments`。
	mutatedMerge := strings.Replace(code, "def _det_boxes",
		"def _merge_fragments(boxes):\n    return boxes\n\n\ndef _det_boxes", 1)
	neg1, _, _ := ocrShapeJudge(mutatedMerge)
	if len(neg1) == 0 {
		t.Fatalf("反证 1 失效：写回 `_merge_fragments` 负锁仍判绿（锁已失明）")
	}

	// 变异 2：删掉 `_det_boxes` 定义 ⇒ 正锁必须点名缺失。
	// ⚠️ 替换名**不许含 "def _det_boxes" 子串**（AGENTS §一·5 那族「子串判据对旧值恒真」）：
	// 写成 `_det_boxes_renamed` 会让这条反证永远抓不到东西，把"锁失明"读成"锁有射程"。
	mutatedDrop := strings.Replace(code, "def _det_boxes", "def detect_boxes_removed", 1)
	_, pos2, _ := ocrShapeJudge(mutatedDrop)
	if len(pos2) == 0 {
		t.Fatalf("反证 2 失效：摘掉 `_det_boxes` 正锁仍判绿（正锁是空转）")
	}

	// 变异 3：契约里删掉 `confs` 键 ⇒ 契约腿必须点名（清洗层拿不到逐行置信度＝整篇被判 uncertain）
	mutatedContract := strings.Replace(code, `"confs": confs,`, "", 1)
	_, _, contract3 := ocrShapeJudge(mutatedContract)
	if len(contract3) == 0 {
		t.Fatalf("反证 3 失效：删掉 confs 契约腿仍判绿")
	}
}
