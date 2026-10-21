// ============================================================================
// modals.password.lock.test.ts — 改密入口不得再连公开找回/重置端点
//
// 2026-10-21 IAM 重做把 /api/auth/forgot-password、/api/auth/reset-password 整体下线；
// 账号菜单仍复用旧 PasswordModal 时，用户会在「已登录改密码」里得到 410。
// 本锁按文件全集检查：登录态验证码 → change-password 是唯一链路。
// ============================================================================
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const authApi = readFileSync(resolve(__dirname, '../api/auth.ts'), 'utf8')
const modals = readFileSync(resolve(__dirname, './modals.tsx'), 'utf8')
const accountMenu = readFileSync(resolve(__dirname, './AccountMenu.tsx'), 'utf8')

describe('已登录改密链（公开找回/重置下线后）', () => {
  it('PasswordModal 只经登录态 helper 改密', () => {
    expect(modals).toContain('sendPwdCode')
    expect(modals).toContain('submitNewPassword')
    expect(modals).not.toContain('/api/auth/forgot-password')
    expect(modals).not.toContain('/api/auth/reset-password')
  })

  it('sendPwdCode / submitNewPassword 分别接 email-code 与 change-password', () => {
    const send = authApi.match(/export async function sendPwdCode[\s\S]*?\n}/)?.[0] ?? ''
    const submit = authApi.match(/export async function submitNewPassword[\s\S]*?\n}/)?.[0] ?? ''
    expect(send).toContain("sendEmailCode(data.email, undefined, 'password')")
    expect(submit).toContain("changePassword('', data.new_password, data.code)")
    expect(authApi).not.toContain('/api/auth/forgot-password')
    expect(authApi).not.toContain('/api/auth/reset-password')
  })

  it('未绑定邮箱先走换绑弹窗，不把用户送进必失败的发码弹窗', () => {
    expect(accountMenu).toMatch(/if \(curEmail\) setOpenPwd\(true\)\s*else setOpenBind\(true\)/)
  })
})
