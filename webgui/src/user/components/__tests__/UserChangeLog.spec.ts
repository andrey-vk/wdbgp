import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import UserChangeLog from '../UserChangeLog.vue'
import type { UserChangeEntry, UserChangeList } from '@/types/change-log'

const i18n = createI18n({
  legacy: false,
  locale: 'en',
  messages: {
    en: {
      'user.change_log_title': 'Change history',
      'user.change_log_empty': 'No changes.',
      'user.change_log_hint': 'Hint.',
      'user.change_log_by_self': 'You',
      'user.change_log_by_admin': 'Administrator',
      'user.change_log_by_feed': 'Feed sync: {feed}',
      'user.change_log_selections': 'Selection changed in {mode}',
      'user.change_log_feed_sync': 'Services changed in {mode}',
      'user.change_log_mode_moved': 'Mode changed from {from} to {to}',
      'user.change_log_filter_mode': 'Route filter mode changed from {from} to {to}',
      'user.change_log_fm_global': 'global',
      'user.change_log_fm_extend': 'extend',
      'user.change_log_routes': 'Route filters changed',
      'user.change_log_added': 'Added: {items}',
      'user.change_log_removed': 'Removed: {items}',
      'user.change_log_omitted': ' and {count} more',
    },
  },
})

function list(p: Partial<UserChangeList> = {}): UserChangeList {
  return { categories: [], services: [], routes: [], omitted: 0, ...p }
}

function entry(p: Partial<UserChangeEntry>): UserChangeEntry {
  return { at: 1700000000, source: 'self', kind: 'selections', added: list(), removed: list(), ...p }
}

function mountLog(entries: UserChangeEntry[]) {
  return mount(UserChangeLog, {
    props: { entries },
    global: { plugins: [i18n] },
  })
}

describe('UserChangeLog', () => {
  it('shows an empty state when there are no changes', () => {
    const w = mountLog([])
    expect(w.find('[data-testid="change-log-empty"]').text()).toBe('No changes.')
    expect(w.findAll('[data-testid="change-log-entry"]')).toHaveLength(0)
  })

  it('attributes a self selection change and lists added categories', () => {
    const w = mountLog([entry({ mode: 'Default', added: list({ categories: ['AI'] }) })])
    const text = w.find('[data-testid="change-log-entry"]').text()
    expect(text).toContain('You')
    expect(text).toContain('Selection changed in Default')
    expect(text).toContain('Added: AI')
    expect(text).not.toContain('Removed:')
  })

  it('labels admin changes without any address', () => {
    const w = mountLog([entry({ source: 'admin', kind: 'mode', from: 'Default', to: 'Lab' })])
    const text = w.find('[data-testid="change-log-entry"]').text()
    expect(text).toContain('Administrator')
    expect(text).toContain('Mode changed from Default to Lab')
  })

  it('names the feed and renders services as category / service', () => {
    const w = mountLog([
      entry({
        source: 'feed_sync',
        kind: 'feed_sync',
        feed_name: 'opencck',
        mode: 'Default',
        added: list({ services: [{ category: 'AI', service: 'OpenAI' }] }),
        removed: list({ services: [{ category: 'TV', service: 'Netflix' }] }),
      }),
    ])
    const text = w.find('[data-testid="change-log-entry"]').text()
    expect(text).toContain('Feed sync: opencck')
    expect(text).toContain('Services changed in Default')
    expect(text).toContain('Added: AI / OpenAI')
    expect(text).toContain('Removed: TV / Netflix')
  })

  it('translates filter-mode values and notes omitted items', () => {
    const w = mountLog([
      entry({ kind: 'filter_mode', from: 'global', to: 'extend' }),
      entry({ kind: 'route_filters', removed: list({ routes: ['deny 10.0.0.0/8'], omitted: 3 }) }),
    ])
    const entries = w.findAll('[data-testid="change-log-entry"]')
    expect(entries[0].text()).toContain('Route filter mode changed from global to extend')
    expect(entries[1].text()).toContain('Route filters changed')
    expect(entries[1].text()).toContain('Removed: deny 10.0.0.0/8 and 3 more')
  })
})
