<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, watch } from 'vue'
import { useI18n } from 'vue-i18n'

const props = withDefaults(defineProps<{
  reviewImg: boolean
  reviewUrl?: string
  reviewUrls?: string[]
  reviewImages?: Array<{ mainImage: string; thumbnail?: string; download?: boolean }>
  reviewIndex?: number
  reviewTitle?: string
  allowDownload?: boolean
  attachTo?: string | HTMLElement
  protectOriginal?: boolean
}>(), { reviewUrl: '', reviewUrls: () => [], allowDownload: true, attachTo: 'body' })
const emit = defineEmits<{
  (event: 'closePreImg'): void
  (event: 'index-change', index: number, context: { trigger: 'prev' | 'next' | 'current' }): void
}>()
const { t } = useI18n()
const images = computed(() => props.reviewImages || (props.reviewUrls.length ? props.reviewUrls : props.reviewUrl ? [props.reviewUrl] : [])
  .map(mainImage => ({ mainImage, download: props.allowDownload })))
let previousFocus: HTMLElement | null = null
const close = () => emit('closePreImg')
function restoreFocus() {
  if (previousFocus?.isConnected) previousFocus.focus({ preventScroll: true })
  previousFocus = null
}

function previewRoot() {
  return document.querySelector<HTMLElement>('.t-image-viewer-preview-image:not(.t-is-hide)')
}

function markControls() {
  previewRoot()?.querySelectorAll<HTMLElement>('.t-image-viewer__modal-icon').forEach(control => {
    // Keep original files on the guarded download path. In particular, do not
    // turn a displayed SVG blob into an executable same-origin document.
    if (props.protectOriginal && control.querySelector('.t-icon-image')) { control.hidden = true; control.style.display = 'none'; return }
    control.tabIndex = 0
    control.setAttribute('role', 'button')
    const icon = control.querySelector('svg')?.getAttribute('class')?.match(/t-icon-([\w-]+)/)?.[1]
    const label = control.className.includes('close-bt') ? t('common.close')
      : control.className.includes('prev-bt') ? t('preview.imagePrevious')
      : control.className.includes('next-bt') ? t('preview.imageNext')
      : control.textContent?.trim() || icon?.replaceAll('-', ' ') || t('preview.fullscreen')
    control.setAttribute('aria-label', label)
  })
}

function keyboard(event: KeyboardEvent) {
  if (!props.reviewImg) return
  if (event.key === 'Escape') {
    // A nested image overlay must not also close the document drawer.
    event.preventDefault()
    event.stopImmediatePropagation()
    close()
  } else if ((event.key === 'Enter' || event.key === ' ') && document.activeElement?.classList.contains('t-image-viewer__modal-icon')) {
    event.preventDefault()
    event.stopImmediatePropagation()
    ;(document.activeElement as HTMLElement).click()
  } else if (event.key === 'Tab') {
    const controls = [...Array.from(previewRoot()?.querySelectorAll<HTMLElement>('[tabindex="0"], button, a[href]') || []),
      ...Array.from(document.querySelectorAll<HTMLElement>('.knowledge-image-retry button'))]
      .filter(control => control.getClientRects().length && !control.hasAttribute('disabled'))
    if (!controls.length) return
    const current = controls.indexOf(document.activeElement as HTMLElement)
    const next = event.shiftKey ? (current <= 0 ? controls.length - 1 : current - 1) : (current + 1) % controls.length
    event.preventDefault()
    event.stopImmediatePropagation()
    controls[next].focus({ preventScroll: true })
  }
}

watch(() => props.reviewImg, async visible => {
  if (visible) {
    previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
    window.addEventListener('keydown', keyboard, true)
    await nextTick()
    markControls()
  } else {
    window.removeEventListener('keydown', keyboard, true)
    restoreFocus()
  }
}, { immediate: true })
watch(() => images.value.length, async () => { await nextTick(); if (props.reviewImg) markControls() })
onBeforeUnmount(() => { window.removeEventListener('keydown', keyboard, true); restoreFocus() })
</script>
<template>
  <t-image-viewer :visible="reviewImg" :attach="attachTo" :z-index="3200" closeOnOverlay closeOnEscKeydown draggable
    :images="images" :index="reviewIndex" :title="reviewTitle"
    @close="close" @index-change="(index: number, context: { trigger: 'prev' | 'next' | 'current' }) => emit('index-change', index, context)">
    <template #trigger><span hidden /></template>
  </t-image-viewer>
</template>
<style scoped lang="less"></style>
