import type { SVGProps } from "react";

export type IconProps = SVGProps<SVGSVGElement> & { size?: number };

function base({ size = 16, ...rest }: IconProps): SVGProps<SVGSVGElement> {
  return {
    width: size,
    height: size,
    viewBox: "0 0 16 16",
    fill: "none",
    "aria-hidden": true,
    ...rest,
  };
}

/* ------------------------------------------------------------------
 * 基础件（交付包原有 7 个，保持不动）
 * ------------------------------------------------------------------ */

/** 对勾（描边 2 纯白 —— 与同行图标对齐光学重量） */
export function CheckIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M3.5 8.4L6.6 11.5L12.5 4.9"stroke="currentColor"strokeWidth={2} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 下载（16×16 / 描边 1.9：竖线 + 折箭头 + 托盘） */
export function DownloadIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M8 1.9v8"stroke="currentColor"strokeWidth={1.9} strokeLinecap="round"/>
      <path d="M4.7 7.2L8 10.5l3.3-3.3"stroke="currentColor"strokeWidth={1.9} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M2.6 11.3v1.3c0 .6.5 1.1 1.1 1.1h8.6c.6 0 1.1-.5 1.1-1.1v-1.3"stroke="currentColor"strokeWidth={1.9} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** Toast 成功（圆圈勾，白） */
export function ToastSuccessIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="8"cy="8"r="6.6"stroke="currentColor"strokeWidth={1.6} />
      <path d="M5.2 8.3L7.2 10.3L10.9 5.9"stroke="currentColor"strokeWidth={1.6} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** Toast 失败（圆圈叉，红） */
export function ToastErrorIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="8"cy="8"r="6.6"stroke="currentColor"strokeWidth={1.6} />
      <path d="M5.6 5.6L10.4 10.4M10.4 5.6L5.6 10.4"stroke="currentColor"strokeWidth={1.6} strokeLinecap="round"/>
    </svg>
  );
}

/** Toast 警告（圆圈叹号，琥珀） */
export function ToastWarnIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="8"cy="8"r="6.6"stroke="currentColor"strokeWidth={1.6} />
      <path d="M8 4.8v3.6"stroke="currentColor"strokeWidth={1.6} strokeLinecap="round"/>
      <circle cx="8"cy="11"r="0.9"fill="currentColor"/>
    </svg>
  );
}

/** 关闭 ×（12） */
export function CloseIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M2.5 2.5L9.5 9.5M9.5 2.5L2.5 9.5"stroke="currentColor"strokeWidth={1.6} strokeLinecap="round"/>
    </svg>
  );
}

/** 下拉箭头（12） */
export function CaretDownIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M2.5 4.5L6 8L9.5 4.5"stroke="currentColor"strokeWidth={1.6} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/* ------------------------------------------------------------------
 * 新增全套 16×16 图标（stroke 1.6–1.9 / currentColor）
 * 用途：替换 UI-ANNOTATIONS.md 切图里的 emoji 图标占位（§0.1）
 * ------------------------------------------------------------------ */

/** 品牌圆点（实心，logo mark） */
export function BrandDotIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="8"cy="8"r="4.4"fill="currentColor"/>
      <circle cx="8"cy="8"r="7.2"stroke="currentColor"strokeWidth={1.6} />
    </svg>
  );
}

/** 闪电（专业准确 / 快速模式） */
export function BoltIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M9.1 1.6L4.2 8.9h3.1l-.5 5.5 5.1-7.5H8.6l.5-5.3z"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 星芒（AI / 专业准确） */
export function SparkIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M8 1.6v3.2M8 11.2v3.2M1.6 8h3.2M11.2 8h3.2"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
      <path d="M8 4.4a3.6 3.6 0 013.6 3.6A3.6 3.6 0 018 11.6 3.6 3.6 0 014.4 8 3.6 3.6 0 018 4.4z"stroke="currentColor"strokeWidth={1.7} strokeLinejoin="round"/>
    </svg>
  );
}

