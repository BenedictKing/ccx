---
title: Benchmark Dashboard
description: Interactive model capability and cost comparison built from CCX benchmark data
---

# Benchmark Dashboard

CCX aggregates model capability, cost, and multi-source comparison data from several public benchmark sources and publishes this interactive chart.

- The upper section shows the capability-cost frontier, with switches for mean / median cost and source scope.
- The lower section compares raw scores for the same model across multiple benchmark sources.
- Because benchmark task sets, scoring rules, and scales differ, **cross-source scores should not be treated as a strictly normalized ranking**.

<BenchmarkUpdatedAt src="/ccx/benchmark/benchmark-viz-data.json" locale="en" />

<iframe
  src="/ccx/benchmark/benchmark-chart.html"
  title="CCX Benchmark Chart"
  style="width: 100%; min-height: 1800px; border: 1px solid var(--vp-c-divider); border-radius: 12px; background: var(--vp-c-bg-alt);"
/>

## Methodology notes

- `pass@1` and raw scores come from the corresponding upstream benchmark results or public APIs.
- The cost frontier currently uses the single-task cost data that can be mapped from DeepSWE and CodexRadar.
- The multi-source comparison chart juxtaposes raw scores from DeepSWE, BenchLM.ai, CodexRadar, Artificial Analysis, and related public sources.
- For the generation timestamp, see the `generatedAt` field in `benchmark-viz-data.json`; that file is generated alongside the chart HTML in the docs static asset directory.

## Data sources and acknowledgments

<table>
  <tr>
    <td align="center" width="60">
      <a target="_blank" rel="noopener noreferrer nofollow" href="https://artificialanalysis.ai/"><img src="/sponsors/artificial-analysis.png" alt="Artificial Analysis Logo" style="max-width: 100%;padding-top: 6px;" width="50"></a>
    </td>
    <td>
      Benchmark data on this page includes Artificial Analysis free API data. Attribution to <a href="https://artificialanalysis.ai/">artificialanalysis.ai</a> is required when using that data. Intelligence Index scores are currently interpreted against <strong>v4.1.1</strong>; Coding Index and Agentic Index are derived subsets of the same evaluation set and are not separately versioned.
    </td>
  </tr>
  <tr>
    <td align="center" width="60">
      <strong>Data</strong>
    </td>
    <td>
      CCX benchmark visualizations and registry updates also use publicly available data from <a href="https://benchlm.ai/">BenchLM.ai</a>, <a href="https://deepswe.datacurve.ai/">DeepSWE</a>, and <a href="https://deng.codexradar.com/">CodexRadar</a>. Pricing and context metadata are additionally refreshed from <a href="https://github.com/BerriAI/litellm/blob/main/model_prices_and_context_window.json">LiteLLM model_prices_and_context_window.json</a>.
    </td>
  </tr>
</table>
