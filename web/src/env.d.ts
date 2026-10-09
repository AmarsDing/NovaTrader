/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly DEV: boolean
  readonly PROD: boolean
  readonly MODE: string
  /** 覆盖 admin HTTP 端口，开发态调试用 */
  readonly VITE_ADMIN_HTTP?: string
  /** 覆盖 admin WebSocket 端口，开发态调试用 */
  readonly VITE_ADMIN_WS?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
