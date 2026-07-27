# -*- mode: python ; coding: utf-8 -*-
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

app = BUNDLE(
    coll,
    name='翻译助手.app',
    icon=None,
    bundle_identifier='com.rox.translator',
    info_plist={
        'NSHighResolutionCapable': True,
        'CFBundleShortVersionString': '1.0.0',
        'CFBundleVersion': '1',
        'CFBundleDisplayName': '翻译助手',
        'LSMinimumSystemVersion': '10.15',
    },
    codesign_identity='-',
    entitlements_file=None,
)