/** 文档（原格式交付 / 文本对象） */
export function DocIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M9.2 1.7H4.4a1.2 1.2 0 00-1.2 1.2v10.2a1.2 1.2 0 001.2 1.2h7.2a1.2 1.2 0 001.2-1.2V5.1L9.2 1.7z"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M9.1 1.8v3.3h3.6"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 文本行（粘贴文本 tab） */
export function TextIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M2.6 3.6h10.8M2.6 7.4h10.8M2.6 11.2h6.6"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
    </svg>
  );
}

/** 仪表/积分计费 */
export function GaugeIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M2.2 12.4a6.6 6.6 0 1111.6 0"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
      <path d="M8 9.6l2.8-2.9"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
      <circle cx="8"cy="9.9"r="1.1"fill="currentColor"/>
    </svg>
  );
}

/** 柱状图（后台总览 / 增长漏斗） */
export function ChartIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M2.4 13.4h11.2"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
      <path d="M4.6 13.3V9.1M7.6 13.3V5.4M10.6 13.3V7.7M13.2 13.3V3.2"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
    </svg>
  );
}

/** 盾牌（内容合规审核） */
export function ShieldIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M8 1.8l5 1.9v4c0 3.1-2.1 5.4-5 6.5-2.9-1.1-5-3.4-5-6.5v-4l5-1.9z"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M5.8 7.9L7.4 9.6l2.9-3"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 锁（租户数据隔离 / 修改密码） */
export function LockIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <rect x="3.4"y="7.1"width="9.2"height="7.1"rx="1.6"stroke="currentColor"strokeWidth={1.7} />
      <path d="M5.7 7.1V5.2a2.3 2.3 0 014.6 0v1.9"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
    </svg>
  );
}

/** 包裹（版本级回滚） */
export function PackageIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M8 1.9l6 2.7v6.8L8 14.1l-6-2.7V4.6l6-2.7z"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M2.2 4.7L8 7.4l5.8-2.7M8 7.5v6.6"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 多人（人工极速通道 / 组织与成员） */
export function UsersIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="6.1"cy="5.6"r="2.5"stroke="currentColor"strokeWidth={1.7} />
      <path d="M1.9 13.2c0-2.3 1.9-4.1 4.2-4.1s4.2 1.8 4.2 4.1"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
      <path d="M10.9 3.6a2.5 2.5 0 010 4.8M11.4 9.5c1.6.5 2.7 1.9 2.7 3.7"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
    </svg>
  );
}

/** 单人（个人中心 / 用户菜单 / 个人用户 tab） */
export function UserIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="8"cy="5.4"r="2.9"stroke="currentColor"strokeWidth={1.7} />
      <path d="M2.6 13.6c0-2.9 2.4-5.2 5.4-5.2s5.4 2.3 5.4 5.2"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
    </svg>
  );
}

/** 楼宇（企业用户 tab / 组织） */
export function BuildingIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M2.6 13.8h10.8"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
      <path d="M4.1 13.7V3.9l5.2-1.6v11.4"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M9.3 6.6h2.9v7.1"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M6 6h1.5M6 8.7h1.5M11.2 9.4h.9"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
    </svg>
  );
}

/** 对话气泡（即时翻译 / 反馈审批 / 反馈操作） */
export function ChatIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M13.6 8.5c0 2.8-2.5 5.1-5.6 5.1-.7 0-1.4-.1-2-.3L2.4 14.4l.9-2.7c-1.1-.9-1.8-2.1-1.8-3.4 0-2.9 2.5-5.2 5.6-5.2s5.6 2.3 5.6 5.2z"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 列表/工单（翻译工单） */
export function ClipboardIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <rect x="3.4"y="2.6"width="9.2"height="11"rx="1.5"stroke="currentColor"strokeWidth={1.7} />
      <path d="M6.2 2.6V1.9h3.6v.7"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M5.8 6.6h4.4M5.8 9.4h4.4M5.8 12.1h2.8"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
    </svg>
  );
}

/** 铅笔（对照编辑） */
export function PencilIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M11.3 2.4l2.3 2.3-8.4 8.4-3.1.8.8-3.1 8.4-8.4z"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M10.2 3.5l2.3 2.3"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
    </svg>
  );
}

