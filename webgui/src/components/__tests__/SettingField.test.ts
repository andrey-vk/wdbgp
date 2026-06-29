import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import SettingField from '@/components/SettingField.vue'
import PrimeVue from 'primevue/config'

// Mock vue-i18n
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

// Stub FormField
const FormFieldStub = {
  template: '<div class="form-field"><slot name="tags" /><slot :value="{}" :disabled="false" :input-id="\'test\'" /></div>',
  props: ['label', 'hint', 'inputId']
}

describe('SettingField', () => {
  const defaultMount = (props = {}) => mount(SettingField, {
    props: { inputId: 'test', defaultValue: 'defaultVal', ...props },
    global: {
      plugins: [PrimeVue],
      stubs: { FormField: FormFieldStub }
    }
  })

  it('renders checkbox when defaultValue provided', () => {
    const wrapper = defaultMount()
    expect(wrapper.findComponent({ name: 'Checkbox' }).exists()).toBe(true)
  })

  it('hides checkbox when no defaultValue', () => {
    const wrapper = mount(SettingField, {
      props: { inputId: 'test' },
      global: { plugins: [PrimeVue], stubs: { FormField: FormFieldStub } }
    })
    expect(wrapper.findComponent({ name: 'Checkbox' }).exists()).toBe(false)
  })

  it('checkbox checked by default when modelValue is null', () => {
    const wrapper = defaultMount({ modelValue: null })
    const cb = wrapper.findComponent({ name: 'Checkbox' })
    expect(cb.props('modelValue')).toBe(true)
  })

  it('checkbox unchecked when modelValue differs from default', () => {
    const wrapper = defaultMount({ modelValue: 'customVal', defaultValue: 'defaultVal' })
    const cb = wrapper.findComponent({ name: 'Checkbox' })
    expect(cb.props('modelValue')).toBe(false)
  })

  it('emits null when checkbox checked (revert to default)', async () => {
    const wrapper = defaultMount({ modelValue: 'customVal', defaultValue: 'defaultVal' })
    const input = wrapper.findComponent({ name: 'Checkbox' }).find('input')
    await input.setValue(true)
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([null])
  })

  it('emits defaultValue when checkbox unchecked (enter custom)', async () => {
    const wrapper = defaultMount({ modelValue: null, defaultValue: '28800' })
    const input = wrapper.findComponent({ name: 'Checkbox' }).find('input')
    await input.setValue(false)
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['28800'])
  })
})
