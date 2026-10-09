/**
 * 后端地址：只配 admin 主机，HTTP 固定 2001，WebSocket 固定 2003。
 * 桌面端不让界面直连 datahub / trade 等其他服务。
 */
import { getToken } from './token'

const HOST_KEY = 'nt_admin_host'

export function getHost(): string {
  const raw = localStorage.getItem(HOST_KEY) || '127.0.0.1'
  return sanitizeHost(raw) || '127.0.0.1'
}

export function setHost(value: unknown): string {
  const host = sanitizeHost(value)
  if (!host) throw new Error('请填写 admin 主机名或 IP')
  localStorage.setItem(HOST_KEY, host)
  return host
}

export function httpBase(): string {
  return `http://${getHost()}:2001`
}

export function wsUrl(): string {
  const url = `ws://${getHost()}:2003/ws`
  const token = getToken()
  if (!token) return url
  return `${url}?token=${encodeURIComponent(token)}`
}

export function sanitizeHost(value: unknown): string {
  return String(value || '')
    .trim()
    .replace(/^https?:\/\//, '')
    .replace(/^wss?:\/\//, '')
    .split('/')[0]
    .split(':')[0]
}