/** 书本（知识库） */
export function BookIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M2.7 3.3c2.1-.6 4.2-.6 5.3.4 1.1-1 3.2-1 5.3-.4v9.4c-2.1-.6-4.2-.6-5.3.4-1.1-1-3.2-1-5.3-.4V3.3z"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M8 3.7v9.4"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
    </svg>
  );
}

/** 铃铛（通知） */
export function BellIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M4.3 6.8a3.7 3.7 0 017.4 0c0 2.4.7 3.7 1.3 4.4H3c.6-.7 1.3-2 1.3-4.4z"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M6.6 12.8a1.6 1.6 0 002.8 0"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
    </svg>
  );
}

/** 主题切换（日/月） */
export function ThemeIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="8"cy="8"r="3.2"stroke="currentColor"strokeWidth={1.7} />
      <path d="M8 1.7v1.6M8 12.7v1.6M1.7 8h1.6M12.7 8h1.6M3.5 3.5l1.1 1.1M11.4 11.4l1.1 1.1M12.5 3.5l-1.1 1.1M4.6 11.4l-1.1 1.1"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
    </svg>
  );
}

/** 地球（语言 / 租户选择器） */
export function GlobeIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="8"cy="8"r="6.3"stroke="currentColor"strokeWidth={1.7} />
      <path d="M1.9 8h12.2"stroke="currentColor"strokeWidth={1.7} />
      <path d="M8 1.7c1.7 1.7 2.6 3.8 2.6 6.3S9.7 12.6 8 14.3C6.3 12.6 5.4 10.5 5.4 8S6.3 3.4 8 1.7z"stroke="currentColor"strokeWidth={1.7} />
    </svg>
  );
}

/** 上传（知识库） */
export function UploadIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M8 11.6V3.4"stroke="currentColor"strokeWidth={1.9} strokeLinecap="round"/>
      <path d="M4.7 6.6L8 3.3l3.3 3.3"stroke="currentColor"strokeWidth={1.9} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M2.6 12.6h10.8"stroke="currentColor"strokeWidth={1.9} strokeLinecap="round"/>
    </svg>
  );
}

/** 加号（新建 / 上传文件） */
export function PlusIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M8 3.1v9.8M3.1 8h9.8"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
    </svg>
  );
}

/** 发送 */
export function SendIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M14 2L7.3 8.7"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M14 2l-4.4 12-2.3-5.3L2 6.4 14 2z"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 刷新 */
export function RefreshIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M13.1 8a5.1 5.1 0 11-1.6-3.7"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M13.4 2.2v3.1h-3.1"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 垃圾桶（删除） */
export function TrashIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M2.9 4.6h10.2"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
      <path d="M6.3 4.6V2.9h3.4v1.7"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M4.2 4.6l.6 8.3c0 .7.6 1.2 1.3 1.2h3.8c.7 0 1.3-.5 1.3-1.2l.6-8.3"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 眼睛（查看详情 / 密码显隐） */
export function EyeIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M1.8 8S4.1 4 8 4s6.2 4 6.2 4-2.3 4-6.2 4S1.8 8 1.8 8z"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <circle cx="8"cy="8"r="1.9"stroke="currentColor"strokeWidth={1.7} />
    </svg>
  );
}

/** 眼睛关闭 */
export function EyeOffIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M4.4 4.9C2.7 6.1 1.8 8 1.8 8s2.3 4 6.2 4c1 0 1.9-.3 2.7-.7"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M13 10.6c.8-.9 1.2-2.6 1.2-2.6s-2.3-4-6.2-4c-.5 0-1 .1-1.4.2"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M2.6 2.6l10.8 10.8"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
    </svg>
  );
}

/** 链接（文件 tab / 外部调用） */
export function LinkIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M6.6 9.4l2.8-2.8"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
      <path d="M9 5.1l1.3-1.3a2.7 2.7 0 013.8 3.8L12.8 9"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M7 10.9L5.7 12.2a2.7 2.7 0 01-3.8-3.8L3.2 7"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 链条（限额） */
export function ChainIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M6.4 9.6l3.2-3.2"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
      <path d="M4.9 7.2L3.6 8.5a2.2 2.2 0 003.1 3.1l1.3-1.3"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M11.1 8.8l1.3-1.3a2.2 2.2 0 00-3.1-3.1L8 5.7"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 剪刀（移除组织） */
