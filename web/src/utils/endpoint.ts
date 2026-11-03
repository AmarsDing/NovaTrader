/**
 * 后端地址：配 admin 主机 + HTTP/WS 端口（默认 2001 / 2003）。
 * 桌面端不让界面直连 datahub / trade 等其他服务。
 * 共享机部署（如 44）可改端口避让冲突。
 */
import { getToken } from './token'

const HOST_KEY = 'nt_admin_host'
const HTTP_PORT_KEY = 'nt_admin_http_port'
const WS_PORT_KEY = 'nt_admin_ws_port'

const DEFAULT_HTTP_PORT = Number(import.meta.env.VITE_DEFAULT_HTTP_PORT) || 2001
const DEFAULT_WS_PORT = Number(import.meta.env.VITE_DEFAULT_WS_PORT) || 2003
const DEFAULT_HOST = String(import.meta.env.VITE_DEFAULT_HOST || '127.0.0.1')

export function getHost(): string {
  const raw = localStorage.getItem(HOST_KEY) || DEFAULT_HOST
  return sanitizeHost(raw) || DEFAULT_HOST || '127.0.0.1'
}

export function setHost(value: unknown): string {
  const host = sanitizeHost(value)
  if (!host) throw new Error('请填写 admin 主机名或 IP')
  localStorage.setItem(HOST_KEY, host)
  return host
}

export function getHttpPort(): number {
  return readPort(localStorage.getItem(HTTP_PORT_KEY), DEFAULT_HTTP_PORT)
}

export function getWsPort(): number {
  return readPort(localStorage.getItem(WS_PORT_KEY), DEFAULT_WS_PORT)
}

export function setHttpPort(value: unknown): number {
  const port = parsePort(value)
  if (!port) throw new Error('请填写有效的 HTTP 端口（1–65535）')
  localStorage.setItem(HTTP_PORT_KEY, String(port))
  return port
}

export function setWsPort(value: unknown): number {
  const port = parsePort(value)
  if (!port) throw new Error('请填写有效的 WebSocket 端口（1–65535）')
  localStorage.setItem(WS_PORT_KEY, String(port))
  return port
}

export function httpBase(): string {
  return `http://${getHost()}:${getHttpPort()}`
}

export function wsUrl(): string {
  const url = `ws://${getHost()}:${getWsPort()}/ws`
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

function readPort(raw: string | null, fallback: number): number {
  if (raw == null || raw === '') return fallback
  const n = Number(raw)
  if (!Number.isInteger(n) || n < 1 || n > 65535) return fallback
  return n
}

function parsePort(value: unknown): number | null {
  const n = typeof value === 'number' ? value : Number(String(value ?? '').trim())
  if (!Number.isInteger(n) || n < 1 || n > 65535) return null
  return n
}
