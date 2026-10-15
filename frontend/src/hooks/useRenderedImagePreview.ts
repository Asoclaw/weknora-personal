import { nextTick, onBeforeUnmount, ref, watch, type Ref } from 'vue'
import { displayedImages } from '@/utils/knowledgeImages'

export function useRenderedImagePreview(root: Ref<HTMLElement | null>, active: () => boolean, selector = 'img') {
  const visible = ref(false)
  const urls = ref<string[]>([])
  const index = ref(0)
  let observer: MutationObserver | undefined
  let generation = 0

  function markImages() {
    root.value?.querySelectorAll<HTMLImageElement>(selector).forEach(img => {
      if (img.closest('.t-image-viewer')) return
      img.tabIndex = 0
      img.setAttribute('role', 'button')
      img.style.cursor = 'zoom-in'
    })
  }

  function open(event: Event) {
    if (!active() || !(event.target instanceof HTMLImageElement) || !root.value) return
    const images = displayedImages(root.value, selector)
    const selected = images.indexOf(event.target)
    if (selected < 0) return
    event.preventDefault()
    event.stopPropagation()
    urls.value = images.map(img => img.currentSrc || img.src)
    index.value = selected
    visible.value = true
  }

  function keydown(event: KeyboardEvent) {
    if (event.key === 'Enter' || event.key === ' ') open(event)
  }

  function close() { visible.value = false }

  watch([root, active], async () => {
    const current = ++generation
    observer?.disconnect()
    if (!active()) { close(); return }
    await nextTick()
    if (current !== generation || !root.value || !active()) return
    markImages()
    observer = new MutationObserver(markImages)
    observer.observe(root.value, { childList: true, subtree: true, attributes: true, attributeFilter: ['src', 'srcset'] })
  }, { immediate: true, flush: 'post' })

  onBeforeUnmount(() => { generation++; observer?.disconnect() })
  return { visible, urls, index, open, keydown, close }
}