export function ScissorsIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="4.2"cy="12"r="2"stroke="currentColor"strokeWidth={1.7} />
      <circle cx="11.8"cy="12"r="2"stroke="currentColor"strokeWidth={1.7} />
      <path d="M5.6 10.6L12.4 2.6M10.4 10.6L3.6 2.6"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
    </svg>
  );
}

/** 交换（移动组织） */
export function SwapIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M2.6 5.6h10.8l-2.6-2.8"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M13.4 10.4H2.6l2.6 2.8"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 皇冠（部门经理） */
export function CrownIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M2.4 12.4h11.2"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
      <path d="M2.6 4.6l2.1 2.3L8 2.6l3.3 4.3 2.1-2.3-1.3 7.8H3.9L2.6 4.6z"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 文件夹（组织树） */
export function FolderIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M2.4 4.2c0-.8.6-1.4 1.4-1.4h2.4l1.5 1.8h5c.8 0 1.4.6 1.4 1.4v5.6c0 .8-.6 1.4-1.4 1.4H3.8c-.8 0-1.4-.6-1.4-1.4V4.2z"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 更多 */
export function MoreIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="3.6"cy="8"r="1.1"fill="currentColor"/>
      <circle cx="8"cy="8"r="1.1"fill="currentColor"/>
      <circle cx="12.4"cy="8"r="1.1"fill="currentColor"/>
    </svg>
  );
}

/** 搜索 */
export function SearchIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="7.1"cy="7.1"r="4.6"stroke="currentColor"strokeWidth={1.7} />
      <path d="M10.5 10.5l3 3"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
    </svg>
  );
}

/** 汉堡菜单（移动端后台） */
export function MenuIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M2.6 4.4h10.8M2.6 8h10.8M2.6 11.6h10.8"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
    </svg>
  );
}

/** 右箭头 */
export function ChevronRightIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M6 3.2L10.4 8L6 12.8"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 左箭头（返回 / 上一步） */
export function ChevronLeftIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M10 3.2L5.6 8L10 12.8"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 细右箭头（菜单行尾 ›） */
export function ArrowRightIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M3.4 8h9.2M9 4.4L12.6 8L9 11.6"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 上箭头（增长） */
export function ArrowUpIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M8 12.6V3.4M4.4 7L8 3.4L11.6 7"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 齿轮（系统与运维） */
export function GearIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="8"cy="8"r="2.4"stroke="currentColor"strokeWidth={1.7} />
      <path d="M8 1.7v1.7M8 12.6v1.7M2.3 8h1.7M12 8h1.7M4 4l1.2 1.2M10.8 10.8L12 12M12 4l-1.2 1.2M5.2 10.8L4 12"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
    </svg>
  );
}

/** 卫星（外部调用） */
export function SatelliteIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M6.2 9.8l-2.6 2.6M9.8 6.2l2.6-2.6"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
      <path d="M10.6 3.2l2.2 2.2-1.6 1.6-2.2-2.2 1.6-1.6z"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M5.4 10.8l-2.2 2.2 1.6 1.6 2.2-2.2-1.6-1.6z"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M3.6 7.2L2.2 5.8l2-2 1.4 1.4M12.4 8.8l1.4 1.4-2 2-1.4-1.4"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 宝石（计费与套餐） */
export function GemIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M5.4 2.4h5.2l3.2 4.3-5.8 6.9L2.2 6.7l3.2-4.3z"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M2.2 6.7h11.6M5.4 2.4l2.6 4.3 2.6-4.3"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 机器人（AI 助手） */
export function RobotIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <rect x="2.8"y="5"width="10.4"height="8"rx="2"stroke="currentColor"strokeWidth={1.7} />
      <path d="M8 2.4v2.6"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
      <circle cx="8"cy="1.9"r="1"fill="currentColor"/>
      <circle cx="6"cy="8.8"r="1"fill="currentColor"/>
      <circle cx="10"cy="8.8"r="1"fill="currentColor"/>
      <path d="M6.2 11.4h3.6"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
      <path d="M1.6 8.4v2.2M14.4 8.4v2.2"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
    </svg>
  );
}

