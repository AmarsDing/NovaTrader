/** 登录身份与角色。owner 可写，viewer 只读。 */
export type UserRole = 'owner' | 'viewer' | string

export interface SessionUser {
  id: string
  name: string
  displayName: string
  role: UserRole
  /** 本地会话（未登录后端）标记；本地会话不伪造账户数字。 */
  local?: boolean
}
