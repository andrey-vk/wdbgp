import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import PrimeVue from 'primevue/config'
import BlastRadiusPreviewDialog from '../BlastRadiusPreviewDialog.vue'
import type { BlastRadiusPreview } from '@/types/blast-radius'

const i18n = createI18n({
  legacy: false,
  locale: 'en',
  messages: {
    en: {
      'blast_radius.title': 'Preview impact',
      'blast_radius.count': '{count} user(s) will see their routes change:',
      'blast_radius.v4': 'IPv4',
      'blast_radius.v6': 'IPv6',
      'blast_radius.loses_routes': 'Loses routes',
      'blast_radius.total': 'Total change: {v4} IPv4, {v6} IPv6 prefixes',
      'blast_radius.apply': 'Apply anyway',
      'dialog.no': 'No',
    },
  },
})

const preview: BlastRadiusPreview = {
  affected_users: [
    { user_id: 1, name: 'unchanged-user', before_v4: 5, before_v6: 1, after_v4: 5, after_v6: 1, lost_routes: false, changed: false },
    { user_id: 2, name: 'loses-user', before_v4: 5, before_v6: 0, after_v4: 2, after_v6: 0, lost_routes: true, changed: true },
    { user_id: 3, name: 'gains-user', before_v4: 1, before_v6: 0, after_v4: 3, after_v6: 0, lost_routes: false, changed: true },
  ],
  total_delta_v4: -1, total_delta_v6: 0,
}

function mountDialog(props: Partial<InstanceType<typeof BlastRadiusPreviewDialog>['$props']> = {}) {
  return mount(BlastRadiusPreviewDialog, {
    // appendTo="self" is not a prop of this component — it falls through
    // (single-root template) onto the real PrimeVue Dialog, keeping its
    // content in the mounted tree instead of teleported to document.body,
    // where wrapper.text()/find() can't see it.
    props: { visible: true, preview, appendTo: 'self', ...props },
    global: { plugins: [i18n, PrimeVue] },
  })
}

describe('BlastRadiusPreviewDialog', () => {
  it('renders only the users whose counts actually changed', () => {
    const wrapper = mountDialog()
    const text = wrapper.text()
    expect(text).not.toContain('unchanged-user')
    expect(text).toContain('loses-user')
    expect(text).toContain('gains-user')
    expect(text).toContain('2 user(s)')
  })

  it('shows the "loses routes" badge only for a user whose count dropped', () => {
    const wrapper = mountDialog()
    const rows = wrapper.findAll('.truncate').map(w => w.text())
    const loseRow = wrapper.html()
    expect(rows).toContain('loses-user')
    expect(rows).toContain('gains-user')
    // The badge text appears exactly once (loses-user only).
    expect((loseRow.match(/Loses routes/g) || []).length).toBe(1)
  })

  it('renders the aggregate total delta', () => {
    const wrapper = mountDialog()
    expect(wrapper.text()).toContain('Total change: -1 IPv4, 0 IPv6 prefixes')
  })

  it('emits apply when the apply button is clicked', async () => {
    const wrapper = mountDialog()
    const buttons = wrapper.findAll('button')
    const applyButton = buttons.find(b => b.text().includes('Apply anyway'))
    expect(applyButton).toBeTruthy()
    await applyButton!.trigger('click')
    expect(wrapper.emitted('apply')).toBeTruthy()
  })

  it('emits cancel and update:visible(false) when the cancel button is clicked', async () => {
    const wrapper = mountDialog()
    const buttons = wrapper.findAll('button')
    const cancelButton = buttons.find(b => b.text().includes('No'))
    expect(cancelButton).toBeTruthy()
    await cancelButton!.trigger('click')
    expect(wrapper.emitted('cancel')).toBeTruthy()
    expect(wrapper.emitted('update:visible')).toEqual([[false]])
  })
})
