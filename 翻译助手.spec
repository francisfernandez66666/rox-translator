# -*- mode: python ; coding: utf-8 -*-
# ============================================================================
# 翻译助手.spec — PyInstaller 构建配置
# 使用方式: pyinstaller --clean 翻译助手.spec
# 构建前需先执行: cd frontend && npm run build
# ============================================================================
a = Analysis(
    ['backend/main.py'],
    pathex=[],
    binaries=[],
    datas=[
        ('frontend/dist', 'frontend/dist'),
        ('backend/data/tm.sqlite3', 'data'),
        ('backend/data/tm_embeddings.npz', 'data'),
        ('backend/skills/translation', 'skills/translation'),
        ('backend/services', 'services'),
        ('config.json', '.'),
    ],
    hiddenimports=[
        'skills.translation.skill',
        'skills.translation.lib',
        'services.file_service',
        'base_skill',
        'skill_registry',
    ],
    hookspath=[],
    hooksconfig={},
    runtime_hooks=[],
    excludes=[],
    noarchive=False,
    optimize=0,
)
pyz = PYZ(a.pure)

exe = EXE(
    pyz,
    a.scripts,
    [],
    exclude_binaries=True,
    name='翻译助手',
    debug=False,
    bootloader_ignore_signals=False,
    strip=False,
    upx=True,
    upx_exclude=[],
    runtime_tmpdir=None,
    console=False,
    disable_windowed_traceback=False,
    argv_emulation=False,
    target_arch=None,
    codesign_identity='-',
    entitlements_file=None,
)

coll = COLLECT(
    exe,
    a.binaries,
    a.datas,
    strip=False,
    upx=True,
    upx_exclude=[],
    name='翻译助手',
)

# .app 由 build.sh 构建，此处不再通过 BUNDLE 创建
