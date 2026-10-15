<script setup lang="ts">
import { useI18n } from 'vue-i18n';
import type { FolderNavigationItem } from '../folderContents';
import { getDatasourceIconUrl as datasourceIcon } from '../settings/datasourceIcons';
defineProps<{ items: FolderNavigationItem[]; mode: 'grid' | 'list' }>();
const emit = defineEmits<{ (e: 'navigate', item: FolderNavigationItem): void }>();
const { t } = useI18n();
</script>
<template>
  <template v-for="item in items" :key="item.key">
    <button v-if="mode === 'grid'" type="button" class="folder-card" :title="item.name" @mousedown.stop @click="emit('navigate', item)">
      <span class="folder-heading">
        <img v-if="item.sourceType && datasourceIcon(item.sourceType)" :src="datasourceIcon(item.sourceType)" class="folder-source-icon" alt="" />
        <t-icon v-else name="folder" class="folder-icon" />
        <span class="folder-name">{{ item.name }}</span>
      </span>
      <span class="folder-count">{{ t('knowledgeBase.folderTree.folderCardCount', { count: item.totalCount }) }}</span>
    </button>
    <div v-else class="folder-list-row" role="row">
      <div role="cell" class="folder-list-cell">
        <button type="button" class="folder-list-button" :title="item.name" @mousedown.stop @click="emit('navigate', item)">
          <img v-if="item.sourceType && datasourceIcon(item.sourceType)" :src="datasourceIcon(item.sourceType)" class="folder-source-icon" alt="" />
          <t-icon v-else name="folder" class="folder-icon" />
          <span class="folder-list-text"><span class="folder-name">{{ item.name }}</span><span class="folder-count">{{ t('knowledgeBase.folderTree.folderCardCount', { count: item.totalCount }) }}</span></span>
        </button>
      </div>
    </div>
  </template>
</template>
<style scoped lang="less">
.folder-card { display: flex; flex-direction: column; justify-content: space-between; gap: 16px; min-width: 0; min-height: 120px; padding: 10px; border: 1px solid var(--td-component-stroke); border-radius: var(--app-radius-md); background: var(--td-bg-color-container); text-align: left; color: var(--td-text-color-primary); font: inherit; cursor: pointer; }
.folder-card:hover { border-color: var(--app-selection-border); }
.folder-heading { display: flex; align-items: flex-start; gap: 7px; min-width: 0; }
.folder-name { min-width: 0; overflow-wrap: anywhere; font-size: var(--app-text-md); font-weight: 500; }
.folder-count { color: var(--td-text-color-secondary); font-size: var(--app-text-xs); }
.folder-icon { flex-shrink: 0; font-size: 24px; color: var(--td-text-color-secondary); }
.folder-source-icon { flex-shrink: 0; width: 24px; height: 24px; object-fit: contain; }
.folder-list-row { border-radius: var(--app-radius-md); }
.folder-list-button { display: flex; align-items: center; gap: 12px; width: 100%; min-height: 68px; padding: 10px 8px 10px 44px; box-sizing: border-box; border: 0; border-radius: var(--app-radius-md); background: transparent; text-align: left; font: inherit; color: var(--td-text-color-primary); cursor: pointer; }
.folder-list-button:hover { background: var(--td-bg-color-secondarycontainer); }
.folder-list-text { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
.folder-card:focus-visible, .folder-list-button:focus-visible { outline: 2px solid var(--app-focus-border); outline-offset: 2px; }
@container doc-card-area (max-width: 720px) { .folder-list-button { padding-left: 40px; } }
</style>
