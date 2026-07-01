import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import PrimeVue from 'primevue/config'
import SettingField from '../SettingField.vue'
import type { SettingMeta } from '@/admin/settingsMeta'

const i18n = createI18n({
  legacy: false,
  locale: 'en',
  messages: {
    en: {
      'settings.click_to_override': 'System default, click to override',
      'settings.revert_default': 'Revert to default',
      'settings.env_override_hint': 'Set via environment variable',
      'settings.requires_restart': 'Requires restart',
      'settings.on': 'On',
      'settings.off': 'Off',
      'settings.empty': 'empty',
      'test.bool': 'Test Bool',
      'test.bool_hint': 'Test bool hint',
      'test.int': 'Test Int',
      'test.int_hint': 'Test int hint',
      'test.str': 'Test String',
      'test.str_hint': 'Test string hint',
      'test.sel': 'Test Select',
      'test.sel_hint': 'Test select hint',
      'opt.a': 'Option A',
      'opt.b': 'Option B',
      'settings.password_set': 'Set',
      'settings.password_not_set': 'Not set',
      'test.pwd': 'Test Password',
      'test.pwd_hint': 'Test password hint',
    },
  },
})

const boolMeta: SettingMeta = { label: 'test.bool', hint: 'test.bool_hint', type: 'bool' }
const intMeta: SettingMeta = { label: 'test.int', hint: 'test.int_hint', type: 'number' }
const stringMeta: SettingMeta = { label: 'test.str', hint: 'test.str_hint', type: 'string' }
const selectMeta: SettingMeta = {
  label: 'test.sel', hint: 'test.sel_hint', type: 'select',
  options: { a: 'opt.a', b: 'opt.b' },
}
const passwordMeta: SettingMeta = { label: 'test.pwd', hint: 'test.pwd_hint', type: 'password' }

function mountField(meta: SettingMeta, value: boolean | number | string | null, defaultValue: boolean | number | string, envOverride = false) {
  return mount(SettingField, {
    props: { fieldKey: 'test', meta, value, defaultValue, envOverride },
    global: { plugins: [i18n, PrimeVue] },
  })
}

