import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import FormField from '@/components/FormField.vue'

// Mock vue-i18n so FieldHint can call useI18n() without crashing
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

describe('FormField', () => {
  it('renders label text via for attribute on <label>', () => {
    const wrapper = mount(FormField, {
      props: { label: 'Test Label', inputId: 'test-input' },
    })
    const label = wrapper.find('label')
    expect(label.attributes('for')).toBe('test-input')
    expect(label.text()).toBe('Test Label')
  })

  it('renders hint button when hint prop is set', () => {
    // Need to provide the inject context for FieldHint's popover mechanism
    const wrapper = mount(FormField, {
      props: { label: 'Test', inputId: 'test', hint: 'test_hint' },
      global: {
        stubs: {
          // Stub the Popover component to avoid PrimeVue dependency
          Popover: { template: '<div class="popover-stub"><slot /></div>' },
        },
        provide: {
          activePopoverId: { value: null },
          hideActivePopover: { value: null },
        },
      },
    })
    // FieldHint component renders a button with type="button"
    expect(wrapper.find('button[type="button"]').exists()).toBe(true)
  })

  it('does NOT render hint button when hint is not set', () => {
    const wrapper = mount(FormField, {
      props: { label: 'Test', inputId: 'test' },
    })
    expect(wrapper.find('button[type="button"]').exists()).toBe(false)
  })

  it('renders default slot content', () => {
    const wrapper = mount(FormField, {
      props: { label: 'Test', inputId: 'test' },
      slots: { default: '<input id="test" type="text" />' },
    })
    expect(wrapper.find('input[type="text"]').exists()).toBe(true)
  })

  it('renders tags slot content when provided', () => {
    const wrapper = mount(FormField, {
      props: { label: 'Test', inputId: 'test' },
      slots: { tags: '<span class="tag-badge">ENV</span>' },
    })
    expect(wrapper.find('.tag-badge').exists()).toBe(true)
    expect(wrapper.find('.tag-badge').text()).toBe('ENV')
  })
})
