<script setup lang="ts">
import { computed } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { retryFailedSync } from '@/api/datasource'
import { submittingRetries } from './failedSyncRetry'

const props = defineProps<{ dataSourceId: string; logId: string; busy: boolean }>()
const emit = defineEmits<{ (e: 'retried', logId: string): void }>()
const { t } = useI18n()
const submitting = computed(() => submittingRetries.has(props.dataSourceId))
async function retry() {
  if (props.busy || submitting.value) return
  submittingRetries.add(props.dataSourceId)
  try {
    const response = await retryFailedSync(props.dataSourceId, props.logId)
    const log = response?.data || response
    emit('retried', log.id)
    MessagePlugin.success(t('datasource.retrySubmitted'))
  } catch (error: any) {
    MessagePlugin.error(error?.message || error?.error || t('datasource.retryFailed'))
  } finally {
    submittingRetries.delete(props.dataSourceId)
  }
}
</script>

<template>
  <t-tooltip :content="t('datasource.retryAfterSync')" :disabled="!busy">
    <span class="ds-retry-button" @click.stop @keydown.stop>
      <t-button size="small" variant="text" theme="primary" :disabled="busy" :loading="submitting" @click.stop="retry">
        {{ t('datasource.retryFailedItems') }}
      </t-button>
    </span>
  </t-tooltip>
</template>

<style scoped>
.ds-retry-button { display: inline-flex; flex-shrink: 0; margin-left: auto; }
</style>