describe('SettingField', () => {
  it('renders default state as clickable text (case 1)', () => {
    const wrapper = mountField(boolMeta, null, false)
    expect(wrapper.find('[data-testid="setting-default"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('Off')
  })

  it('default clickable text shows number for int type', () => {
    const wrapper = mountField(intMeta, null, 8080)
    expect(wrapper.find('[data-testid="setting-default"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('8080')
  })

  it('default clickable text shows string for string type', () => {
    const wrapper = mountField(stringMeta, null, 'hello')
    expect(wrapper.find('[data-testid="setting-default"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('hello')
  })

  it('default clickable text shows select option label', () => {
    const wrapper = mountField(selectMeta, null, 'a')
    expect(wrapper.find('[data-testid="setting-default"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('Option A')
  })

  it('clicking default text enters editing mode (case 1 → editing)', async () => {
    const wrapper = mountField(boolMeta, null, false)
    await wrapper.find('[data-testid="setting-default"] span').trigger('click')
    expect(wrapper.find('[data-testid="setting-default"]').exists()).toBe(false)
    expect(wrapper.findComponent({ name: 'ToggleSwitch' }).exists()).toBe(true)
  })

  it('renders env override as readonly text (case 2)', () => {
    const wrapper = mountField(boolMeta, true, false, true)
    expect(wrapper.find('[data-testid="setting-default"]').exists()).toBe(false)
    expect(wrapper.findComponent({ name: 'ToggleSwitch' }).exists()).toBe(false)
    // Should show ENV badge
    expect(wrapper.text()).toContain('ENV')
  })

  it('renders input when DB value exists (case 3)', () => {
    const wrapper = mountField(boolMeta, true, false)
    expect(wrapper.findComponent({ name: 'ToggleSwitch' }).exists()).toBe(true)
  })

  it('revert button emits null and returns to default', async () => {
    const wrapper = mountField(boolMeta, true, false)
    await wrapper.find('[data-testid="setting-revert"]').trigger('click')
    expect(wrapper.emitted('update:value')?.[0]).toEqual([null])
  })

  it('revert from editing returns to default (case editing → default)', async () => {
    const wrapper = mountField(boolMeta, null, false)
    // Click to enter editing mode (startEditing emits the default value)
    await wrapper.find('[data-testid="setting-default"] span').trigger('click')
    expect(wrapper.find('[data-testid="setting-default"]').exists()).toBe(false)
    // startEditing emitted defaultValue first
    expect(wrapper.emitted('update:value')?.[0]).toEqual([false])
    // Click revert
    await wrapper.find('[data-testid="setting-revert"]').trigger('click')
    expect(wrapper.emitted('update:value')?.[1]).toEqual([null])
  })

  it('emits new value on toggle', async () => {
    const wrapper = mountField(boolMeta, false, false)
    await wrapper.findComponent({ name: 'ToggleSwitch' }).vm.$emit('update:modelValue', true)
    expect(wrapper.emitted('update:value')?.[0]).toEqual([true])
  })

  it('emits number value on InputNumber change', async () => {
    const wrapper = mountField(intMeta, 8080, 8080)
    await wrapper.findComponent({ name: 'InputNumber' }).vm.$emit('update:modelValue', 3000)
    expect(wrapper.emitted('update:value')?.[0]).toEqual([3000])
  })

  it('emits string value on InputText change', async () => {
    const wrapper = mountField(stringMeta, 'old', 'default')
    await wrapper.findComponent({ name: 'InputText' }).vm.$emit('update:modelValue', 'new')
    expect(wrapper.emitted('update:value')?.[0]).toEqual(['new'])
  })

  it('emits string value on Select change', async () => {
    const wrapper = mountField(selectMeta, 'a', 'a')
    await wrapper.findComponent({ name: 'Select' }).vm.$emit('update:modelValue', 'b')
    expect(wrapper.emitted('update:value')?.[0]).toEqual(['b'])
  })

  it('shows restart tag when restart prop is true', () => {
    const wrapper = mount(SettingField, {
      props: { fieldKey: 'test', meta: boolMeta, value: true, defaultValue: false, envOverride: false, restart: true },
      global: { plugins: [i18n, PrimeVue] },
    })
    expect(wrapper.text()).toContain('Requires restart')
  })

  it('does not show restart tag when restart prop is false', () => {
    const wrapper = mountField(boolMeta, true, false)
    expect(wrapper.text()).not.toContain('Requires restart')
  })

  // --- password type tests ---

  it('password type shows "Not set" when value is null (case 1 default)', () => {
    const wrapper = mountField(passwordMeta, null, '')
    expect(wrapper.find('[data-testid="setting-default"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('Not set')
  })

  it('password type shows "Set" when value exists (case 3)', () => {
    const wrapper = mountField(passwordMeta, 'some-password', '')
    expect(wrapper.findComponent({ name: 'Password' }).exists()).toBe(true)
    // The field is in editing mode, so we should see the revert button and Password input
    expect(wrapper.find('[data-testid="setting-editing"]').exists()).toBe(true)
  })

  it('password type does not auto-commit default on edit', async () => {
    const wrapper = mountField(passwordMeta, null, '')
    await wrapper.find('[data-testid="setting-default"] span').trigger('click')
    // startEditing should NOT emit for password type — no auto-commit of default value
    expect(wrapper.emitted('update:value')).toBeFalsy()
    // Should now be in editing mode with Password component
    expect(wrapper.find('[data-testid="setting-default"]').exists()).toBe(false)
    expect(wrapper.findComponent({ name: 'Password' }).exists()).toBe(true)
  })

  it('password type renders Password component in editing mode', () => {
    const wrapper = mountField(passwordMeta, 'current', '')
    const pwd = wrapper.findComponent({ name: 'Password' })
    expect(pwd.exists()).toBe(true)
    // feedback should be disabled (not showing strength meter for admin settings)
    expect(pwd.props('feedback')).toBe(false)
  })

  it('password emits updated value on Password change', async () => {
    const wrapper = mountField(passwordMeta, 'old', '')
    await wrapper.findComponent({ name: 'Password' }).vm.$emit('update:modelValue', 'new-secret')
    expect(wrapper.emitted('update:value')?.[0]).toEqual(['new-secret'])
  })
})
