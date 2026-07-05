export function formatDateTime(raw: string | undefined | null): string {
  if (!raw) return '—'
  try {
    const d = new Date(raw.replace(' ', 'T') + (raw.includes('Z') ? '' : 'Z'))
    if (isNaN(d.getTime())) return raw
    return d.toLocaleString()
  } catch {
    return raw
  }
}
