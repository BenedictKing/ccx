import { isValidUrl } from './quickInputParser'

export type ChannelType = 'messages' | 'chat' | 'responses' | 'gemini' | 'images'
export type ServiceType = 'openai' | 'claude' | 'gemini' | 'responses' | ''

export interface ExpectedRequestUrlItem {
  baseUrl: string
  expectedUrl: string
}

export function buildExpectedRequestUrls(
  channelType: ChannelType,
  serviceType: ServiceType,
  baseUrl?: string,
  baseUrls?: string[]
): ExpectedRequestUrlItem[] {
  if (!serviceType) return []

  const urls: string[] = []
  if (baseUrls && baseUrls.length > 0) {
    urls.push(...baseUrls)
  } else if (baseUrl) {
    urls.push(baseUrl)
  }

  if (urls.length === 0) return []

  let endpoint = ''
  if (channelType === 'responses') {
    if (serviceType === 'responses') {
      endpoint = '/responses'
    } else if (serviceType === 'claude') {
      endpoint = '/messages'
    } else if (serviceType === 'gemini') {
      endpoint = '/models/{model}:generateContent'
    } else {
      endpoint = '/chat/completions'
    }
  } else {
    if (serviceType === 'claude') {
      endpoint = '/messages'
    } else if (serviceType === 'gemini') {
      endpoint = '/models/{model}:generateContent'
    } else if (serviceType === 'responses') {
      endpoint = '/responses'
    } else {
      endpoint = '/chat/completions'
    }
  }

  return urls
    .filter(url => url && isValidUrl(url.replace(/#$/, '')))
    .map(rawUrl => {
      let normalizedBaseUrl = rawUrl.trim()
      const skipVersion = normalizedBaseUrl.endsWith('#')
      if (skipVersion) {
        normalizedBaseUrl = normalizedBaseUrl.slice(0, -1)
      }
      normalizedBaseUrl = normalizedBaseUrl.replace(/\/$/, '')

      const hasVersion = /\/v\d+[a-z]*$/.test(normalizedBaseUrl)
      const versionPrefix = serviceType === 'gemini' ? '/v1beta' : '/v1'
      const expectedUrl = hasVersion || skipVersion ? normalizedBaseUrl + endpoint : normalizedBaseUrl + versionPrefix + endpoint

      return { baseUrl: rawUrl, expectedUrl }
    })
}
