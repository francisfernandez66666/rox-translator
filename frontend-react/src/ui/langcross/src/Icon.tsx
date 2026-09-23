import {
  AlertIcon,
  BanIcon,
  BellIcon,
  BookIcon,
  BoltIcon,
  BrandDotIcon,
  BuildingIcon,
  CardIcon,
  ChainIcon,
  ChartIcon,
  ChatIcon,
  CheckCircleIcon,
  CheckIcon,
  ClipboardIcon,
  CloseIcon,
  CrownIcon,
  DocIcon,
  DownloadIcon,
  EyeIcon,
  EyeOffIcon,
  FolderIcon,
  GaugeIcon,
  GemIcon,
  GearIcon,
  GlobeIcon,
  IdeaIcon,
  KeyIcon,
  LayersIcon,
  LinkIcon,
  LockIcon,
  LogoutIcon,
  MailIcon,
  MenuIcon,
  MoreIcon,
  PackageIcon,
  PencilIcon,
  PlusIcon,
  RefreshIcon,
  RobotIcon,
  SatelliteIcon,
  ScissorsIcon,
  SearchIcon,
  SendIcon,
  ShieldIcon,
  SparkIcon,
  SwapIcon,
  TagIcon,
  TerminalIcon,
  TextIcon,
  ThemeIcon,
  TrashIcon,
  UploadIcon,
  UserIcon,
  UsersIcon,
  WebIcon,
  WrenchIcon,
} from "./icons";

/**
 * 图标名 → 组件。用于替换设计切图里的 emoji 图标占位
 * （UI-ANNOTATIONS.md §0.1：emoji 是画布图标位，不是文案）。
 */
const REGISTRY = {
  alert: AlertIcon,
  ban: BanIcon,
  bell: BellIcon,
  book: BookIcon,
  bolt: BoltIcon,
  brand: BrandDotIcon,
  building: BuildingIcon,
  card: CardIcon,
  chain: ChainIcon,
  chart: ChartIcon,
  chat: ChatIcon,
  checkcircle: CheckCircleIcon,
  check: CheckIcon,
  clipboard: ClipboardIcon,
  close: CloseIcon,
  crown: CrownIcon,
  doc: DocIcon,
  download: DownloadIcon,
  eye: EyeIcon,
  eyeoff: EyeOffIcon,
  folder: FolderIcon,
  gauge: GaugeIcon,
  gem: GemIcon,
  gear: GearIcon,
  globe: GlobeIcon,
  idea: IdeaIcon,
  key: KeyIcon,
  layers: LayersIcon,
  link: LinkIcon,
  lock: LockIcon,
  logout: LogoutIcon,
  mail: MailIcon,
  menu: MenuIcon,
  more: MoreIcon,
  package: PackageIcon,
  pencil: PencilIcon,
  plus: PlusIcon,
  refresh: RefreshIcon,
  robot: RobotIcon,
  satellite: SatelliteIcon,
  scissors: ScissorsIcon,
  search: SearchIcon,
  send: SendIcon,
  shield: ShieldIcon,
  spark: SparkIcon,
  swap: SwapIcon,
  tag: TagIcon,
  terminal: TerminalIcon,
  text: TextIcon,
  theme: ThemeIcon,
  trash: TrashIcon,
  upload: UploadIcon,
  user: UserIcon,
  users: UsersIcon,
  web: WebIcon,
  wrench: WrenchIcon,
} as const;

// 图标名联合类型：取值由 REGISTRY 决定，禁止手写未注册的名字
export type IconName = keyof typeof REGISTRY;

// 图标组件入参：尺寸/颜色/描边宽（统一 16×16、stroke 1.6–1.9、currentColor）
export interface IconComponentProps {
  /** 图标名，见 REGISTRY */
  n: IconName;
  size?: number;
  className?: string;
  style?: React.CSSProperties;
}

/**
 * 统一图标入口：`<Icon n="doc" size={16} />`
 * 全部 16×16 viewBox、stroke 1.6–1.9、currentColor，与全站单色体系一致。
 */
export function Icon({ n, size = 16, className, style }: IconComponentProps) {
  const Cmp = REGISTRY[n];
  if (!Cmp) return null;
  return <Cmp size={size} className={className} style={style} />;
}

export default Icon;
