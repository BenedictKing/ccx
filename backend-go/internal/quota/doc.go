// Package quota 实现配额真相分级与懒重置饱和桶。
//
// 设计来源：docs/specs/omniroute-benchmark-upgrades.md §2
// 蓝本参考：OmniRoute src/lib/quota/providerQuotaTelemetry.ts + accountBuckets.ts
//
// 核心概念：
//   - TruthLevel: 五级配额真相（healthy / approaching_limit / exhausted / unavailable / unknown）
//   - SourcePriority: 来源优先级（provider_api > response_headers > configured > estimated > unknown）
//   - Buckets: 懒重置饱和桶，读取时 now>=resetsAtMs 清零，无后台 cron
//   - HeaderParser: per-provider 响应头映射解析
package quota
