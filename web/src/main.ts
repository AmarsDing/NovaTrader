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
import { useSettingsStore } from '@/stores/settings'

import './style/global.css'

const pinia = createPinia()
setActivePinia(pinia)

const app = createApp(App)
app.use(pinia)
app.use(router)

useSettingsStore().init()

const auth = useAuthStore()

setUnauthorizedHandler(() => {
  void auth.clear()
  if (router.currentRoute.value.path !== '/login') void router.push('/login')
})

async function bootstrap() {
  await auth.hydrate()
  app.mount('#app')
}

void bootstrap()
