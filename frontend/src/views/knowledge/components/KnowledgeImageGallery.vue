<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { listKnowledgeFiles, previewKnowledgeFile, thumbnailKnowledgeFile, type ListKnowledgeFilesParams } from '@/api/knowledge-base'
import PicturePreview from '@/components/picture-preview.vue'
import { getPreviewMimeType, resolveFilePreviewExt } from '@/utils/filePreview'
import { knowledgeImageVersion, type KnowledgeImage } from '@/utils/knowledgeImages'
import { useI18n } from 'vue-i18n'

const props = defineProps<{ kbId: string; initial: KnowledgeImage; filters: Omit<ListKnowledgeFilesParams, 'page' | 'page_size'>; canDownload: boolean }>()
const emit = defineEmits<{ (event: 'close'): void }>()
const { t } = useI18n()
const items = ref<KnowledgeImage[]>([props.initial])
const index = ref(0)
const total = ref(1)
const ready = ref(false)
const urls = ref<Record<string, string>>({})
const thumbnails = ref<Record<string, string>>({})
const failed = ref(false)
let page = 0
let listed: KnowledgeImage[] = []
let closed = false
let pageLoading: Promise<void> | undefined
const pending = new Set<string>()
const thumbPending = new Set<string>()
const thumbFailures = new Set<string>()
const emptyThumbnail = 'data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7'
let retryTask: () => Promise<void> = () => loadImage(current.value)
const current = computed(() => items.value[index.value] || props.initial)
const key = (item: KnowledgeImage) => `${item.id}:${knowledgeImageVersion(item)}`
const images = computed(() => items.value.map(item => ({ mainImage: urls.value[key(item)] || '', thumbnail: thumbnails.value[key(item)] || emptyThumbnail, download: props.canDownload })))
const title = computed(() => `${current.value.file_name || current.value.title || ''} · ${index.value + 1}/${total.value}`)

async function loadPage() {
  if (pageLoading) return pageLoading
  pageLoading = (async () => {
    const result = await listKnowledgeFiles(props.kbId, {
      ...props.filters, file_type: 'image', page: page + 1, page_size: 50,
    })
    if (closed) return
    page++
    total.value = result.total
    const selected = current.value.id
    listed = page === 1 ? result.data : [...listed, ...result.data]
    const nextIndex = listed.findIndex(item => item.id === selected)
    if (ready.value || nextIndex >= 0) {
      items.value = listed
      if (nextIndex >= 0) index.value = nextIndex
    }
  })().finally(() => { pageLoading = undefined })
  return pageLoading
}

async function loadImage(item: KnowledgeImage) {
  const id = key(item)
  if (urls.value[id] || pending.has(id)) return
  pending.add(id)
  try {
    const blob = await previewKnowledgeFile(item.id)
    if (closed) return
    const ext = resolveFilePreviewExt(item.original_file_name || item.file_name || item.title, item.file_type)
    urls.value[id] = URL.createObjectURL(new Blob([blob], { type: getPreviewMimeType(ext) || blob.type }))
    const keep = new Set(items.value.slice(Math.max(0, index.value - 1), index.value + 2).map(key))
    for (const old of Object.keys(urls.value)) if (!keep.has(old)) { URL.revokeObjectURL(urls.value[old]); delete urls.value[old] }
  } catch {
    if (!closed && current.value.id === item.id) failed.value = true
  } finally { pending.delete(id) }
}

async function loadThumbnail(item: KnowledgeImage) {
  const id = key(item)
  if (thumbnails.value[id] || thumbPending.has(id) || thumbFailures.has(id)) return
  thumbPending.add(id)
  try {
    const blob = await thumbnailKnowledgeFile(item.id, knowledgeImageVersion(item))
    if (closed) return
    thumbnails.value[id] = URL.createObjectURL(blob)
    const keep = new Set(items.value.slice(Math.max(0, index.value - 3), index.value + 4).map(key))
    for (const old of Object.keys(thumbnails.value)) if (!keep.has(old)) { URL.revokeObjectURL(thumbnails.value[old]); delete thumbnails.value[old] }
  } catch { thumbFailures.add(id) }
  finally { thumbPending.delete(id) }
}

async function changeIndex(next: number, context: { trigger: string }) {
  if (!ready.value) return
  if (context.trigger === 'next' && next === index.value && items.value.length < total.value) {
    const previousLength = items.value.length
    try { await loadPage(); if (!closed) index.value = Math.min(previousLength, items.value.length - 1) }
    catch { failed.value = true; retryTask = () => changeIndex(next, context) }
  } else { index.value = next }
}

watch(current, async item => {
  failed.value = false
  retryTask = () => loadImage(item)
  for (const neighbor of items.value.slice(Math.max(0, index.value - 3), index.value + 4)) void loadThumbnail(neighbor)
  await loadImage(item)
  if (closed || current.value.id !== item.id) return
  const neighbors = items.value.slice(Math.max(0, index.value - 1), index.value + 2)
  const keep = new Set(neighbors.map(key))
  for (const id of Object.keys(urls.value)) if (!keep.has(id)) { URL.revokeObjectURL(urls.value[id]); delete urls.value[id] }
  for (const neighbor of neighbors) void loadImage(neighbor)
}, { immediate: true })

async function initialize() {
  try {
    const selected = props.initial.id
    do { await loadPage() } while (!closed && !listed.some(item => item.id === selected) && listed.length < total.value)
    if (closed) return
    const selectedIndex = items.value.findIndex(item => item.id === selected)
    if (selectedIndex < 0) { emit('close'); return }
    index.value = selectedIndex
    ready.value = true
  } catch { if (!closed) { ready.value = true; failed.value = true; retryTask = initialize } }
}
onMounted(initialize)
onBeforeUnmount(() => {
  closed = true
  for (const url of Object.values(urls.value)) URL.revokeObjectURL(url)
  for (const url of Object.values(thumbnails.value)) URL.revokeObjectURL(url)
})
</script>

<template>
  <PicturePreview :review-img="true" :review-images="images" :review-index="index" :review-title="title" protect-original
    @close-pre-img="emit('close')" @index-change="changeIndex" />
  <Teleport to="body">
    <div v-if="failed" class="knowledge-image-retry">
      <span>{{ t('preview.loadFailed') }}</span>
      <button type="button" @click="failed = false; retryTask()">{{ t('preview.retry') }}</button>
    </div>
  </Teleport>
</template>

<style scoped>
.knowledge-image-retry { position: fixed; inset: 45% auto auto 50%; transform: translateX(-50%); z-index: 3202; display: flex; gap: 12px; align-items: center; padding: 12px 16px; border-radius: var(--app-radius-md); background: var(--td-bg-color-container); color: var(--td-text-color-primary); }
.knowledge-image-retry button { background: transparent; border: 0; color: var(--td-brand-color); cursor: pointer; }
</style>
