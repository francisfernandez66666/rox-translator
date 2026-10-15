// scripts/xbot/_rasterize.js — SVG → PNG 光栅化（sharp / librsvg）
// 用法：node _rasterize.js <in.svg> <out.png> [targetWidth]
//
// ★★ 为什么要传 targetWidth（2026-10-13 实测踩到）：
//   sharp 的 density 默认 72，这里原本硬编码 384，而 librsvg 会按 density/72
//   放大输出尺寸 ⇒ 声明 1200×675 的图实际出成 6400×3600（正好 5.33 倍）。
//   后果：Instagram 单图 30MB、TikTok 20MB 的上限被静默突破，
//   而且**不报任何错**，文件还"变大了"所以体积检查也照样通过。
//   ⇒ 高 density 只用于获得清晰字形，最后必须 resize 回到目标尺寸。
const fs = require('fs');
const sharp = require('sharp');

const [, , inPath, outPath, targetWidth] = process.argv;
if (!inPath || !outPath) {
  console.error('用法: node _rasterize.js <in.svg> <out.png> [targetWidth]');
  process.exit(2);
}
const svg = fs.readFileSync(inPath);

let pipeline = sharp(svg, { density: 384 }).png({ compressionLevel: 9 });
if (targetWidth) {
  const w = parseInt(targetWidth, 10);
  if (!Number.isFinite(w) || w <= 0) {
    console.error('targetWidth 非法: ' + targetWidth);
    process.exit(2);
  }
  pipeline = pipeline.resize({ width: w });
}

pipeline
  .toFile(outPath)
  .then((info) => {
    // 自证：把实际尺寸打到 stdout，由 Python 侧核对。静默出错是最难查的一类。
    console.log(`${info.width}x${info.height}`);
    process.exit(0);
  })
  .catch((e) => {
    console.error('sharp 渲染失败:', e.message);
    process.exit(1);
  });