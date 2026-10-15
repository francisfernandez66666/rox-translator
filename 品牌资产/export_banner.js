// 生成 X 首推 banner 两个版本（1200x675，纯黑单色品牌体系）
// A 版：返工痛点；B 版：降本增效（算账视角）
const sharp = require('sharp');
const OUT = '/Users/zhangzifei/Desktop/翻译助手/品牌资产';

// logo 图形部分（取自源 logo.svg，白描边版）
const logoPaths = `
  <g transform="translate(60,187.5) scale(10)">
    <path d="M9.5 10.5v5.2a4.3 4.3 0 0 0 8.6 0V12" stroke="#FFFFFF" stroke-width="2.4" fill="none" stroke-linecap="round"/>
    <path d="M20.5 19.5v-5.2a4.3 4.3 0 0 0-8.6 0V18" stroke="#FFFFFF" stroke-width="2.4" fill="none" stroke-linecap="round" opacity=".5"/>
  </g>`;

// 文本宽度自适应：全角按 1.0em、半角按 0.55em 估算，保证不溢出画布
function fitSize(text, maxWidth, maxSize) {
  let em = 0;
  for (const ch of text) em += /[\u2E80-\uFFFF]/.test(ch) ? 1 : 0.55;
  return Math.min(maxSize, Math.floor(maxWidth / em));
}

const LEFT = 360;
const AVAIL = 1200 - LEFT - 72; // 右侧留 72px 边距

function svg(main, sub, sub2) {
  const fs1 = fitSize(main, AVAIL, 82);
  return `<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="675" viewBox="0 0 1200 675">
  <rect width="1200" height="675" fill="#000000"/>
  <rect x="1" y="1" width="1198" height="673" fill="none" stroke="#2A2F3A" stroke-width="2" rx="18"/>
  ${logoPaths}
  <text x="${LEFT}" y="290" font-family="'PingFang SC','Hiragino Sans GB',sans-serif" font-size="${fs1}" font-weight="700" fill="#FFFFFF">${main}</text>
  <text x="${LEFT}" y="372" font-family="'PingFang SC','Hiragino Sans GB',sans-serif" font-size="36" fill="#8B939F">${sub}</text>
  <text x="${LEFT}" y="436" font-family="'PingFang SC','Hiragino Sans GB',sans-serif" font-size="36" fill="#8B939F">${sub2}</text>
  <text x="${LEFT}" y="560" font-family="'PingFang SC','Hiragino Sans GB',sans-serif" font-size="26" fill="#E7E9EA">能言 LangCross</text>
  <text x="640" y="560" font-family="Menlo,monospace" font-size="26" fill="#6E7683">langcross.lexicorn.cn</text>
</svg>`;
}

const A = svg(
  '返工一次，这单白干',
  'Word / PPT / Excel / PDF 原版式直出',
  '术语按你的库强制统一，不对就拦',
);
const B = svg(
  '成本约为人工的 1/8',
  '交付周期：从「等三天」到「等三分钟」',
  '多语言并行，一次打包交付',
);

(async () => {
  for (const [name, s] of [['banner-x-1200x675.png', A], ['banner-x-alt-1200x675.png', B]]) {
    const i = await sharp(Buffer.from(s)).png({ compressionLevel: 9 }).toFile(`${OUT}/${name}`);
    console.log(name, i.width + 'x' + i.height, (i.size / 1024).toFixed(1) + 'KB');
  }
})().catch((e) => {
  console.error('FAIL', e);
  process.exit(1);
});