/** 登录与会话（admin 自有接口 /api/v1/login）。 */
import type { SessionUser, UserRole } from '@/types/session'

export type { SessionUser, UserRole }

export interface LoginRequest {
  user_name: string
  password: string
}

export interface LoginReply {
  token?: string
  user?: {
    id?: string
    name?: string
    display_name?: string
    role?: string
  }
}

export interface LoginResult {
  token: string
  user: SessionUser
}
