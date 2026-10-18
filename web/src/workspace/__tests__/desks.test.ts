/**
 * G6 桌面包定义锁定：四桌结构完整性 + Shift+N 热键匹配 + 全表快照。
 * 桌变更必须显式更新快照进评审（防静默改键位/面板编排）。
 */
import { describe, expect, it } from 'vitest'
import { DESKS, deskByHotkey, getDesk, matchDeskHotkey, validateDesks } from '../desks'

function keyEvent(init: KeyboardEventInit): KeyboardEvent {
  return new KeyboardEvent('keydown', init)
}

describe('桌定义完整性', () => {
  it('validateDesks 自证无问题', () => {
    expect(validateDesks()).toEqual([])
  })

  it('四桌齐：盘前/竞价/打板/复盘，热键 1..4', () => {
    expect(DESKS.map((d) => [d.hotkey, d.id])).toEqual([
      [1, 'preopen'],
      [2, 'auction'],
      [3, 'trading'],
      [4, 'review'],
    ])
  })

  it('竞价桌预绑行情 000001（参数走 panel-bus 通道）', () => {
    const auction = getDesk('auction')
    const market = auction?.panels.find((p) => p.id === 'market')
    expect(market?.params).toEqual({ symbol: '000001' })
  })

  it('打板桌含交易面板且为 docked 编排（桌不开 popout）', () => {
    const trading = getDesk('trading')
    expect(trading?.panels.map((p) => p.id)).toContain('trade')
    for (const desk of DESKS) {
      for (const p of desk.panels) {
        expect(['right', 'below', 'within', undefined]).toContain(p.direction)
      }
    }
  })

  it('DESKS 全表快照', () => {
    expect(DESKS).toMatchSnapshot()
  })
})

describe('matchDeskHotkey 热键判定', () => {
  it('Shift+Digit1..4 命中对应桌', () => {
    expect(matchDeskHotkey(keyEvent({ code: 'Digit1', shiftKey: true }))?.id).toBe('preopen')
    expect(matchDeskHotkey(keyEvent({ code: 'Digit2', shiftKey: true }))?.id).toBe('auction')
    expect(matchDeskHotkey(keyEvent({ code: 'Digit4', shiftKey: true }))?.id).toBe('review')
  })

  it('event.code 判定与键面字符无关（Shift+2 在部分布局产出 @）', () => {
    expect(matchDeskHotkey(keyEvent({ code: 'Digit2', key: '@', shiftKey: true }))?.id).toBe('auction')
  })

  it('未占用数字返回 null（Shift+5..9 留给后续桌/面板切换）', () => {
    expect(matchDeskHotkey(keyEvent({ code: 'Digit5', shiftKey: true }))).toBeNull()
    expect(matchDeskHotkey(keyEvent({ code: 'Digit9', shiftKey: true }))).toBeNull()
  })

  it('无 Shift / 带 Ctrl / 带 Alt 均不命中', () => {
    expect(matchDeskHotkey(keyEvent({ code: 'Digit1' }))).toBeNull()
    expect(matchDeskHotkey(keyEvent({ code: 'Digit1', shiftKey: true, ctrlKey: true }))).toBeNull()
    expect(matchDeskHotkey(keyEvent({ code: 'Digit1', shiftKey: true, altKey: true }))).toBeNull()
  })

  it('小键盘数字不命中（Numpad1 是不同工作键）', () => {
    expect(matchDeskHotkey(keyEvent({ code: 'Numpad1', shiftKey: true }))).toBeNull()
  })

  it('deskByHotkey / getDesk 反查', () => {
    expect(deskByHotkey(3)?.id).toBe('trading')
    expect(getDesk('nope')).toBeUndefined()
    expect(deskByHotkey(8)).toBeUndefined()
  })
})
