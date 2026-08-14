---
title: Benchmark 基准图表
description: CCX 汇总多来源 benchmark 数据生成的模型能力/成本对比页
---

# Benchmark 基准图表

CCX 会从多个公开 benchmark 来源汇总模型能力、成本与多来源比较数据，并生成这张交互式图表页面。

- 上半部分展示模型能力-成本边界，可切换平均成本 / 中位成本与来源范围。
- 下半部分展示同一模型在多来源 benchmark 中的原始分数，便于观察来源内相对位置。
- 不同 benchmark 的任务集、评分方法与量纲并不完全一致，因此**跨来源分数不应被直接视为严格同标尺排序**。

<BenchmarkUpdatedAt src="/ccx/benchmark/benchmark-viz-data.json" locale="zh" />

<iframe
  src="/ccx/benchmark/benchmark-chart.html"
  title="CCX Benchmark Chart"
  style="width: 100%; min-height: 1800px; border: 1px solid var(--vp-c-divider); border-radius: 12px; background: var(--vp-c-bg-alt);"
/>

## 方法说明

- `pass@1` / 原始 score 均来自对应上游 benchmark 的公开结果或公开 API。
- 成本图当前主要使用 DeepSWE 与 CodexRadar 可映射出的单任务成本数据。
- 多来源比较图会并列展示 DeepSWE、BenchLM.ai、CodexRadar、Artificial Analysis 等来源的原始分数。
- 数据生成时间可查看 `benchmark-viz-data.json` 中的 `generatedAt` 字段；该文件与图表 HTML 一同生成到文档站静态资源目录。

## 数据来源与致谢

<table>
  <tr>
    <td align="center" width="60">
      <a target="_blank" rel="noopener noreferrer nofollow" href="https://artificialanalysis.ai/"><img src="/sponsors/artificial-analysis.png" alt="Artificial Analysis Logo" style="max-width: 100%;padding-top: 6px;" width="50"></a>
    </td>
    <td>
      本页面中的部分 benchmark 数据使用了 Artificial Analysis 免费 API。凡使用这些数据，需按要求归因到 <a href="https://artificialanalysis.ai/">artificialanalysis.ai</a>。其中 Intelligence Index 当前应按 <strong>v4.1.1</strong> 解读；Coding Index 与 Agentic Index 是同一组评测子集的派生指标，不单独做版本管理。
    </td>
  </tr>
  <tr>
    <td align="center" width="60">
      <strong>数据源</strong>
    </td>
    <td>
      CCX 的 benchmark 可视化与注册表更新还使用了 <a href="https://benchlm.ai/">BenchLM.ai</a>、<a href="https://deepswe.datacurve.ai/">DeepSWE</a>、<a href="https://deng.codexradar.com/">CodexRadar</a> 的公开数据；价格与上下文窗口元数据另外同步自 <a href="https://github.com/BerriAI/litellm/blob/main/model_prices_and_context_window.json">LiteLLM model_prices_and_context_window.json</a>。
    </td>
  </tr>
</table>
