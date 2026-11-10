import { reactive } from 'vue'

// Shared by the card and history drawer during the same request.
export const submittingRetries = reactive(new Set<string>())
