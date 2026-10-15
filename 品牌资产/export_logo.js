// 从项目源 logo.svg 导出多规格 PNG（白底版 / 深色版 / 透明底版 / X 头像版）
const fs = require('fs');
const path = require('path');
const sharp = require('sharp');

const SRC = '/Users/zhangzifei/Desktop/翻译助手/frontend-react/public/logo.svg';
const OUT = '/Users/zhangzifei/Desktop/翻译助手/品牌资产';

const svgRaw = fs.readFileSync(SRC, 'utf8');

// 原样：白底 + 黑图形
const svgLight = svgRaw;
// 深色版：黑底 + 白图形（图形第二条 opacity .45 提到 .55 保证深色底可见）
const svgDark = svgRaw
  .replace('fill="#FFFFFF"', 'fill="#000000"')
  .replace(/stroke="#000"/g, 'stroke="#FFFFFF"')
  .replace('opacity=".45"', 'opacity=".55"');
// 透明底版：去掉底板，只留黑图形
const svgTransparent = svgRaw.replace(/<rect[^>]*\/>/, '');

async function render(svg, size, file, bg) {
  const buf = Buffer.from(svg);
  let img = sharp(buf, { density: 384 }).resize(size, size, {
    fit: 'contain',
    kernel: 'lanczos3',
  });
  if (bg) img = img.flatten({ background: bg });
  await img.png({ compressionLevel: 9 }).toFile(path.join(OUT, file));
  const st = fs.statSync(path.join(OUT, file));
  console.log(`${file}  ${size}x${size}  ${(st.size / 1024).toFixed(1)}KB`);
}

(async () => {
  fs.copyFileSync(SRC, path.join(OUT, 'logo.svg'));
  console.log('logo.svg  (源文件副本)');

  // 通用大图
  await render(svgLight, 1024, 'logo-1024.png', '#FFFFFF');
  await render(svgDark, 1024, 'logo-dark-1024.png', '#000000');
  await render(svgTransparent, 1024, 'logo-transparent-1024.png', null);
  // X 头像规格（X 显示 400x400）
  await render(svgLight, 400, 'logo-avatar-400.png', '#FFFFFF');
  await render(svgDark, 400, 'logo-avatar-dark-400.png', '#000000');
  // 站点 favicon
  await render(svgLight, 64, 'logo-64.png', '#FFFFFF');
})().catch((e) => {
  console.error('FAIL', e);
  process.exit(1);
});
