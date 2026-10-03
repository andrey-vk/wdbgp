import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import PrimeVue from 'primevue/config'

const mockGet = vi.fn()

vi.mock('@/api/client', () => ({
  default: {
    get: mockGet,
    interceptors: { response: { use: vi.fn() } },
  },
}))

const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: {} } })

function stubPrimeVueComponents() {
  return {
    InputText: {
      props: ['modelValue'],
      emits: ['update:modelValue'],
      template: '<input class="stub-input" v-bind="$attrs" :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />',
    },
    Button: { props: ['label'], template: '<button class="stub-btn" @click="$emit(\'click\')">{{ label }}<slot /></button>' },
    Paginator: {
      props: ['rows', 'first', 'totalRecords'],
      template: '<div class="stub-paginator" data-testid="paginator" @click="$emit(\'page\', { first: 50, rows: 50 })" />',
    },
  }
}

async function mountAuditLogPage() {
  const AuditLogPage = (await import('../AuditLogPage.vue')).default
  const wrapper = mount(AuditLogPage, {
    global: {
      plugins: [i18n, PrimeVue],
      stubs: stubPrimeVueComponents(),
    },
  })
  await wrapper.vm.$nextTick()
  await new Promise((r) => setTimeout(r, 0))
  return wrapper
}

describe('AuditLogPage', () => {
  beforeEach(() => {
    mockGet.mockReset()
  })

  it('fetches entries on mount with default pagination and renders them', async () => {
    mockGet.mockResolvedValue({
      data: {
        entries: [
          {
            id: 1, recorded_at: '2026-01-01T00:00:00Z', actor: 'admin:1.2.3.4', user_agent: 'curl',
            action: 'feed.enabled_changed', object_type: 'feed', object_id: '7',
            before: '{"enabled":true}', after: '{"enabled":false}',
          },
        ],
        total: 1,
      },
    })

    const wrapper = await mountAuditLogPage()

    expect(mockGet).toHaveBeenCalledWith('/admin/audit-log', { params: { limit: 50, offset: 0 } })
    const rows = wrapper.findAll('[data-testid="audit-log-row"]')
    expect(rows).toHaveLength(1)
    expect(rows[0].text()).toContain('admin:1.2.3.4')
    expect(rows[0].text()).toContain('feed.enabled_changed')
    expect(rows[0].text()).toContain('feed/7')
  })

  it('shows the empty-state message when no entries match', async () => {
    mockGet.mockResolvedValue({ data: { entries: [], total: 0 } })

    const wrapper = await mountAuditLogPage()

    expect(wrapper.text()).toContain('audit_log.empty')
    expect(wrapper.find('[data-testid="audit-log-row"]').exists()).toBe(false)
  })

  it('re-fetches with the entered filter params when the filter button is clicked', async () => {
    mockGet.mockResolvedValue({ data: { entries: [], total: 0 } })
    const wrapper = await mountAuditLogPage()
    mockGet.mockClear()

    await wrapper.find('#audit-actor-input').setValue('user:42')
    await wrapper.find('#audit-action-input').setValue('user.mode_changed')
    await wrapper.find('[data-testid="audit-log-filter-button"]').trigger('click')
    await new Promise((r) => setTimeout(r, 0))

    expect(mockGet).toHaveBeenCalledWith('/admin/audit-log', {
      params: { limit: 50, offset: 0, actor: 'user:42', action: 'user.mode_changed' },
    })
  })

  it('resets to the first page when filters are applied', async () => {
    mockGet.mockResolvedValue({
      data: { entries: Array.from({ length: 1 }, (_, i) => ({ id: i, recorded_at: '2026-01-01T00:00:00Z', actor: 'a', user_agent: '', action: 'a', object_type: 't', object_id: '1', before: '', after: '' })), total: 60 },
    })
    const wrapper = await mountAuditLogPage()

    // Advance a page via the stubbed paginator.
    await wrapper.find('[data-testid="paginator"]').trigger('click')
    await new Promise((r) => setTimeout(r, 0))
    expect(mockGet).toHaveBeenLastCalledWith('/admin/audit-log', { params: { limit: 50, offset: 50 } })

    mockGet.mockClear()
    await wrapper.find('[data-testid="audit-log-filter-button"]').trigger('click')
    await new Promise((r) => setTimeout(r, 0))
    expect(mockGet).toHaveBeenLastCalledWith('/admin/audit-log', { params: { limit: 50, offset: 0 } })
  })

  it('toggles the before/after detail panel for a row', async () => {
    mockGet.mockResolvedValue({
      data: {
        entries: [
          { id: 1, recorded_at: '2026-01-01T00:00:00Z', actor: 'a', user_agent: '', action: 'a', object_type: 't', object_id: '1', before: '{"x":1}', after: '{"x":2}' },
        ],
        total: 1,
      },
    })
    const wrapper = await mountAuditLogPage()

    expect(wrapper.find('[data-testid="audit-log-details"]').exists()).toBe(false)

    await wrapper.find('[data-testid="audit-log-toggle"]').trigger('click')
    expect(wrapper.find('[data-testid="audit-log-details"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="audit-log-details"]').text()).toContain('"x":1')
    expect(wrapper.find('[data-testid="audit-log-details"]').text()).toContain('"x":2')

    await wrapper.find('[data-testid="audit-log-toggle"]').trigger('click')
    expect(wrapper.find('[data-testid="audit-log-details"]').exists()).toBe(false)
  })

  it('shows the error page when the initial load fails', async () => {
    mockGet.mockRejectedValue(new Error('network error'))
    const wrapper = await mountAuditLogPage()

    expect(wrapper.text()).toContain('error.generic_title')
    expect(wrapper.find('[data-testid="audit-log-row"]').exists()).toBe(false)
  })
})
