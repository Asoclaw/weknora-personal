<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { thumbnailKnowledgeFile } from '@/api/knowledge-base'
import { knowledgeImageVersion, type KnowledgeImage } from '@/utils/knowledgeImages'

const props = defineProps<{ item: KnowledgeImage }>()
const emit = defineEmits<{ (event: 'open'): void }>()
const root = ref<HTMLElement | null>(null)
const url = ref('')
const failed = ref(false)
const inView = ref(false)
let observer: IntersectionObserver | undefined
let generation = 0

function clear() {
  if (url.value) URL.revokeObjectURL(url.value)
  url.value = ''
}

watch([() => props.item.id, () => knowledgeImageVersion(props.item), inView], async () => {
  const request = ++generation
  clear()
  failed.value = false
  if (!inView.value) return
  try {
    const blob = await thumbnailKnowledgeFile(props.item.id, knowledgeImageVersion(props.item))
    if (request !== generation) return
    url.value = URL.createObjectURL(blob)
  } catch {
    if (request === generation) failed.value = true
  }
}, { immediate: true })

onMounted(() => {
  if (!('IntersectionObserver' in window)) { inView.value = true; return }
  observer = new IntersectionObserver(entries => {
    if (entries.some(entry => entry.isIntersecting)) {
      inView.value = true
      observer?.disconnect()
    }
  }, { rootMargin: '160px' })
  if (root.value) observer.observe(root.value)
})
onBeforeUnmount(() => { generation++; observer?.disconnect(); clear() })
</script>

<template>
  <button ref="root" type="button" class="knowledge-image-thumbnail" :aria-label="item.file_name || item.title"
    @click.stop="emit('open')" @keydown.stop>
    <img v-if="url && !failed" :src="url" :alt="item.file_name || item.title || ''" loading="lazy" decoding="async" @error="failed = true" />
    <t-icon v-else-if="failed" name="image-error" size="28px" :title="$t('preview.loadFailed')" />
    <t-skeleton v-else animation="gradient" :row-col="[{ width: '100%', height: '100%', type: 'rect' }]" />
  </button>
</template>

<style scoped lang="less">
.knowledge-image-thumbnail {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  height: 160px;
  margin: 0 0 10px;
  padding: 6px;
  border: 0;
  border-radius: var(--app-radius-xs);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-placeholder);
  cursor: zoom-in;
  img { width: 100%; height: 100%; object-fit: contain; }
  :deep(.t-skeleton) { width: 100%; height: 100%; }
  &:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: 2px; }
}
</style>
