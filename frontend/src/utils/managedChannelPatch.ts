import type { Channel } from '../services/api'
import { normalizeMaxGroupMultiplier } from './channelPayload'
import { EDIT_CHANNEL_PAYLOAD_KEYS } from './editChannelPayload'
import { isStructurallyEqual } from './structuralSharing'

function normalizePositiveOrZero(value: unknown): number {
  const parsed = Number(value)
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0
}

function normalizeTrimmed(value: unknown): string {
  return (value ?? '').toString().trim()
}

function normalizeHeaders(value: unknown): Record<string, string> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
  return value as Record<string, string>
}

function normalizeModelMapping(value: unknown): Record<string, string> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
  return value as Record<string, string>
}

/**
 * 托管账号渠道编辑保存时，账号接口（PUT /accounts）不承载、需经单卡更新下发的渠道级字段，
 * 及其与 buildChannelPayload 下发口径一致的归一化器（0/空=清除）。
 */
const CHANNEL_PATCH_NORMALIZERS: Readonly<Record<string, (value: unknown) => unknown>> = {
  costMultiplier: normalizePositiveOrZero,
  maxGroupMultiplier: (value) => normalizeMaxGroupMultiplier(value) ?? 0,
  channelPaymentCurrency: normalizeTrimmed,
  channelPaymentAmount: normalizePositiveOrZero,
  channelCreditCurrency: normalizeTrimmed,
  channelCreditAmount: normalizePositiveOrZero,
  proxyUrl: normalizeTrimmed,
  customHeaders: normalizeHeaders,
  modelMapping: normalizeModelMapping,
}

/** 账号接口（凭证池/名称/地址）或独立特例（website/remark）已承载的字段，不参与丢弃检测。 */
const ACCOUNT_BORNE_FIELDS = new Set(['name', 'apiKeys', 'apiKeyConfigs', 'baseUrl', 'baseUrls', 'website', 'remark'])

/**
 * 比较托管账号渠道编辑前后的渠道级字段，返回需要经单卡更新下发的 patch（空对象=无变化）。
 * 表单原始值（字符串/数字/null）与渠道视图值统一归一化后比较，避免形态差异误判变化。
 */
export function buildManagedChannelPatch(
  original: Channel | null | undefined,
  next: Record<string, unknown>,
): Record<string, unknown> {
  if (!original) return {}
  const originalRecord = original as unknown as Record<string, unknown>
  const patch: Record<string, unknown> = {}
  for (const [field, normalize] of Object.entries(CHANNEL_PATCH_NORMALIZERS)) {
    const before = normalize(originalRecord[field])
    const after = normalize(next[field])
    if (!isStructurallyEqual(before, after)) {
      patch[field] = after
    }
  }
  return patch
}

/**
 * 托管账号保存链路不会持久化、但编辑前后发生了变化的字段名清单（debug 日志兜底用）。
 * 覆盖账号接口与 website/remark 特例承载的字段及 CHANNEL_PATCH_NORMALIZERS 已下发字段；
 * 数字与数字字符串按数值宽松相等，避免表单原始值形态差异误报。
 */
export function listDroppedManagedChannelFields(
  original: Channel | null | undefined,
  next: Record<string, unknown>,
): string[] {
  if (!original) return []
  const originalRecord = original as unknown as Record<string, unknown>
  const dropped: string[] = []
  for (const key of EDIT_CHANNEL_PAYLOAD_KEYS) {
    if (ACCOUNT_BORNE_FIELDS.has(key) || key in CHANNEL_PATCH_NORMALIZERS) continue
    if (!(key in next)) continue
    if (!looselyEqual(originalRecord[key], next[key])) {
      dropped.push(key)
    }
  }
  return dropped
}

function looselyEqual(a: unknown, b: unknown): boolean {
  if (isStructurallyEqual(a, b)) return true
  if (a === null || a === undefined || a === '' || b === null || b === undefined || b === '') return false
  const na = Number(a)
  const nb = Number(b)
  return Number.isFinite(na) && Number.isFinite(nb) && na === nb
}

type KeyMultiplierConfigLike = {
  key?: string
  keyUid?: string
  credentialUid?: string
  groupMultiplier?: number | null
  consumptionPolicy?: 'normal' | 'opportunistic' | null
}

/**
 * 比较 Key 级倍率（groupMultiplier/consumptionPolicy）在编辑前后的差异，
 * 返回需要随单卡更新补发的 trimmed apiKeyConfigs（仅定位字段+倍率字段，
 * 避免覆盖托管凭证元数据）；无差异返回 null。托管账号保存链路专用。
 */
export function buildStagedKeyMultiplierConfigs(
  original: Channel | null | undefined,
  next: Record<string, unknown>,
): Array<KeyMultiplierConfigLike> | null {
  if (!original) return null
  const originalConfigs = (original as unknown as Record<string, unknown>).apiKeyConfigs as KeyMultiplierConfigLike[] | undefined
  const nextConfigs = next.apiKeyConfigs as KeyMultiplierConfigLike[] | undefined
  if (!nextConfigs?.length) return null

  const staged: Array<KeyMultiplierConfigLike> = []
  for (const nextCfg of nextConfigs) {
    const before = originalConfigs?.find(cfg =>
      (nextCfg.keyUid && cfg.keyUid === nextCfg.keyUid)
      || (nextCfg.credentialUid && cfg.credentialUid === nextCfg.credentialUid)
      || (nextCfg.key && cfg.key === nextCfg.key),
    )
    const multiplierChanged = !looselyEqual(before?.groupMultiplier ?? null, nextCfg.groupMultiplier ?? null)
    const policyChanged = (before?.consumptionPolicy ?? null) !== (nextCfg.consumptionPolicy ?? null)
    if (!multiplierChanged && !policyChanged) continue
    staged.push({
      key: nextCfg.key,
      keyUid: nextCfg.keyUid,
      credentialUid: nextCfg.credentialUid,
      groupMultiplier: nextCfg.groupMultiplier ?? null,
      consumptionPolicy: nextCfg.consumptionPolicy ?? null,
    })
  }
  return staged.length > 0 ? staged : null
}
