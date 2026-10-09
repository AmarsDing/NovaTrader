import { createRouter, createWebHistory } from 'vue-router'
import { getToken } from '@/utils/token'

const workbench = () => import('@/workspace/WorkbenchView.vue')

const routes = [
  {
    path: '/login',
    name: 'login',
    component: () => import('@/view/LoginView.vue'),
    meta: { public: true },
  },
  // 全驾驶舱：进 app 即工作台，面板由 dockview 承载
  { path: '/', name: 'workbench', component: workbench },
  // 深链：开对应面板后把地址剥回 `/`（见 WorkbenchView.consumeDeepLink）
  { path: '/panel/:id', name: 'panel', component: workbench },
  { path: '/settings', redirect: '/panel/settings' },
  // 旧侧栏路径兼容（P3 前的 /signals、/market…）
  {
    path: '/:legacy(signals|market|trade|intel|backtest|ops|overview)',
    redirect: (to) => ({ path: `/panel/${String(to.params.legacy)}`, query: to.query }),
  },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.beforeEach((to) => {
  const authed = !!getToken()
  if (to.meta.public) {
    if (authed && to.name === 'login') return { path: '/' }
    return true
  }
  if (!authed) return { name: 'login', query: { redirect: to.fullPath } }
  return true
})
