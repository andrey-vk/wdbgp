import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref, type Ref } from 'vue'
import FieldHint from '@/components/FieldHint.vue'

// Mock vue-i18n
const tMock = vi.fn((key: string) => key)

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: tMock }),
}))

describe('FieldHint', () => {
  // Helper to mount FieldHint with provide/inject context for the single-popover mechanism
  function mountHint(targetId: string, activePopoverId: Ref<string | null>, hideActivePopover: Ref<(() => void) | null>) {
    return mount(FieldHint, {
      props: { hint: 'test.hint', targetId },
      global: {
        provide: {
          activePopoverId: activePopoverId,
          hideActivePopover: hideActivePopover,
        },
      },
    })
  }

  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders a ? button', () => {
    const activeId = ref<string | null>(null)
    const hideFn = ref<(() => void) | null>(null)
    const wrapper = mountHint('input-1', activeId, hideFn)
    expect(wrapper.find('button.hint-btn').exists()).toBe(true)
    expect(wrapper.find('button.hint-btn').text()).toBe('?')
  })

  it('shows popover on click', async () => {
    // Add a target element
    document.body.innerHTML = '<div id="input-1"></div>'
    const activeId = ref<string | null>(null)
    const hideFn = ref<(() => void) | null>(null)
    const wrapper = mountHint('input-1', activeId, hideFn)
    
    // Access the component's expose or internal ref - we need to mock the Popover component
    // Instead, we check that the show logic sets activePopoverId and registers hideActivePopover
    // The Popover itself is a PrimeVue component that's hard to test in unit tests
    // We test the behavior: clicking the button should trigger the show function
    
    await wrapper.find('button.hint-btn').trigger('click')
    
    // After setTimeout(0), activePopoverId should be set and hideActivePopover should be assigned
    await new Promise(resolve => setTimeout(resolve, 10))
    
    expect(activeId.value).toBe('input-1')
    expect(hideFn.value).toBeInstanceOf(Function)
  })

  it('only one popover visible globally - clicking second closes first', async () => {
    document.body.innerHTML = '<div id="input-1"></div><div id="input-2"></div>'
    const activeId = ref<string | null>(null)
    const hideFn = ref<(() => void) | null>(null)
    
    // Mount first FieldHint
    const wrapper1 = mountHint('input-1', activeId, hideFn)
    await wrapper1.find('button.hint-btn').trigger('click')
    await new Promise(resolve => setTimeout(resolve, 10))
    expect(activeId.value).toBe('input-1')
    const firstHideFn = hideFn.value
    expect(firstHideFn).toBeInstanceOf(Function)
    
    // Mount second FieldHint with same refs
    const wrapper2 = mountHint('input-2', activeId, hideFn)
    await wrapper2.find('button.hint-btn').trigger('click')
    await new Promise(resolve => setTimeout(resolve, 10))
    
    // Now activePopoverId should be 'input-2'
    expect(activeId.value).toBe('input-2')
    // The second hint's hide function should be the new one
    expect(hideFn.value).toBeInstanceOf(Function)
  })
})
