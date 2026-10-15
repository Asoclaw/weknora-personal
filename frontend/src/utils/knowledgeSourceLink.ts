export interface SourceLinkKnowledge {
  type?: string
  channel?: string
  source?: string
  metadata?: unknown
}

export function resolveKnowledgeSourceLink(item: SourceLinkKnowledge): string {
  let value: unknown = item.type === 'url' ? item.source : ''
  if (item.channel === 'tencent_docs') {
    let metadata = item.metadata
    if (typeof metadata === 'string') {
      try { metadata = JSON.parse(metadata) } catch { return '' }
    }
    value = metadata && typeof metadata === 'object'
      ? (metadata as Record<string, unknown>).source_url : ''
  }
  if (typeof value !== 'string') return ''
  const trimmed = value.trim()
  try {
    const parsed = new URL(trimmed)
    return ['http:', 'https:'].includes(parsed.protocol) && parsed.hostname && !parsed.username && !parsed.password
      ? trimmed : ''
  } catch { return '' }
}
