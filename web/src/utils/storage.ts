/** 会话用户信息的本地留档（不含令牌）。 */
import type { SessionUser } from '@/types/session'

const USER_KEY = 'nt_user'
const LEGACY_TOKEN = 'nt_token'

export function getStoredUser(): SessionUser | null {
  try {
    const raw = localStorage.getItem(USER_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as SessionUser
    return parsed && typeof parsed === 'object' ? parsed : null
  } catch {
    return null
  }
}

export function setStoredUser(user: SessionUser | null): void {
  if (user) localStorage.setItem(USER_KEY, JSON.stringify(user))
  else localStorage.removeItem(USER_KEY)
}

/** 取走早期版本遗留在 localStorage 的明文令牌，取完即删。 */
export function takeLegacyToken(): string {
  const legacy = localStorage.getItem(LEGACY_TOKEN)
  if (legacy) localStorage.removeItem(LEGACY_TOKEN)
  return legacy || ''
}
