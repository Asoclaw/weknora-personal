<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue';
const props = defineProps<{ label: string }>();
const key = 'weknora:knowledge-folder-width';
const pane = ref<HTMLElement>();
const requestedWidth = ref(220);
const available = ref(1000);
const maximum = computed(() => Math.max(180, Math.min(720, available.value - 320)));
const width = computed(() => Math.min(maximum.value, Math.max(180, requestedWidth.value)));
const dragging = ref(false);
let pointer: number | null = null;
let startX = 0;
let startWidth = 0;
let observer: ResizeObserver | undefined;
function save() { try { localStorage.setItem(key, String(requestedWidth.value)); } catch {} }
function start(event: PointerEvent) {
  if (!event.isPrimary || event.button !== 0) return;
  const target = event.currentTarget as HTMLElement;
  target.setPointerCapture(event.pointerId);
  pointer = event.pointerId;
  startX = event.clientX;
  startWidth = width.value;
  dragging.value = true;
  event.preventDefault();
}
function move(event: PointerEvent) {
  if (pointer !== event.pointerId) return;
  requestedWidth.value = Math.min(maximum.value, Math.max(180, startWidth + event.clientX - startX));
}
function finish() { if (pointer !== null) save(); pointer = null; dragging.value = false; }
function keyboard(event: KeyboardEvent) {
  const next = event.key === 'ArrowLeft' ? width.value - 20 : event.key === 'ArrowRight' ? width.value + 20 : event.key === 'Home' ? 180 : event.key === 'End' ? maximum.value : null;
  if (next === null) return;
  event.preventDefault();
  requestedWidth.value = Math.min(maximum.value, Math.max(180, next));
  save();
}
onMounted(() => {
  try { const saved = Number(localStorage.getItem(key)); if (Number.isFinite(saved) && saved >= 180) requestedWidth.value = Math.min(720, saved); } catch {}
  const parent = pane.value?.parentElement;
  if (parent) {
    available.value = parent.clientWidth - 16;
    observer = new ResizeObserver(() => { available.value = parent.clientWidth - 16; });
    observer.observe(parent);
  }
});
onUnmounted(() => { observer?.disconnect(); finish(); });
</script>
<template>
  <div ref="pane" class="resizable-folder-pane" :class="{ 'is-dragging': dragging }" :style="{ width: `${width}px` }">
    <slot />
    <div class="folder-resize-handle" role="separator" tabindex="0" aria-orientation="vertical"
      :aria-label="props.label" :aria-valuemin="180" :aria-valuemax="maximum" :aria-valuenow="Math.round(width)"
      @pointerdown="start" @pointermove="move" @pointerup="finish" @pointercancel="finish" @lostpointercapture="finish" @keydown="keyboard" />
  </div>
</template>
<style scoped lang="less">
.resizable-folder-pane { position: relative; display: flex; flex-shrink: 0; min-height: 0; margin-right: 16px; border-right: 1px solid var(--td-component-stroke); box-sizing: border-box; }
.resizable-folder-pane :deep(.source-folder-tree), .resizable-folder-pane > :deep(.kb-folder-tree) { width: 100%; margin-right: 0; border-right: 0; }
.folder-resize-handle { position: absolute; z-index: 2; top: 0; bottom: 0; right: -5px; width: 10px; cursor: col-resize; touch-action: none; }
.folder-resize-handle::after { content: ''; position: absolute; top: 0; bottom: 0; left: 4px; width: 2px; background: transparent; }
.folder-resize-handle:hover::after, .folder-resize-handle:focus-visible::after, .is-dragging .folder-resize-handle::after { background: var(--td-brand-color); }
.is-dragging { user-select: none; }
@media (max-width: 1000px) {
  .resizable-folder-pane { width: 100% !important; max-height: 200px; margin: 0 0 16px; border-right: 0; border-bottom: 1px solid var(--td-component-stroke); }
  .resizable-folder-pane :deep(.source-folder-tree), .resizable-folder-pane > :deep(.kb-folder-tree) { padding-bottom: 12px; border-bottom: 0; margin-bottom: 0; }
  .folder-resize-handle { display: none; }
}
</style>
