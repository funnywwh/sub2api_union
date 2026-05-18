import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import UsageProgressBar from '../UsageProgressBar.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

describe('UsageProgressBar', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-03-17T00:00:00Z'))
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('showNowWhenIdle=true 且利用率为 0 时显示“现在”', () => {
    const wrapper = mount(UsageProgressBar, {
      props: {
        label: '5h',
        utilization: 0,
        resetsAt: '2026-03-17T02:30:00Z',
        showNowWhenIdle: true,
        color: 'indigo'
      }
    })

    expect(wrapper.text()).toContain('现在')
    expect(wrapper.text()).not.toContain('2h 30m')
  })

  it('showNowWhenIdle=true 但利用率大于 0 时显示倒计时', () => {
    const wrapper = mount(UsageProgressBar, {
      props: {
        label: '7d',
        utilization: 12,
        resetsAt: '2026-03-17T02:30:00Z',
        showNowWhenIdle: true,
        color: 'emerald'
      }
    })

    expect(wrapper.text()).toContain('2h 30m')
    expect(wrapper.text()).not.toContain('现在')
  })

  it('showNowWhenIdle=false 时保持原有倒计时行为', () => {
    const wrapper = mount(UsageProgressBar, {
      props: {
        label: '1d',
        utilization: 0,
        resetsAt: '2026-03-17T02:30:00Z',
        showNowWhenIdle: false,
        color: 'indigo'
      }
    })

    expect(wrapper.text()).toContain('2h 30m')
    expect(wrapper.text()).not.toContain('现在')
  })

  it('statusMode=binary 时低于 100% 显示绿色安全态', () => {
    const wrapper = mount(UsageProgressBar, {
      props: {
        label: '7d P',
        utilization: 88,
        color: 'amber',
        statusMode: 'binary'
      }
    })

    expect(wrapper.find('.bg-green-500').exists()).toBe(true)
    expect(wrapper.find('.text-green-600').exists()).toBe(true)
  })

  it('statusMode=binary 时超过 100% 显示红色超额态', () => {
    const wrapper = mount(UsageProgressBar, {
      props: {
        label: '7d P',
        utilization: 132,
        color: 'amber',
        statusMode: 'binary'
      }
    })

    expect(wrapper.find('.bg-red-500').exists()).toBe(true)
    expect(wrapper.find('.text-red-600').exists()).toBe(true)
  })

  it('displayText 存在时优先显示自定义文本', () => {
    const wrapper = mount(UsageProgressBar, {
      props: {
        label: '7d P',
        utilization: 132,
        color: 'amber',
        displayText: '+12h'
      }
    })

    expect(wrapper.text()).toContain('+12h')
    expect(wrapper.text()).not.toContain('132%')
  })
})
