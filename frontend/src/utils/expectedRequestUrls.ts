import { isValidUrl } from './quickInputParser'
import { buildExpectedRequestUrl } from './baseUrlSemantics'

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
  if (channelType === 'images') {
    endpoint = '/images/generations'
  } else if (channelType === 'responses') {
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
      const expectedUrl = buildExpectedRequestUrl(serviceType, endpoint, rawUrl)

      return { baseUrl: rawUrl, expectedUrl }
    })
}
