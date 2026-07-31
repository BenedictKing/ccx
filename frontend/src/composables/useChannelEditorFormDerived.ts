import { computed } from 'vue'
import { buildDiscoveryExpectedRequestUrls, buildExpectedRequestUrls } from '../utils/expectedRequestUrls'

type ChannelType = 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'
type ServiceType = 'openai' | 'gemini' | 'claude' | 'responses' | 'copilot' | ''
type RefLike<T> = { value: T }
type FormLike = {
  baseUrl: string
  customHeaders: Record<string, string>
  serviceType: ServiceType
}

export function useChannelEditorFormDerived(
  channelType: RefLike<ChannelType>,
  form: FormLike,
  baseUrlsText: RefLike<string>,
) {
  const baseUrlHasError = computed(() => {
    const value = form.baseUrl
    if (!value) return true
    try {
      new URL(value)
      return false
    } catch {
      return true
    }
  })

  const expectedRequestUrls = computed(() => {
    if (!baseUrlsText.value || !form.serviceType) return []
    const baseUrls = baseUrlsText.value.split('\n').map(url => url.trim()).filter(Boolean)
    if (
      channelType.value !== 'images' &&
      channelType.value !== 'vectors' &&
      form.serviceType !== 'copilot'
    ) {
      return baseUrls.flatMap(buildDiscoveryExpectedRequestUrls)
    }
    return buildExpectedRequestUrls(
      channelType.value,
      form.serviceType,
      undefined,
      baseUrls,
    ).map(item => ({ ...item, protocol: channelType.value }))
  })

  const customHeadersArray = computed(() => {
    return Object.entries(form.customHeaders).map(([key, value]) => ({ key, value }))
  })

  const updateCustomHeaders = (headers: Array<{ key: string; value: string }>) => {
    const newHeaders: Record<string, string> = {}
    headers.forEach(h => {
      if (h.key && h.value) {
        newHeaders[h.key] = h.value
      }
    })
    form.customHeaders = newHeaders
  }

  return {
    baseUrlHasError,
    expectedRequestUrls,
    customHeadersArray,
    updateCustomHeaders,
  }
}
