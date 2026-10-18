/**
 * G6 布局撤销/重做栈（dockview 序列化快照，容量 20）。
 * 纪律：只在程序化整版切换（桌面包 / 预设 / 重置默认）前记录当前快照；
 * 用户手拖面板不入栈 —— 否则栈里全是拖拽中间态，撤销失去语义。
 * 纯模块无副作用（调用方决定套用），可单测。
 */
const CAPACITY = 20

let undoStack: unknown[] = []
let redoStack: unknown[] = []

/** 程序化切换前调用：压入当前快照；任何新切换清空重做栈（经典编辑器语义）。 */
export function recordLayout(current: unknown): void {
  if (current == null) return
  undoStack.push(current)
  if (undoStack.length > CAPACITY) undoStack.shift()
  redoStack = []
}

/** 撤销：current 进重做栈，弹出并返回要套用的上一版；无可撤销返回 null。 */
export function undoLayout(current: unknown): unknown | null {
  const prev = undoStack.pop()
  if (prev === undefined) return null
  if (current != null) redoStack.push(current)
  return prev
}

/** 重做：current 回撤销栈，弹出并返回要套用的下一版；无可重做返回 null。 */
export function redoLayout(current: unknown): unknown | null {
  const next = redoStack.pop()
  if (next === undefined) return null
  if (current != null) undoStack.push(current)
  return next
}

export function historySizes(): { undo: number; redo: number } {
  return { undo: undoStack.length, redo: redoStack.length }
}

/** 测试与账号切换时重置。 */
export function resetHistory(): void {
  undoStack = []
  redoStack = []
}
