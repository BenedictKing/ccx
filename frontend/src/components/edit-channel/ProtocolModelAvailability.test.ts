// @vitest-environment jsdom
import { mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import type { ChannelProtocolRoute } from '../../services/api'
import ProtocolModelAvailability from './ProtocolModelAvailability.vue'

const autopilotMocks = vi.hoisted(() => ({
  autoDiscoverChannel: vi.fn(),
  getChannelAutoStatus: vi.fn(),
}))

vi.mock('../../services/autopilot-api', () => autopilotMocks)

vi.mock('../../i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, number>) => {
      if (params?.count !== undefined) return `${key}:${params.count}`
      if (params?.available !== undefined) return `${key}:${params.available}/${params.total}`
      return key
    },
  }),
}))

const passthroughStub = defineComponent({
  template: '<span><slot /></span>',
})

const buttonStub = defineComponent({
  emits: ['click'],
  template: '<button @click="$emit(\'click\')"><slot /></button>',
})

describe('ProtocolModelAvailability', () => {
  it('按协议分组展示各自的可用模型', () => {
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [
          {
            kind: 'messages', index: 0, name: 'fastaitoken-claude', serviceType: 'claude',
            supportedModels: ['gpt-5.6-terra', 'gpt-5.6-sol', 'gpt-5.6-sol'],
          },
          {
            kind: 'chat', index: 0, name: 'fastaitoken-chat', serviceType: 'openai',
            supportedModels: ['gpt-5.6-sol'],
          },
          {
            kind: 'responses', index: 0, name: 'fastaitoken-codex', serviceType: 'responses',
            supportedModels: ['codex-auto-review'],
          },
        ],
      },
      global: {
        stubs: {
          VChip: passthroughStub,
          VIcon: passthroughStub,
        },
      },
    })

    const messages = wrapper.get('[data-kind="messages"]')
    const chat = wrapper.get('[data-kind="chat"]')
    const responses = wrapper.get('[data-kind="responses"]')

    expect(messages.text()).toContain('/v1/messages')
    expect(messages.text()).toContain('gpt-5.6-sol')
    expect(messages.text()).toContain('gpt-5.6-terra')
    expect(messages.text().match(/gpt-5\.6-sol/g)).toHaveLength(1)
    expect(chat.text()).toContain('/v1/chat/completions')
    expect(chat.text()).not.toContain('gpt-5.6-terra')
    expect(responses.text()).toContain('/v1/responses')
    expect(responses.text()).toContain('codex-auto-review')
  })

  it('模型清单一致时只展示一次跨协议共有模型', () => {
    const routes: ChannelProtocolRoute[] = (['messages', 'chat', 'responses'] as const).map((kind, index) => ({
      kind,
      index,
      name: `shared-${kind}`,
      serviceType: kind === 'messages' ? 'claude' : kind === 'chat' ? 'openai' : 'responses',
      modelInventoryKnown: true,
      discoveredModels: ['model-a', 'model-b'],
    }))
    const wrapper = mount(ProtocolModelAvailability, {
      props: { routes },
      global: {
        stubs: {
          VChip: passthroughStub,
          VIcon: passthroughStub,
        },
      },
    })

    const shared = wrapper.get('.protocol-model-shared')
    expect(shared.text()).toContain('channelEditor.protocolModels.sharedTitle')
    expect(shared.text()).toContain('channelEditor.protocolModels.sharedHint:3')
    expect(shared.text()).toContain('model-a')
    expect(shared.text()).toContain('model-b')
    for (const kind of ['messages', 'chat', 'responses']) {
      const route = wrapper.get(`[data-kind="${kind}"]`)
      expect(route.text()).not.toContain('model-a')
      expect(route.text()).not.toContain('model-b')
      expect(route.text()).toContain('channelEditor.protocolModels.specificEmpty')
    }
  })

  it('先展示跨协议交集，再按协议展示独有模型', () => {
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [
          {
            kind: 'messages', index: 0, name: 'messages', serviceType: 'claude',
            discoveredModels: ['shared-model', 'claude-only'],
          },
          {
            kind: 'chat', index: 0, name: 'chat', serviceType: 'openai',
            discoveredModels: ['shared-model', 'chat-only'],
          },
          {
            kind: 'responses', index: 0, name: 'responses', serviceType: 'responses',
            discoveredModels: ['shared-model'],
          },
        ],
      },
      global: {
        stubs: {
          VChip: passthroughStub,
          VIcon: passthroughStub,
        },
      },
    })

    const shared = wrapper.get('.protocol-model-shared')
    expect(shared.text()).toContain('shared-model')
    expect(shared.text()).not.toContain('claude-only')
    expect(shared.text()).not.toContain('chat-only')
    expect(wrapper.get('[data-kind="messages"]').text()).toContain('claude-only')
    expect(wrapper.get('[data-kind="messages"]').text()).not.toContain('shared-model')
    expect(wrapper.get('[data-kind="chat"]').text()).toContain('chat-only')
    expect(wrapper.get('[data-kind="responses"]').text()).toContain('channelEditor.protocolModels.specificEmpty')
  })

  it('已发现的空模型协议会使跨协议交集为空', () => {
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [
          {
            kind: 'messages', index: 0, name: 'messages', serviceType: 'claude',
            modelInventoryKnown: true, discoveredModels: ['model-a'],
          },
          {
            kind: 'chat', index: 0, name: 'chat', serviceType: 'openai',
            modelInventoryKnown: true, discoveredModels: [],
          },
        ],
      },
      global: {
        stubs: {
          VChip: passthroughStub,
          VIcon: passthroughStub,
        },
      },
    })

    expect(wrapper.find('.protocol-model-shared').exists()).toBe(false)
    expect(wrapper.get('[data-kind="messages"]').text()).toContain('model-a')
  })

  it('区分未记录模型范围与协议不可用', () => {
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [{ kind: 'gemini', index: 0, name: 'gemini', serviceType: 'gemini' }],
      },
      global: {
        stubs: {
          VChip: passthroughStub,
          VIcon: passthroughStub,
        },
      },
    })

    const gemini = wrapper.get('[data-kind="gemini"]')
    expect(gemini.text()).toContain('channelEditor.protocolModels.empty')
    expect(gemini.text()).not.toContain('channelEditor.protocolModels.count:0')
  })

  it('已发现到空模型清单时不回退配置白名单', () => {
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [{
          kind: 'responses', upstreamKind: 'chat', index: 0, name: 'chat-through-responses', serviceType: 'openai',
          supportedModels: ['configured-model'], modelInventoryKnown: true, discoveredModels: [],
          modelBindings: [{ credentialUid: 'cred-empty', keyMask: 'sk-e***001', models: [] }],
        }],
      },
      global: {
        stubs: {
          VChip: passthroughStub,
          VIcon: passthroughStub,
        },
      },
    })

    const chat = wrapper.get('[data-kind="chat"]')
    expect(chat.text()).toContain('/v1/chat/completions')
    expect(chat.text()).toContain('channelEditor.protocolModels.count:0')
    expect(chat.text()).not.toContain('configured-model')
  })

  it('优先展示 endpoint profile 模型并标记 Key 差异', () => {
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [{
          kind: 'messages', index: 0, name: 'volcengine-claude', serviceType: 'claude',
          supportedModels: ['configured-model'],
          discoveredModels: ['actual-model'],
          modelBindings: [
            { credentialUid: 'cred-a', keyMask: 'ark-a***001', models: ['actual-model'] },
            { credentialUid: 'cred-b', keyMask: 'ark-b***002', models: ['other-model'] },
          ],
        }],
      },
      global: {
        stubs: {
          VChip: passthroughStub,
          VIcon: passthroughStub,
        },
      },
    })

    const messages = wrapper.get('[data-kind="messages"]')
    expect(messages.text()).toContain('actual-model')
    expect(messages.text()).not.toContain('configured-model')
    expect(messages.text()).toContain('channelEditor.protocolModels.diffCount:2')
    expect(messages.text()).toContain('ark-a***001')
    expect(messages.text()).toContain('ark-b***002')
    expect(messages.text()).toContain('channelEditor.protocolModels.coverage:1/2')
    // 覆盖分组内模型数量少于 10 个时直接展开列出，不折叠。
    expect(messages.findAll('.protocol-model-route__all-summary')).toHaveLength(0)
    expect(messages.text()).toContain('actual-model')
    expect(messages.text()).toContain('other-model')
  })

  it('按相同可用 Key 集合归并共同与专有模型', () => {
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [{
          kind: 'messages', index: 0, name: 'volcengine-agent-plan', serviceType: 'claude',
          discoveredModels: ['shared-model', 'coding-exclusive', 'agent-exclusive'],
          modelBindings: [
            { credentialUid: 'cred-f5', keyMask: 'ark-f5***2fd', models: ['shared-model', 'coding-exclusive'] },
            { credentialUid: 'cred-de', keyMask: 'de5371***84e', models: ['shared-model', 'coding-exclusive'] },
            { credentialUid: 'cred-9b', keyMask: 'ark-9b***8db', models: ['shared-model', 'agent-exclusive'] },
            { credentialUid: 'cred-ec', keyMask: 'ark-ec***570', models: ['shared-model', 'agent-exclusive'] },
          ],
        }],
      },
      global: {
        stubs: {
          VChip: passthroughStub,
          VIcon: passthroughStub,
        },
      },
    })

    const messages = wrapper.get('[data-kind="messages"]')
    const groups = messages.findAll('.protocol-model-coverage-group')
    const shared = groups.find(group => group.text().includes('shared-model'))
    const codingOnly = groups.find(group => group.text().includes('coding-exclusive'))
    const agentOnly = groups.find(group => group.text().includes('agent-exclusive'))

    expect(messages.text()).toContain('channelEditor.protocolModels.diffCount:2')
    expect(groups).toHaveLength(3)
    expect(shared?.text()).toContain('channelEditor.protocolModels.coverageGroupShared:4')
    expect(shared?.text()).toContain('ark-f5***2fd')
    expect(shared?.text()).toContain('ark-ec***570')
    expect(codingOnly?.text()).toContain('channelEditor.protocolModels.coverageGroupExclusive:2')
    expect(codingOnly?.text()).toContain('ark-f5***2fd')
    expect(codingOnly?.text()).toContain('de5371***84e')
    expect(codingOnly?.text()).not.toContain('ark-9b***8db')
    expect(agentOnly?.text()).toContain('ark-9b***8db')
    expect(agentOnly?.text()).toContain('ark-ec***570')
    expect(agentOnly?.text()).not.toContain('ark-f5***2fd')
  })

  it('多 Key 模型一致时展示共同模型分组', () => {
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [{
          kind: 'messages', index: 0, name: 'multi-key', serviceType: 'claude',
          discoveredModels: ['model-a', 'model-b'],
          modelBindings: [
            { credentialUid: 'cred-a', keyMask: 'sk-a***001', models: ['model-a', 'model-b'] },
            { credentialUid: 'cred-b', keyMask: 'sk-b***002', models: ['model-a', 'model-b'] },
          ],
        }],
      },
      global: {
        stubs: {
          VChip: passthroughStub,
          VIcon: passthroughStub,
        },
      },
    })

    const messages = wrapper.get('[data-kind="messages"]')
    expect(messages.text()).toContain('channelEditor.protocolModels.consistent:2')
    expect(messages.text()).not.toContain('channelEditor.protocolModels.diffCount')
    expect(messages.find('.protocol-model-route__coverage-groups').exists()).toBe(true)
    expect(messages.text()).toContain('channelEditor.protocolModels.coverageGroupShared:2')
    expect(messages.text()).toContain('model-a')
    expect(messages.text()).toContain('model-b')
  })

  it('展示模型清单的发现时间、来源和说明', () => {
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [{
          kind: 'messages', index: 0, name: 'volcengine-claude', serviceType: 'claude',
          modelInventoryKnown: true,
          discoveredModels: ['glm-5.2'],
          modelsDiscoveredAt: '2026-07-22T00:42:12Z',
          modelDiscoverySource: 'control_plane',
          modelDiscoveryMessage: '火山管控面 Coding Plan 模型清单',
        }],
      },
      global: {
        stubs: {
          VChip: passthroughStub,
          VIcon: passthroughStub,
        },
      },
    })

    const messages = wrapper.get('[data-kind="messages"]')
    expect(messages.text()).toContain('channelEditor.protocolModels.lastDiscovered')
    expect(messages.text()).toContain('channelEditor.protocolModels.source.controlPlane')
    expect(messages.text()).toContain('火山管控面 Coding Plan 模型清单')
  })

  it('只在区块头部重新发现，并标记发现但未配置的协议', async () => {
    autopilotMocks.autoDiscoverChannel.mockResolvedValue({ discoveryStarted: true })
    autopilotMocks.getChannelAutoStatus.mockResolvedValue({
      autoManaged: true,
      discovery: { status: 'done' },
    })
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [
          {
            kind: 'responses', upstreamKind: 'responses', index: 2, channelUid: 'ch-responses',
            name: 'multi-protocol', serviceType: 'responses', configured: true,
            modelInventoryKnown: true, discoveredModels: ['gpt-5.4'],
          },
          {
            kind: 'messages', upstreamKind: 'messages', index: -1, channelUid: 'ch-responses',
            name: 'multi-protocol', serviceType: 'claude', configured: false,
            modelInventoryKnown: true, discoveredModels: ['claude-sonnet-4-6'],
          },
        ],
      },
      global: {
        stubs: {
          VAlert: passthroughStub,
          VBtn: buttonStub,
          VChip: passthroughStub,
          VIcon: passthroughStub,
          VProgressCircular: passthroughStub,
        },
      },
    })

    expect(wrapper.findAll('.protocol-model-availability__rediscover-all')).toHaveLength(1)
    expect(wrapper.get('[data-kind="messages"]').classes()).toContain('protocol-model-route--unconfigured')
    expect(wrapper.get('[data-kind="messages"]').text()).toContain('channelEditor.protocolModels.unconfiguredProtocol')

    await wrapper.get('.protocol-model-availability__rediscover-all').trigger('click')
    await vi.waitFor(() => {
      expect(autopilotMocks.autoDiscoverChannel).toHaveBeenCalledWith('responses', 'ch-responses')
    })
  })
})
