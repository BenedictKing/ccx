// @vitest-environment jsdom
/* eslint-disable vue/one-component-per-file, vue/require-prop-types */
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h, nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import SiteOnboardingWizard from './SiteOnboardingWizard.vue'
import api from '../services/api'

vi.mock('../i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('../services/api', () => ({ default: {
  listSiteAccounts: vi.fn(), upsertSiteAccount: vi.fn(), verifySiteAccount: vi.fn(),
  getSiteAccountGroups: vi.fn(), getSiteAccountKeys: vi.fn(), provisionSiteAccount: vi.fn(),
} }))

const box = defineComponent({ setup(_, { slots }) { return () => h('div', slots.default?.()) } })
const field = defineComponent({ name: 'TestField', props: ['label', 'modelValue', 'items'], emits: ['update:modelValue'], setup: () => () => h('div') })
const button = defineComponent({ props: ['disabled'], emits: ['click'], setup(props, { slots, emit }) { return () => h('button', { disabled: props.disabled, onClick: () => emit('click') }, slots.default?.()) } })
const stepper = defineComponent({ props: ['modelValue'], setup(props, { slots }) { return () => h('div', slots[`item.${props.modelValue}`]?.()) } })

function renderWizard() {
  return mount(SiteOnboardingWizard, { global: { stubs: {
    VStepper: stepper, VCard: box, VCardText: box, VRow: box, VCol: box,
    VAlert: box, VTable: box, VIcon: box, VChip: box, VProgressLinear: box,
    VSelect: field, VCombobox: field, VTextField: field, VCheckboxBtn: field, VSwitch: field, VBtn: button,
  } } })
}
type Wizard = ReturnType<typeof renderWizard>
async function setField(wrapper: Wizard, label: string, value: unknown) {
  const input = wrapper.findAllComponents(field).find(item => item.props('label') === label)
  expect(input, label).toBeDefined()
  input!.vm.$emit('update:modelValue', value)
  await nextTick()
}
async function click(wrapper: Wizard, label: string) {
  const target = wrapper.findAll('button').find(item => item.text() === label)
  expect(target, label).toBeDefined()
  await target!.trigger('click')
  await flushPromises()
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.listSiteAccounts).mockResolvedValue({ accounts: [] })
  vi.mocked(api.upsertSiteAccount).mockResolvedValue({ account: { uid: 'account-1', siteType: 'newapi', baseUrl: 'https://relay.example', createdAt: '', updatedAt: '' } })
  vi.mocked(api.verifySiteAccount).mockResolvedValue({ verified: true })
  vi.mocked(api.getSiteAccountKeys).mockResolvedValue({ keys: null as never })
  vi.mocked(api.getSiteAccountGroups).mockResolvedValue({ identified: true, groups: [
    { name: 'cheap', ratio: 0.5, models: ['model-a', 'model-b'] },
    { name: 'expensive', ratio: 2, models: ['model-a'] },
  ] })
  vi.mocked(api.provisionSiteAccount).mockResolvedValue({ accountUID: 'account-1', protocol: 'responses', groups: [] })
})

describe('站点接入向导', () => {
  it('新账号无 Key 时仍可选模型，按倍率筛组并显式提交备用顺序', async () => {
    const wrapper = renderWizard()
    await flushPromises()
    await setField(wrapper, 'siteOnboarding.baseUrl', 'https://relay.example')
    await setField(wrapper, 'siteOnboarding.accessToken', 'management-token')
    await click(wrapper, 'siteOnboarding.verifyAndContinue')
    await setField(wrapper, 'siteOnboarding.targetModels', ['model-a', 'model-b'])
    await click(wrapper, 'siteOnboarding.next')
    await setField(wrapper, 'siteOnboarding.allowFallback', true)
    await click(wrapper, 'siteOnboarding.startProvision')
    expect(api.provisionSiteAccount).toHaveBeenCalledWith('account-1', expect.objectContaining({
      agent: 'codex', protocol: 'responses', models: ['model-a', 'model-b'], groups: ['cheap'],
      allowModelFallback: true, maxAttempts: 6, maxWaitSeconds: 90, autoSyncGroups: false,
    }))
    wrapper.unmount()
  })

  it('只有推理 Key 且模型列表失败时仍可手动输入模型接入', async () => {
    vi.mocked(api.verifySiteAccount).mockResolvedValue({ verified: false, error: 'models unavailable' })
    vi.mocked(api.getSiteAccountGroups).mockResolvedValue({ identified: false, groups: [{ name: 'imported', ratio: 0, models: [] }], error: 'manual model required' })
    const wrapper = renderWizard()
    await flushPromises()
    await setField(wrapper, 'siteOnboarding.credentialMode', 'key')
    await setField(wrapper, 'siteOnboarding.baseUrl', 'https://relay.example')
    await setField(wrapper, 'siteOnboarding.inferenceKey', 'sk-inference')
    await click(wrapper, 'siteOnboarding.verifyAndContinue')
    await setField(wrapper, 'siteOnboarding.targetModels', ['chosen-model'])
    await click(wrapper, 'siteOnboarding.next')
    await click(wrapper, 'siteOnboarding.startProvision')
    expect(api.getSiteAccountKeys).not.toHaveBeenCalled()
    expect(api.provisionSiteAccount).toHaveBeenCalledWith('account-1', expect.objectContaining({ groups: ['imported'], models: ['chosen-model'], allowModelFallback: false }))
    wrapper.unmount()
  })
})
