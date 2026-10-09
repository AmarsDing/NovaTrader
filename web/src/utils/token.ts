/**
 * 内存令牌：只在本次页面生命周期里存在。
 * 持久化走 utils/credentials.ts（桌面端进系统凭据库）。
 */

let token = ''

export function getToken(): string {
  return token
}

export function setMemoryToken(value: unknown): void {
  token = typeof value === 'string' ? value : ''
}
