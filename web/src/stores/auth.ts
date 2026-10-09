/**
 * 会话 store（Pinia）。迁移自 store/auth.js，行为保持一致：
 * 令牌三份（内存 + keyring/sessionStorage + 本地用户档案），登录成功后启动 WS 链路。
 * 注意：依赖 main.ts 先 setActivePinia 再调用 hydrate()。
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { loginApi } from '@/api/auth'
import type { SessionUser } from '@/types/session'
import { readToken, writeToken } from '@/utils/credentials'
import { messageOf } from '@/utils/format'
import { getStoredUser, setStoredUser, takeLegacyToken } from '@/utils/storage'
import { setMemoryToken } from '@/utils/token'
import { startLink, stopLink } from '@/ws/hub'

export const useAuthStore = defineStore('auth', () => {
  const token = ref('')
  const user = ref<SessionUser | null>(null)
  const loading = ref(false)
  const error = ref('')
  const canLocal = ref(false)

  async function applySession(data: { token: string; user: SessionUser }) {
    if (!data.token) throw new Error('登录响应没有令牌')
    await writeToken(data.token)
    setMemoryToken(data.token)
    setStoredUser(data.user)
    token.value = data.token
    user.value = data.user
  }

  async function hydrate() {
    const legacy = takeLegacyToken()
    let t = ''
    try {
      t = await readToken()
    } catch {
      t = ''
    }
    if (!t && legacy) {
      t = legacy
      try {
        await writeToken(t)
      } catch {
        t = ''
      }
    }
    setMemoryToken(t)
    token.value = t
    if (t.startsWith('local-')) {
      const localUser: SessionUser = getStoredUser() || {
        id: 'local',
        name: '本地会话',
        displayName: '本地会话',
        role: 'owner' as SessionUser['role'],
        local: true,
      }
      localUser.local = true
      user.value = localUser
      setStoredUser(localUser)
    } else {
      user.value = t ? getStoredUser() : null
    }
  }

  async function clear() {
    token.value = ''
    user.value = null
    error.value = ''
    canLocal.value = false
    setMemoryToken('')
    setStoredUser(null)
    try {
      await writeToken('')
    } catch {
      // 凭据库不可用时仍清掉内存会话。
    }
    stopLink()
  }

  async function login(username: string, password: string): Promise<boolean> {
    loading.value = true
    error.value = ''
    canLocal.value = false
    try {
      const data = await loginApi({ username: username.trim(), password })
      await applySession(data)
      startLink()
      return true
    } catch (err) {
      error.value = messageOf(err)
      const status = (err as { status?: number } | null)?.status
      canLocal.value = status === 404 || status === 405 || status === 501 || status === 0
      return false
    } finally {
      loading.value = false
    }
  }

  async function enterLocal(username: string): Promise<boolean> {
    const name = username.trim()
    if (!name) {
      error.value = '请输入账号'
      return false
    }
    await applySession({
      token: `local-${Date.now()}`,
      user: { id: 'local', name, displayName: name, role: 'owner' as SessionUser['role'], local: true },
    })
    error.value = ''
    canLocal.value = false
    startLink()
    return true
  }

  function isViewer(u: SessionUser | null | undefined = user.value): boolean {
    return String(u?.role || '').toLowerCase() === 'viewer'
  }

  const operatorName = computed(() => user.value?.displayName || user.value?.name || '')

  return { token, user, loading, error, canLocal, hydrate, clear, login, enterLocal, isViewer, operatorName }
})
