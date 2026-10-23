/**
 * 应用入口。
 * 顺序约定：先创建并激活 Pinia，再 hydrate 会话——store 门面在激活前不可调用。
 */
import { createPinia, setActivePinia } from 'pinia'
import { createApp } from 'vue'
import App from './App.vue'
import { router } from './router'
import { setUnauthorizedHandler } from '@/api/http'
import { useAuthStore } from '@/stores/auth'
import { useSecurityStore } from '@/stores/security'
import { useSettingsStore } from '@/stores/settings'
import { startServerClock } from '@/utils/server-clock'

import './style/global.css'

const pinia = createPinia()
setActivePinia(pinia)

const app = createApp(App)
app.use(pinia)
app.use(router)

useSettingsStore().init()
// §6.2 名称字典：启动即恢复本地缓存，fetcher 未注册前 ensure 静默 noop。
useSecurityStore().hydrate()

const auth = useAuthStore()

setUnauthorizedHandler(() => {
  void auth.clear()
  if (router.currentRoute.value.path !== '/login') void router.push('/login')
})

async function bootstrap() {
  await auth.hydrate()
  // P2-10：先挂钟再挂 UI——倒计时/时段显示首帧即可吃到校准源的降级态（未成功前=本地钟）。
  startServerClock()
  app.mount('#app')
}

void bootstrap()
