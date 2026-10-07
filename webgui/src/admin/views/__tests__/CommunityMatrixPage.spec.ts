import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import PrimeVue from 'primevue/config'
import CommunityMatrixPage from '../CommunityMatrixPage.vue'
import type { CommunityMatrixResponse } from '@/types/community-matrix'

const { mockGet, mockPost } = vi.hoisted(() => ({ mockGet: vi.fn(), mockPost: vi.fn() }))

vi.mock('@/api/client', () => ({
  default: {
    get: mockGet,
    post: mockPost,
    interceptors: { response: { use: vi.fn() } },
  },
}))

const mockToastAdd = vi.fn()
vi.mock('primevue/usetoast', () => ({
  useToast: () => ({ add: mockToastAdd }),
}))

const i18n = createI18n({
  legacy: false,
  locale: 'en',
  messages: {
    en: {
      'community_matrix.divergent_count': '{count} categories differ between modes',
      'community_matrix.generated': 'Generated {count} missing numbers',
      'community_matrix.generate_failed': 'Failed to generate missing numbers',
    },
  },
})

const matrix: CommunityMatrixResponse = {
  modes: [
    { id: 1, name: 'Default', enabled: true },
    { id: 2, name: 'Lab', enabled: true },
  ],
  categories: [
    { category: 'ai', values: { '1': 10000, '2': 10000 }, service_divergence: 0, divergent: false },
    { category: 'tv', values: { '1': 20000, '2': 30000 }, service_divergence: 0, divergent: true },
    { category: 'news', values: { '1': 40000, '2': null }, service_divergence: 0, divergent: true },
  ],
}

async function mountPage() {
  mockGet.mockResolvedValue({ data: matrix })
  const wrapper = mount(CommunityMatrixPage, {
    global: {
      plugins: [i18n, PrimeVue],
      stubs: {
        InputText: {
          props: ['modelValue'],
          emits: ['update:modelValue'],
          template: '<input class="stub-input" :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />',
        },
        Checkbox: {
          props: ['modelValue'],
          emits: ['update:modelValue'],
          template: '<input type="checkbox" class="stub-checkbox" :checked="modelValue" @change="$emit(\'update:modelValue\', $event.target.checked)" />',
        },
        Button: { props: ['label', 'loading'], template: '<button class="stub-btn">{{ label }}</button>' },
      },
    },
  })
  await new Promise((r) => setTimeout(r, 0))
  await wrapper.vm.$nextTick()
  return wrapper
}

describe('CommunityMatrixPage', () => {
  beforeEach(() => {
    mockGet.mockReset()
    mockPost.mockReset()
    mockToastAdd.mockReset()
  })

  it('loads the matrix and shows one row per category with each mode value', async () => {
    const wrapper = await mountPage()
    expect(mockGet).toHaveBeenCalledWith('/admin/communities/matrix')
    expect(wrapper.findAll('[data-testid="matrix-row"]')).toHaveLength(3)
    expect(wrapper.find('[data-testid="matrix-cell-tv-2"]').text()).toBe('30000')
  })

  it('flags divergent categories and marks a missing mode value', async () => {
    const wrapper = await mountPage()
    const rows = wrapper.findAll('[data-testid="matrix-row"]')
    expect(rows[0].attributes('data-divergent')).toBe('false')
    expect(rows[1].attributes('data-divergent')).toBe('true')
    expect(wrapper.find('[data-testid="matrix-cell-news-2"]').text()).toBe('—')
    expect(wrapper.find('[data-testid="matrix-divergent-count"]').text()).toContain('2')
  })

  it('narrows the rows to divergent categories and to a name filter', async () => {
    const wrapper = await mountPage()
    await wrapper.find('[data-testid="matrix-only-divergent"]').setValue(true)
    expect(wrapper.findAll('[data-testid="matrix-row"]')).toHaveLength(2)
    await wrapper.find('.stub-input').setValue('tv')
    expect(wrapper.findAll('[data-testid="matrix-row"]')).toHaveLength(1)
  })

  it('generates missing numbers on demand and reloads the matrix', async () => {
    const wrapper = await mountPage()
    mockPost.mockResolvedValue({ data: { generated: 2 } })
    mockGet.mockClear()
    mockGet.mockResolvedValue({ data: matrix })

    await wrapper.find('[data-testid="matrix-generate"]').trigger('click')
    await new Promise((r) => setTimeout(r, 0))

    expect(mockPost).toHaveBeenCalledTimes(1)
    expect(mockPost).toHaveBeenCalledWith('/admin/communities/matrix/generate')
    expect(mockGet).toHaveBeenCalledTimes(1)
    expect(mockToastAdd).toHaveBeenCalledWith(expect.objectContaining({ severity: 'success' }))
  })
})
