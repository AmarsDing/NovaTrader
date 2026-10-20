/**
 * F10 抽屉开关状态（方案 §6.5 悬浮卡+抽屉）：右锚 F10Drawer 的唯一数据源。
 * - 宿主（SymbolName 悬浮卡、行情面板工具位等）只调 openFor(code, tab?)，不直接挂抽屉；
 * - 悬浮卡本体是宿主局部瞬态，不进本 store；
 * - code 经 security.normalizeCode 归一为全码，与全站证券代码口径一致。
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { normalizeCode } from '@/stores/security'

export type F10Tab = 'overview' | 'finance' | 'announce' | 'ai'

export const useF10Store = defineStore('f10', () => {
  const open = ref(false)
  const code = ref('')
  const tab = ref<F10Tab>('overview')

  function openFor(raw: string, nextTab?: F10Tab) {
    const normalized = normalizeCode(raw)
    if (!normalized) return
    code.value = normalized
    if (nextTab) tab.value = nextTab
    open.value = true
  }

  function close() {
    open.value = false
  }

  return { open, code, tab, openFor, close }
})
