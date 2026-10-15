<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import KbFolderTree from './KbFolderTree.vue'
import type { KnowledgeFolderTree } from '@/api/knowledge-base/index'
const props=defineProps<{tree:KnowledgeFolderTree;selectedGroup:string|null;selectedPath:string;canEdit:boolean}>()
const emit=defineEmits<{
 select:[selection:{group:string|null;path:string}]
 rename:[payload:{from:string;to:string;datasource_id?:string;unassigned?:boolean}]
 collapse:[]
}>()
const {t}=useI18n()
const allTree=computed(()=>({folders:[],root_document_count:props.tree.root_document_count,total_document_count:props.tree.total_document_count}))
const groupKey=(group:any)=>group.unassigned?'__unassigned__':group.id
const groupName=(group:any)=>group.unassigned?t('knowledgeBase.sourceGroups.other'):group.name||(group.removed?t('knowledgeBase.sourceGroups.removed'):t(`datasource.connector.${group.type}`))
</script>
<template>
 <aside class="source-folder-tree">
  <div class="source-folder-header"><span>{{ t('knowledgeBase.folderTree.title') }}</span><button type="button" :aria-label="t('knowledgeBase.folderTree.collapse')" @click="emit('collapse')"><t-icon name="chevron-left-double" size="15px" /></button></div>
  <div class="source-folder-body">
   <KbFolderTree embedded :tree="allTree" :root-label="t('knowledgeBase.sourceGroups.all')" :selected-path="selectedGroup===null?'':null" @select="emit('select',{group:null,path:''})" />
   <KbFolderTree v-for="group in tree.source_groups" :key="groupKey(group)" embedded :tree="group.tree" :root-label="groupName(group)" :root-source-type="group.type" :selected-path="selectedGroup===groupKey(group)?selectedPath:null" :can-edit="canEdit" @select="path=>emit('select',{group:groupKey(group),path})" @rename="payload=>emit('rename',{...payload,datasource_id:group.unassigned?undefined:group.id,unassigned:group.unassigned})" />
  </div>
 </aside>
</template>
<style scoped lang="less">
.source-folder-tree {display:flex;flex-direction:column;flex-shrink:0;width:220px;min-height:0;padding-right:12px;margin-right:16px;border-right:1px solid var(--td-component-stroke);box-sizing:border-box;}
.source-folder-header {display:flex;justify-content:space-between;align-items:center;height:32px;padding-bottom:16px;flex-shrink:0;font-size:var(--app-text-md);font-weight:500;color:var(--td-text-color-primary);}
.source-folder-header button {display:flex;align-items:center;justify-content:center;width:24px;height:24px;border:0;border-radius:var(--app-radius-sm);background:transparent;color:var(--td-text-color-secondary);cursor:pointer;}
.source-folder-body {min-height:0;overflow:auto;scrollbar-width:thin;}
.source-folder-body :deep(.kb-folder-tree__body){overflow:visible;flex:initial;padding:0;}
</style>
