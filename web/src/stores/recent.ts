/**
 * 最近使用的股票代码（G2 助记符联想数据源）。
 * 纪律（抄 gloomberb recentPaneTemplateArg）：只收归一后的 6 位数字代码，
 * 自由文本（复盘备注、短语等）绝不写入持久化 —— 防敏感文本泄漏进联想列表。
 * 最多保留 20 条，最新在前；持久化 localStorage `nt.recent-tickers.v1`。
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { isTicker } from '@/workspace/command-grammar'

const KEY = 'nt.recent-tickers.v1'
const MAX = 20

export const useRecentStore = defineStore('recent', () => {
  const tickers = ref<string[]>([])

  function load() {
    try {
      const raw = localStorage.getItem(KEY)
      if (!raw) return
      const data: unknown = JSON.parse(raw)
      if (!Array.isArray(data)) return
      // 历史写入也过滤非法形态，坏存储净化而非崩溃。
      tickers.value = data.filter((t): t is string => typeof t === 'string' && isTicker(t)).slice(0, MAX)
    } catch {
      // 损坏数据回空，不阻塞启动。
    }
  }

  function persist() {
    try {
      localStorage.setItem(KEY, JSON.stringify(tickers.value))
    } catch {
      /* 存储满/隐私模式忽略 */
    }
  }

  /** 记入最近代码；非 6 位数字静默拒收（调用方传参前不必自行校验）。 */
  function pushTicker(code: string) {
    const t = code.trim()
    if (!isTicker(t)) return
    tickers.value = [t, ...tickers.value.filter((x) => x !== t)].slice(0, MAX)
    persist()
  }

  function clear() {
    tickers.value = []
    persist()
  }

  load()

  return { tickers, pushTicker, clear }
})