/** 扳手（进入管理后台） */
export function WrenchIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M10.6 2.6a3.4 3.4 0 00-4.3 4.3L2.4 10.8c-.5.5-.5 1.4 0 1.9s1.4.5 1.9 0l3.9-3.9a3.4 3.4 0 004.3-4.3l-3 3-2.1-2.1 3-2.8z"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 退出（danger） */
export function LogoutIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M6.4 13.4H3.6c-.8 0-1.4-.6-1.4-1.4V4c0-.8.6-1.4 1.4-1.4h2.8"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M10.4 5.4L13.4 8l-3 2.6M6.2 8h7.2"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 邮件（修改邮箱 / 发送验证码） */
export function MailIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <rect x="2"y="3.6"width="12"height="8.8"rx="1.6"stroke="currentColor"strokeWidth={1.7} />
      <path d="M2.4 4.4L8 8.4l5.6-4"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 钥匙（API Key） */
export function KeyIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="5.2"cy="10.8"r="2.8"stroke="currentColor"strokeWidth={1.7} />
      <path d="M7.2 8.8l5.4-5.4M10.4 5.2l1.8 1.8M12.2 3.4l1.8 1.8"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 警告三角（告警 / 额度） */
export function AlertIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M8 2.4l6.2 11H1.8L8 2.4z"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M8 6.4v3.2"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
      <circle cx="8"cy="11.4"r="0.85"fill="currentColor"/>
    </svg>
  );
}

/** 圆圈勾（完成态 / 检查点） */
export function CheckCircleIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="8"cy="8"r="6.4"stroke="currentColor"strokeWidth={1.7} />
      <path d="M5.3 8.2L7.2 10.2L10.8 6"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 标签（品牌名） */
export function TagIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M8.4 1.9H13a1.1 1.1 0 011.1 1.1v4.6c0 .3-.1.6-.3.8l-5.6 5.6a1.1 1.1 0 01-1.6 0L2.9 10a1.1 1.1 0 010-1.6L8.5 2.2c.2-.2.4-.3.6-.3z"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <circle cx="10.8"cy="5.2"r="1.1"fill="currentColor"/>
    </svg>
  );
}

/** 层（行业管理 / 数据采集） */
export function LayersIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M8 1.9l6.2 3.3-6.2 3.3L1.8 5.2 8 1.9z"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M1.8 8.5l6.2 3.3 6.2-3.3"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M1.8 11.6l6.2 3.3 6.2-3.3"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 网（数据采集） */
export function WebIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="8"cy="8"r="6.3"stroke="currentColor"strokeWidth={1.7} />
      <path d="M8 1.7v12.6M1.7 8h12.6M3.5 3.5l9 9M12.5 3.5l-9 9"stroke="currentColor"strokeWidth={1.5} />
    </svg>
  );
}

/** 灯泡（计费说明） */
export function IdeaIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M8 1.9a4.3 4.3 0 00-2.5 7.8c.4.3.6.7.6 1.2v.6h3.8v-.6c0-.5.2-.9.6-1.2A4.3 4.3 0 008 1.9z"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
      <path d="M6.4 13.4h3.2"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
    </svg>
  );
}

/** 终端（快捷键） */
export function TerminalIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <rect x="1.9"y="2.6"width="12.2"height="10.8"rx="1.6"stroke="currentColor"strokeWidth={1.7} />
      <path d="M4.6 6.6l2.1 2.1-2.1 2.1M8.4 10.8h3"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"strokeLinejoin="round"/>
    </svg>
  );
}

/** 禁用（停用） */
export function BanIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="8"cy="8"r="6.3"stroke="currentColor"strokeWidth={1.7} />
      <path d="M3.6 12.4l8.8-8.8"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
    </svg>
  );
}

/** 卡片（支付 / 发票） */
export function CardIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <rect x="1.9"y="3.6"width="12.2"height="8.8"rx="1.6"stroke="currentColor"strokeWidth={1.7} />
      <path d="M1.9 6.6h12.2"stroke="currentColor"strokeWidth={1.7} />
      <path d="M4.4 9.8h3"stroke="currentColor"strokeWidth={1.7} strokeLinecap="round"/>
    </svg>
  );
}
