/**
 * 令牌持久化。
 * 桌面端：Tauri 命令进 Windows 凭据库。
 * 浏览器开发态：只放 sessionStorage，开新标签即失效。
 */
import { inTauri } from './tauri'

const SESSION_KEY = 'nt_token'

export async function readToken(): Promise<string> {
  if (inTauri()) {
    const { invoke } = await import('@tauri-apps/api/core')
    const value = await invoke<string>('credential_get')
    return typeof value === 'string' ? value : ''
  }
  return sessionStorage.getItem(SESSION_KEY) || ''
}

export async function writeToken(token: string): Promise<void> {
  if (inTauri()) {
    const { invoke } = await import('@tauri-apps/api/core')
    if (token) await invoke('credential_set', { token })
    else await invoke('credential_delete')
    return
  }
  if (token) sessionStorage.setItem(SESSION_KEY, token)
  else sessionStorage.removeItem(SESSION_KEY)
}
