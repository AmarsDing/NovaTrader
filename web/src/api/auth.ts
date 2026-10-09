import { request } from '@/api/http'
import { pick } from '@/utils/format'
import type { LoginReply, LoginResult } from './types/auth'
import type { UserRole } from '@/types/session'

export interface LoginPayload {
  username: string
  password: string
}

export async function loginApi(payload: LoginPayload): Promise<LoginResult> {
  const data = await request<LoginReply>('/api/v1/login', {
    method: 'POST',
    json: { user_name: payload.username, password: payload.password },
  })
  const user = data?.user && typeof data.user === 'object' ? data.user : {}
  const name = pick<string>(user, 'displayName', 'display_name', 'name', 'userName', 'user_name') || payload.username
  const roleRaw = pick<string>(user, 'role', 'Role') || ''
  return {
    token: pick<string>(data, 'token') || '',
    user: {
      id: pick<string>(user, 'id', 'userId', 'user_id') || '',
      name,
      displayName: name,
      role: String(roleRaw).toLowerCase() as UserRole,
      local: false,
    },
  }
}
