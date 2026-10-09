/** 异步工具：吞掉异常返回 null（看板并行刷新时单点失败不拖垮整页）。 */

export async function quiet<T>(fn: () => Promise<T>): Promise<T | null> {
  try {
    return await fn()
  } catch {
    return null
  }
}
