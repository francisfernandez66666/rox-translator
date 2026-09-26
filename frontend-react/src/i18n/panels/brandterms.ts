// ============ panels/brandterms.ts · 职责说明 ============
// 品牌名术语面板（BrandTermsP）i18n 键（★ F2）
// =============================================
export const zh: Record<string, string> = {
  'bt.needBrand': '请输入品牌中文名',
  'bt.needEn': '请输入品牌外语统一译法（如 ROX）',
  'bt.added': '品牌名已新增（全语言译为 {en}）',
  'bt.addFail': '新增失败：{err}',
  // ★ F-58（2026-09-26 〇-U 批 I-8）：21 语种串行写入的中止与诚实回执四键。
  //   bt.adding 替掉提交期间的「保存」按钮文案（进度可见），bt.stop 是同一期间的取消钮语义
  //   （点它=中止后续语种，不是关掉窗口继续跑）；后两键按实际结果分支，
  //   绝不再在无脑 toastSuccess 里把「没写完/写挂了」说成成功。
  'bt.adding': '写入中 {ok}/{total}…',
  'bt.stop': '中止',
  'bt.addedPartial': '已写入 {ok}/{total} 个语言，未成功：{why}',
  'bt.addCancelled': '已中止：写入 {ok}/{total} 个语言，其余语言未提交，可改正后重新保存补齐',
  // ★ F-25（2026-09-25）：原三枚 window.prompt 旧文案键（bt 前缀下的 promptLang、promptText、
  //   editPrompt 三键）已随站内 Dialog 改造删除（键集闸容不下死键），
  //   语言/译法输入改走 bt.formLangLabel / bt.formTextLabel 表单标签。
  'bt.langAdded': '已补充 {lang} 译法',
  'bt.langFail': '补充失败：{err}',
  // ★ F-25：编辑弹窗（单语言补充 / 修改译法同构）标题与三字段标签
  'bt.editLangTitle': '编辑品牌译法',
  'bt.formLangLabel': '目标语言',
  'bt.formTextLabel': '译法（译文）',
  'bt.updated': '已更新',
  'bt.updateFail': '更新失败：{err}',
  // ★ F-25：删除前置站内确认框（confirmDialog 标题 + 正文，正文带品牌名与语种两个占位符）
  'bt.delConfirmTitle': '确认删除该语言译法',
  'bt.delConfirmBody': '确定删除「{brand}」的 {lang} 译法吗？删除后该语言不再强制规定译法。',
  'bt.entryDeleted': '已删除该语言条目',
  'bt.deleteFail': '删除失败：{err}',
  'bt.hint': '品牌名（如 极石 / 极石汽车）在外语统一译为规定写法（如 ROX）。翻译管线会强约束并剥离「ROX vehicles / ROX motor」等自创后缀。',
  'bt.new': '＋ 新增品牌名',
  'bt.newTitle': '新增品牌名',
  'bt.brandLabel': '品牌中文名',
  'bt.brandPlaceholder': '极石 或 极石汽车',
  'bt.enLabel': '外语统一译法',
  'bt.cancel': '取消',
  'bt.save': '保存',
  'bt.needPkg': '请先选择知识库包',
  'bt.empty': '暂无品牌名术语，点击「新增品牌名」添加。',
  'bt.colBrand': '品牌名',
  'bt.colLangs': '各语言译法',
  'bt.pkgLabel': '知识库包',
  'bt.editShort': '改',
  'bt.delEntry': '删除该语言译法',
  'bt.addLang': '+补语言',
}
// 英文文案字典（与 zh 同 key 对齐，parity.test.ts 守护双语一致性）
export const en: Record<string, string> = {
  'bt.needBrand': 'Enter the brand name (Chinese)',
  'bt.needEn': 'Enter the unified foreign translation (e.g. ROX)',
  'bt.added': 'Brand added (translated as {en} in all languages)',
  'bt.addFail': 'Add failed: {err}',
  // ★ F-58：与 zh 同步的四键（占位符 {ok}/{total}/{why} 逐键一致，locales 闸门按英文基准校验）
  'bt.adding': 'Writing {ok}/{total}…',
  'bt.stop': 'Stop',
  'bt.addedPartial': 'Written {ok}/{total} languages; failed: {why}',
  'bt.addCancelled': 'Stopped: wrote {ok}/{total} languages, the rest were not submitted. Fix and save again to complete them.',
  // ★ F-25（2026-09-25）：与 zh 同步——三枚 window.prompt 旧文案键（bt 前缀下的 promptLang、
  //   promptText、editPrompt）已删，
  //   新增编辑弹窗标题/字段标签与删除确认框文案（英文值决定各语种的占位符基准）。
  'bt.langAdded': 'Added {lang} translation',
  'bt.langFail': 'Add failed: {err}',
  'bt.editLangTitle': 'Edit Brand Translation',
  'bt.formLangLabel': 'Target language',
  'bt.formTextLabel': 'Translation',
  'bt.updated': 'Updated',
  'bt.updateFail': 'Update failed: {err}',
  'bt.delConfirmTitle': 'Delete This Translation',
  'bt.delConfirmBody': 'Delete the {lang} translation of "{brand}"? That language will no longer enforce the mandated brand form.',
  'bt.entryDeleted': 'Language entry deleted',
  'bt.deleteFail': 'Delete failed: {err}',
  'bt.hint': 'Brand names (e.g. 极石 / 极石汽车) are rendered with the mandated foreign form (e.g. ROX). The pipeline strips invented suffixes such as "ROX vehicles / ROX motor".',
  'bt.new': '+ Add Brand',
  'bt.newTitle': 'Add Brand',
  'bt.brandLabel': 'Brand (CN)',
  'bt.brandPlaceholder': 'e.g. Jishi / Jishi Auto',
  'bt.enLabel': 'Unified translation',
  'bt.cancel': 'Cancel',
  'bt.save': 'Save',
  'bt.needPkg': 'Select a KB package first',
  'bt.empty': 'No brand terms yet. Click "Add Brand".',
  'bt.colBrand': 'Brand',
  'bt.colLangs': 'Translations by language',
  'bt.pkgLabel': 'KB package',
  'bt.editShort': 'edit',
  'bt.delEntry': 'Delete this language translation',
  'bt.addLang': '+ add language',
}
