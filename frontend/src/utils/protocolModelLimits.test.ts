import { describe, expect, it } from 'vitest'
import { buildProtocolModelLimitUpdates } from './protocolModelLimits'
import type { ChannelProtocolRoute } from '../services/api'

const routes: ChannelProtocolRoute[] = [
  { kind: 'responses', index: 1, channelUid: 'luna', name: 'luna', serviceType: 'responses', supportedModels: ['*luna'] },
  { kind: 'responses', index: 3, channelUid: 'code', name: 'code', serviceType: 'responses', supportedModels: ['*sol'] },
]
describe('buildProtocolModelLimitUpdates', () => {
  it('按 UID 匹配同协议多路由，忽略下标重排', () => {
    expect(buildProtocolModelLimitUpdates(routes, [
      { ...routes[1]!, index: 0, supportedModels: ['*astra'] }, routes[0]!,
    ])).toEqual([{ channelUid: 'code', kind: 'responses', supportedModels: ['*astra'] }])
  })
  it('显式提交空数组清除限制，不提交未修改路由', () => {
    expect(buildProtocolModelLimitUpdates(routes, [{ ...routes[0]!, supportedModels: [] }, routes[1]!]))
      .toEqual([{ channelUid: 'luna', kind: 'responses', supportedModels: [] }])
    expect(buildProtocolModelLimitUpdates(routes, routes)).toEqual([])
  })
  it('转换协议使用实际执行协议，只保存已有路由', () => {
    expect(buildProtocolModelLimitUpdates(routes, [
      { ...routes[0]!, upstreamKind: 'chat', supportedModels: ['gpt*', '!*luna'] },
      { ...routes[1]!, channelUid: 'unconfigured', supportedModels: ['*'] },
    ])).toEqual([{ channelUid: 'luna', kind: 'chat', supportedModels: ['gpt*', '!*luna'] }])
  })
})
