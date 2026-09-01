#!/usr/bin/env node

import { readFile, writeFile } from 'node:fs/promises'
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

const DEFAULT_INPUT = '/tmp/benchmark-viz-data.json'
const DEFAULT_OUTPUT = resolve(import.meta.dirname, '../docs/public/benchmark/benchmark-chart.html')

function optionValue(args, name, fallback) {
  const inline = args.find(arg => arg.startsWith(`${name}=`))
  if (inline) return inline.slice(name.length + 1)
  const index = args.indexOf(name)
  return index >= 0 && args[index + 1] ? args[index + 1] : fallback
}

export function validateVisualizationData(raw) {
  if (!raw || !Array.isArray(raw.data)) throw new Error('输入文件缺少 data 数组')
  const qualityTiers = raw.qualityTiers || {
    scale: '0-100',
    algorithm: 'medium-aligned-coding-v1-fallback',
    source: 'benchmark-chart',
    premiumMin: 75,
    highMin: 61,
    normalMin: 55,
  }
  const boundaries = [qualityTiers.normalMin, qualityTiers.highMin, qualityTiers.premiumMin]
  if (qualityTiers.scale !== '0-100' || boundaries.some(value => !Number.isFinite(value))
      || boundaries.some(value => value < 0 || value > 100)
      || boundaries.some((value, index) => index > 0 && value < boundaries[index - 1])) {
    throw new Error('输入文件的 qualityTiers 无效')
  }
  // quality_score 为逐档口径：可靠档有值，小样本档为 null（保留行、不评定档位）
  const rows = raw.data.filter(row => (
    row && typeof row.model === 'string' && typeof row.source === 'string'
      && Number.isFinite(row.pass_rate)
      && (row.quality_score == null || Number.isFinite(row.quality_score))
      && (Number.isFinite(row.mean_cost) || Number.isFinite(row.median_cost))
  ))
  const comparisons = (Array.isArray(raw.comparisons) ? raw.comparisons : []).filter(row => (
    row && typeof row.model === 'string' && typeof row.source === 'string'
      && typeof row.category === 'string' && Number.isFinite(row.score)
  ))
  if (rows.length === 0 && comparisons.length === 0) {
    throw new Error('输入文件没有可绘制的 benchmark 数据')
  }
  return { rows, comparisons, qualityTiers }
}

export function renderBenchmarkChart(rows, comparisons = [], qualityTiers = null) {
  const resolvedQualityTiers = qualityTiers || {
    scale: '0-100',
    algorithm: 'medium-aligned-coding-v1-fallback',
    source: 'benchmark-chart',
    premiumMin: 75,
    highMin: 61,
    normalMin: 55,
  }
  const serializedRows = JSON.stringify(rows).replaceAll('<', '\\u003c')
  const serializedComparisons = JSON.stringify(comparisons).replaceAll('<', '\\u003c')
  const serializedQualityTiers = JSON.stringify(resolvedQualityTiers).replaceAll('<', '\\u003c')
  return `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>模型能力-成本边界</title>
<style>
:root {
  color-scheme: light dark;
  --background: #f7f8f6;
  --surface: #ffffff;
  --foreground: #151714;
  --muted: #62675f;
  --border: #d8ddd5;
  --grid: #e7eae5;
  --accent: #176a4b;
  --accent-soft: #dceee5;
  --tier-low: #f0f1ee;
  --tier-normal: #e7eef5;
  --tier-high: #e5f1ec;
  --tier-premium: #f3ecdf;
  --frontier: #111713;
  --series-1: #1769aa;
  --series-2: #b44b27;
  --series-3: #168269;
  --series-4: #8a50a5;
  --series-5: #c17b08;
  --series-6: #c33b56;
  --series-7: #417c24;
  --series-8: #4f5fc4;
  --series-9: #9b5c18;
  --series-10: #287a86;
}
@media (prefers-color-scheme: dark) {
  :root {
    --background: #171917;
    --surface: #202320;
    --foreground: #f1f3ef;
    --muted: #aeb5aa;
    --border: #3b403a;
    --grid: #2c302c;
    --accent: #58c899;
    --accent-soft: #233d31;
    --tier-low: #212421;
    --tier-normal: #202831;
    --tier-high: #203129;
    --tier-premium: #342f24;
    --frontier: #f4f6f2;
    --series-1: #62a9e4;
    --series-2: #ef845f;
    --series-3: #4fc2a5;
    --series-4: #c58cdb;
    --series-5: #e6ae43;
    --series-6: #ed7890;
    --series-7: #86be65;
    --series-8: #8996ea;
    --series-9: #d69a57;
    --series-10: #65b6c0;
  }
}
* { box-sizing: border-box; }
body {
  margin: 0;
  background: var(--background);
  color: var(--foreground);
  font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  font-size: 14px;
  letter-spacing: 0;
}
.page { width: min(1180px, 100%); margin: 0 auto; padding: 24px; }
header { display: flex; align-items: baseline; justify-content: space-between; gap: 16px; margin-bottom: 18px; }
h1 { margin: 0; font-size: 22px; font-weight: 500; }
#summary { color: var(--muted); font-size: 12px; text-align: right; }
.controls { display: flex; align-items: end; flex-wrap: wrap; gap: 14px 20px; padding: 12px 0; border-block: 1px solid var(--border); }
.control { display: grid; gap: 6px; }
.control-label { color: var(--muted); font-size: 11px; font-weight: 500; }
.segmented { display: inline-flex; padding: 2px; border: 1px solid var(--border); border-radius: 6px; background: var(--surface); }
.segmented button { min-height: 30px; padding: 4px 11px; border: 0; border-radius: 4px; color: var(--muted); background: transparent; font: inherit; cursor: pointer; }
.segmented button[aria-pressed="true"] { color: var(--foreground); background: var(--accent-soft); }
select { min-height: 36px; padding: 5px 30px 5px 10px; border: 1px solid var(--border); border-radius: 6px; color: var(--foreground); background: var(--surface); font: inherit; }
button:focus-visible, select:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.chart-shell { position: relative; width: 100%; margin-top: 18px; }
.benchmark-chart { display: block; width: 100%; color: var(--foreground); }
.benchmark-chart .grid-line { stroke: var(--grid); stroke-width: 1; vector-effect: non-scaling-stroke; }
.benchmark-chart .quality-band { pointer-events: none; }
.benchmark-chart .quality-band-boundary { stroke: var(--border); stroke-width: 1; stroke-dasharray: 4 4; vector-effect: non-scaling-stroke; }
.benchmark-chart .quality-band-label { fill: var(--muted); font-size: 10px; font-weight: 500; }
.benchmark-chart .axis-line { stroke: var(--border); stroke-width: 1; vector-effect: non-scaling-stroke; }
.benchmark-chart .axis-text, .benchmark-chart .axis-title { fill: var(--muted); font-size: 11px; }
.benchmark-chart .axis-title { font-size: 12px; }
.benchmark-chart .trajectory { fill: none; stroke-width: 1.5; opacity: .42; vector-effect: non-scaling-stroke; transition: d 180ms ease, opacity 120ms ease, stroke-width 120ms ease; }
.benchmark-chart .trajectory.is-dim { opacity: .07; }
.benchmark-chart .trajectory.is-highlight { opacity: .9; stroke-width: 2.5; }
.benchmark-chart .frontier { fill: none; stroke: var(--frontier); stroke-width: 2.5; vector-effect: non-scaling-stroke; transition: d 180ms ease; }
.benchmark-chart .point { stroke: var(--surface); stroke-width: 1.5; cursor: crosshair; vector-effect: non-scaling-stroke; transition: cx 180ms ease, cy 180ms ease, opacity 120ms ease; }
.benchmark-chart .point.is-pareto { stroke: var(--frontier); stroke-width: 2; }
.benchmark-chart .point.is-dim { opacity: .12; }
.benchmark-chart .point.low-sample { opacity: .38; stroke-dasharray: 3 2; }
.benchmark-chart .label { fill: var(--foreground); font-size: 11px; font-weight: 500; }
.benchmark-chart .label-link { stroke: var(--border); stroke-width: 1; vector-effect: non-scaling-stroke; }
.tooltip { position: absolute; z-index: 2; width: max-content; max-width: min(260px, calc(100% - 16px)); padding: 9px 11px; border: 1px solid var(--border); border-radius: 6px; background: var(--surface); color: var(--foreground); box-shadow: 0 8px 22px rgb(0 0 0 / .14); pointer-events: none; opacity: 0; transform: translateY(3px); transition: opacity 100ms ease, transform 100ms ease; }
.tooltip.visible { opacity: 1; transform: translateY(0); }
.tooltip strong { display: block; margin-bottom: 4px; font-weight: 500; }
.tooltip-line { color: var(--muted); font-size: 12px; }
.legend { display: flex; flex-wrap: wrap; gap: 7px 15px; min-height: 24px; margin: 4px 0 20px; color: var(--muted); font-size: 11px; }
.legend-item { display: inline-flex; align-items: center; gap: 6px; cursor: default; transition: opacity 120ms ease; }
.legend-item.is-active { color: var(--foreground); }
.legend-item.is-dim { opacity: .35; }
.legend-line { width: 17px; height: 3px; border-radius: 2px; }
.quality-legend { display: flex; flex-wrap: wrap; gap: 6px 14px; margin: 8px 0 0; color: var(--muted); font-size: 11px; }
.quality-legend-item { display: inline-flex; align-items: center; gap: 6px; }
.quality-swatch { width: 12px; height: 12px; border: 1px solid var(--border); border-radius: 3px; }
.comparison-section { margin: 28px 0 22px; padding-top: 18px; border-top: 1px solid var(--border); }
.comparison-heading { display: flex; align-items: end; justify-content: space-between; gap: 18px; }
.section-note { max-width: 720px; margin: 5px 0 0; color: var(--muted); font-size: 11px; line-height: 1.5; }
.comparison-chart .comparison-row { stroke: var(--grid); stroke-width: 1; vector-effect: non-scaling-stroke; }
.comparison-chart .comparison-range { stroke: var(--border); stroke-width: 2; vector-effect: non-scaling-stroke; }
.comparison-chart .comparison-point { stroke: var(--surface); stroke-width: 1.5; cursor: crosshair; vector-effect: non-scaling-stroke; }
.comparison-chart .comparison-point.low-sample { opacity: .38; stroke-dasharray: 3 2; }
.comparison-chart .comparison-value.low-sample { opacity: .45; }
.comparison-chart .comparison-value { fill: var(--muted); font-size: 10px; font-variant-numeric: tabular-nums; }
.comparison-meta { display: flex; align-items: baseline; justify-content: space-between; gap: 12px; }
#comparison-summary { color: var(--muted); font-size: 11px; }
.hidden { display: none !important; }
.table-section { border-top: 1px solid var(--border); padding-top: 14px; }
.table-heading { display: flex; justify-content: space-between; align-items: baseline; gap: 12px; margin-bottom: 8px; }
h2 { margin: 0; font-size: 15px; font-weight: 500; }
#table-count { color: var(--muted); font-size: 11px; }
table { width: 100%; border-collapse: collapse; table-layout: fixed; font-size: 12px; }
th, td { padding: 8px 9px; border-bottom: 1px solid var(--grid); text-align: left; vertical-align: middle; overflow-wrap: anywhere; }
th { color: var(--muted); font-size: 11px; font-weight: 500; }
tbody tr:hover { background: var(--accent-soft); }
.numeric { text-align: right; font-variant-numeric: tabular-nums; }
.pareto-mark { color: var(--accent); font-weight: 500; }
.col-model { width: 22%; }
.col-effort { width: 10%; }
.col-score { width: 12%; }
.col-rate, .col-cost { width: 13%; }
.col-pareto { width: 12%; }
.col-source { width: 18%; }
@media (prefers-reduced-motion: reduce) { * { transition-duration: 0s !important; } }
@media (max-width: 640px) {
  .page { padding: 16px 12px; }
  header { display: block; }
  #summary { margin-top: 6px; text-align: left; }
  .controls { align-items: stretch; gap: 12px; }
  .comparison-heading, .comparison-meta { align-items: stretch; flex-direction: column; }
  .control { flex: 1 1 140px; }
  .segmented { display: flex; }
  .segmented button { flex: 1; }
  select { width: 100%; }
  th, td { padding-inline: 5px; }
  .col-model { width: 38%; }
  .col-effort { width: 18%; }
  .col-rate, .col-cost { width: 22%; }
  .col-pareto, .col-source { display: none; }
}
</style>
</head>
<body>
<main class="page">
  <header>
    <h1>模型能力-成本边界</h1>
    <div id="summary" aria-live="polite"></div>
  </header>
  <div class="controls" aria-label="图表筛选">
    <div class="control">
      <span class="control-label">成本口径</span>
      <div class="segmented" id="metric-control">
        <button type="button" data-value="mean_cost" aria-pressed="true">平均成本</button>
        <button type="button" data-value="median_cost" aria-pressed="false">中位成本</button>
      </div>
    </div>
    <div class="control">
      <span class="control-label">成本范围</span>
      <div class="segmented" id="range-control">
        <button type="button" data-value="focus" aria-pressed="true">聚焦范围</button>
        <button type="button" data-value="full" aria-pressed="false">全部成本</button>
      </div>
    </div>
    <label class="control" for="source-control">
      <span class="control-label">数据源</span>
      <select id="source-control"></select>
    </label>
  </div>
  <section class="chart-shell" id="chart-shell">
    <svg class="benchmark-chart" id="chart" role="img" aria-labelledby="chart-title chart-description">
      <title id="chart-title">模型 pass@1 与单任务成本散点图</title>
      <desc id="chart-description">纵轴为各思考强度档的实测 pass@1，轨迹展示同一模型随 effort 升高的能力增益；背景色带为自动质量档边界，档位按各点自身实测 pass@1 逐档评定（同一模型不同 effort 档可分属不同档位），深色折线表示成本与实测 pass@1 的 Pareto 前沿。</desc>
      <defs><clipPath id="plot-clip"><rect id="clip-rect"></rect></clipPath></defs>
      <g id="quality-bands" clip-path="url(#plot-clip)" aria-label="自动质量档背景区域"></g>
      <g id="grid"></g>
      <g id="axes"></g>
      <g id="trajectories" clip-path="url(#plot-clip)"></g>
      <path id="frontier" class="frontier" clip-path="url(#plot-clip)"></path>
      <g id="points" clip-path="url(#plot-clip)"></g>
      <g id="labels"></g>
    </svg>
    <div class="tooltip" id="tooltip" role="status"></div>
  </section>
  <div class="quality-legend" id="quality-legend" aria-label="自动质量档图例"></div>
  <div class="legend" id="legend" aria-label="模型图例"></div>
  <section class="comparison-section" id="comparison-section">
    <div class="comparison-heading">
      <div>
        <h2>多来源能力比较</h2>
        <p class="section-note">按同一能力类别展示 BenchLM.ai、DeepSWE、CodexRadar 与 Artificial Analysis 的原始分数。不同基准的任务集和评分口径不同，仅用于观察来源内相对位置。任务数不足 3 的档位（半透明虚线点）视为样本不足：该档不评定质量档，排序时也不计入，仅供观察。</p>
      </div>
      <label class="control" for="comparison-category-control">
        <span class="control-label">能力类别</span>
        <select id="comparison-category-control"></select>
      </label>
    </div>
    <div class="chart-shell" id="comparison-shell">
      <svg class="benchmark-chart comparison-chart" id="comparison-chart" role="img" aria-labelledby="comparison-title comparison-description">
        <title id="comparison-title">多来源模型能力分数比较</title>
        <desc id="comparison-description">同一模型在不同 benchmark 来源中的原始能力分数。</desc>
        <g id="comparison-grid"></g>
        <g id="comparison-axes"></g>
        <g id="comparison-ranges"></g>
        <g id="comparison-points"></g>
      </svg>
      <div class="tooltip" id="comparison-tooltip" role="status"></div>
    </div>
    <div class="comparison-meta">
      <div class="legend" id="comparison-legend" aria-label="数据来源图例"></div>
      <div id="comparison-summary" aria-live="polite"></div>
    </div>
  </section>
  <section class="table-section">
    <div class="table-heading"><h2>当前范围</h2><span id="table-count"></span></div>
    <table>
      <thead><tr>
        <th class="col-model">模型</th><th class="col-effort">强度</th>
        <th class="col-score numeric">质量档</th><th class="col-rate numeric">pass@1</th><th class="col-cost numeric" id="cost-heading">平均成本</th>
        <th class="col-pareto">边界</th><th class="col-source">数据源</th>
      </tr></thead>
      <tbody id="table-body"></tbody>
    </table>
  </section>
</main>
<script>
const RAW_ROWS = ${serializedRows};
const COMPARISON_ROWS = ${serializedComparisons};
const QUALITY_TIERS = ${serializedQualityTiers};
const state = { metric: 'mean_cost', range: 'focus', source: 'all' };
const effortRank = new Map([['off', -2], ['minimal', -1], ['low', 0], ['medium', 1], ['high', 2], ['xhigh', 3], ['max', 4], ['ultra', 5]]);
// 小样本阈值与 scripts/benchmark-sources/visualization.mjs 的 MIN_RELIABLE_TASKS 一致：
// 任务数不足的档位不参与 Pareto 前沿与轨迹，仅在图上以半透明空点标注供参考。
const LOW_SAMPLE_TASKS = 3;
function isLowSampleRow(row) {
  return Number.isFinite(row.taskCount) && row.taskCount > 0 && row.taskCount < LOW_SAMPLE_TASKS;
}
// 质量档按各点自身实测 pass@1 逐档评定：同一模型不同 effort 档可分属不同档位。
// quality_score 为 null（小样本档）时不评定。
const TIER_RANK = { premium: 3, high: 2, normal: 1, low: 0 };
function tierOf(row) {
  if (!Number.isFinite(row.quality_score)) return null;
  if (row.quality_score >= QUALITY_TIERS.premiumMin) return 'premium';
  if (row.quality_score >= QUALITY_TIERS.highMin) return 'high';
  if (row.quality_score >= QUALITY_TIERS.normalMin) return 'normal';
  return 'low';
}
const palette = Array.from({ length: 10 }, (_, index) => 'var(--series-' + (index + 1) + ')');
// 同族模型的展示排序覆盖：须在 compareModels 之前声明，否则顶层 sort 触发 TDZ。
const modelOrderOverrides = new Map([
  ['gpt-5.6-sol', 0],
  ['gpt-5.6-terra', 1],
  ['gpt-5.6-luna', 2],
]);
// 版本感知模型排序：与 scripts/benchmark-sources/mapper.mjs 的 compareCanonicalModels 等价。
// 版本号降序（opus-5 在 opus-4.8 前）、预发布后缀（preview/beta/...）排在正式版后。
const PRERELEASE_TAGS = ['preview', 'beta', 'alpha', 'rc', 'pre', 'dev', 'nightly'];
function parseModelVersion(model) {
  const s = String(model).toLowerCase();
  const m = s.match(/\\d+(?:[.-]\\d+)*/);
  if (!m) return { family: s, nums: [], suffix: '' };
  const family = s.slice(0, m.index).replace(/[-/]+$/, '');
  const nums = m[0].split(/[.-]/).map(Number);
  const suffix = s.slice(m.index + m[0].length).replace(/^[-/]+/, '');
  return { family, nums, suffix };
}
function suffixRank(suffix) {
  if (!suffix) return -1;
  const idx = PRERELEASE_TAGS.findIndex(tag => suffix === tag || suffix.startsWith(tag + '-') || suffix.startsWith(tag + '.'));
  return idx >= 0 ? idx : PRERELEASE_TAGS.length + 1;
}
// 次版本段的十进制比较（grok-4.20 = 4.2 < 4.3）：位数不同右补零对齐，主版本段仍是整数。
function compareDecimalSegment(x, y) {
  const px = String(x).padEnd(Math.max(String(x).length, String(y).length), '0');
  const py = String(y).padEnd(Math.max(String(x).length, String(y).length), '0');
  return px < py ? -1 : px > py ? 1 : 0;
}
function compareModels(a, b) {
  const pa = parseModelVersion(a);
  const pb = parseModelVersion(b);
  if (pa.family !== pb.family) return pa.family.localeCompare(pb.family);
  const overrideA = modelOrderOverrides.get(a);
  const overrideB = modelOrderOverrides.get(b);
  if (overrideA != null || overrideB != null) {
    if (overrideA == null) return 1;
    if (overrideB == null) return -1;
    return overrideA - overrideB;
  }
  const len = Math.max(pa.nums.length, pb.nums.length);
  for (let i = 0; i < len; i++) {
    const x = pa.nums[i] ?? 0;
    const y = pb.nums[i] ?? 0;
    if (x === y) continue;
    return i === 0 ? y - x : -compareDecimalSegment(x, y);
  }
  const ra = suffixRank(pa.suffix);
  const rb = suffixRank(pb.suffix);
  const aPre = ra >= 0 && ra < PRERELEASE_TAGS.length;
  const bPre = rb >= 0 && rb < PRERELEASE_TAGS.length;
  if (aPre !== bPre) return aPre ? 1 : -1;
  if (aPre && bPre && ra !== rb) return ra - rb;
  return pa.suffix.localeCompare(pb.suffix);
}
const modelNames = [...new Set(RAW_ROWS.map(row => row.model))].sort(compareModels);
const modelSeries = [...new Set(RAW_ROWS.map(row => row.model + '|' + row.source))]
  .sort((a, b) => compareModels(a.split('|')[0], b.split('|')[0]) || a.split('|')[1].localeCompare(b.split('|')[1]));
const colors = new Map(modelSeries.map((series, index) => [series, palette[index % palette.length]]));
const svg = document.getElementById('chart');
const shell = document.getElementById('chart-shell');
const tooltip = document.getElementById('tooltip');
const comparisonSvg = document.getElementById('comparison-chart');
const comparisonShell = document.getElementById('comparison-shell');
const comparisonTooltip = document.getElementById('comparison-tooltip');
const comparisonSources = [...new Set(COMPARISON_ROWS.map(row => row.source))].sort();
const comparisonColors = new Map(comparisonSources.map((source, index) => [source, palette[index % palette.length]]));
const categoryLabels = {
  overall: '综合', knowledge: '知识', math: '数学', coding: '编程', agentic: '智能体', multimodal: '多模态',
  image_arena: '图像 Arena Elo',
};
const comparisonState = { category: null };
const ns = 'http://www.w3.org/2000/svg';
const qualityBands = [
  { key: 'low', label: 'Low', from: 0, to: () => QUALITY_TIERS.normalMin, color: 'var(--tier-low)' },
  { key: 'normal', label: 'Normal', from: () => QUALITY_TIERS.normalMin, to: () => QUALITY_TIERS.highMin, color: 'var(--tier-normal)' },
  { key: 'high', label: 'High', from: () => QUALITY_TIERS.highMin, to: () => QUALITY_TIERS.premiumMin, color: 'var(--tier-high)' },
  { key: 'premium', label: 'Premium', from: () => QUALITY_TIERS.premiumMin, to: 100, color: 'var(--tier-premium)' },
];
let geometry = null;

function svgNode(name, attributes, text) {
  const node = document.createElementNS(ns, name);
  Object.entries(attributes || {}).forEach(([key, value]) => node.setAttribute(key, String(value)));
  if (text != null) node.textContent = text;
  return node;
}

function quantile(values, q) {
  const sorted = [...values].sort((a, b) => a - b);
  if (!sorted.length) return 0;
  const position = (sorted.length - 1) * q;
  const lower = Math.floor(position);
  const fraction = position - lower;
  return sorted[lower + 1] == null ? sorted[lower] : sorted[lower] + fraction * (sorted[lower + 1] - sorted[lower]);
}

function paretoFrontier(rows) {
  const sorted = [...rows].sort((a, b) => a.cost - b.cost || b.pass_rate - a.pass_rate);
  const frontier = [];
  let bestScore = -Infinity;
  sorted.forEach(row => {
    if (row.pass_rate > bestScore + 1e-9) {
      frontier.push(row);
      bestScore = row.pass_rate;
    }
  });
  return frontier;
}

function niceMax(value) {
  if (value <= 0) return 1;
  const magnitude = 10 ** Math.floor(Math.log10(value));
  const normalized = value / magnitude;
  const rounded = [1, 1.2, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10].find(candidate => normalized <= candidate) || 10;
  return rounded * magnitude;
}

function ticks(minimum, maximum, count) {
  const span = maximum - minimum;
  const rawStep = span / Math.max(1, count);
  const magnitude = 10 ** Math.floor(Math.log10(rawStep || 1));
  const normalized = rawStep / magnitude;
  const step = (normalized <= 1 ? 1 : normalized <= 2 ? 2 : normalized <= 5 ? 5 : 10) * magnitude;
  const values = [];
  for (let value = Math.ceil(minimum / step) * step; value <= maximum + step * .01; value += step) values.push(value);
  return values;
}

function currentRows() {
  return RAW_ROWS
    .filter(row => state.source === 'all' || row.source === state.source)
    .filter(row => Number.isFinite(row.pass_rate) && Number.isFinite(row[state.metric]))
    .map(row => ({ ...row, cost: row[state.metric], key: [row.model, row.effort || 'default', row.source].join('|') }));
}

function linePath(rows, x, y) {
  return rows.map((row, index) => (index ? 'L' : 'M') + x(row.cost).toFixed(2) + ',' + y(row.pass_rate * 100).toFixed(2)).join(' ');
}

function setGeometry(rows) {
  const width = Math.max(300, Math.round(shell.clientWidth));
  const height = width < 560 ? 520 : 560;
  const margin = { top: 26, right: width < 560 ? 12 : 24, bottom: 66, left: width < 560 ? 54 : 66 };
  const plotWidth = Math.max(200, width - margin.left - margin.right);
  const plotHeight = height - margin.top - margin.bottom;
  const costs = rows.map(row => row.cost);
  const focusMax = quantile(costs, .95);
  const visibleMax = state.range === 'focus' ? focusMax : Math.max(...costs);
  const xMax = niceMax(visibleMax * 1.04);
  const scores = rows.filter(row => row.cost <= xMax).map(row => row.pass_rate * 100);
  // Y 轴随可见数据自适应展开；pass@1 理论区间 [0,100]，上下限不得越界
  const dataMin = Math.min(...scores);
  const dataMax = Math.max(...scores);
  const span = Math.max(10, dataMax - dataMin);
  const yMin = Math.max(0, Math.floor((dataMin - span * .07) / 5) * 5);
  const yMax = Math.min(100, Math.max(yMin + 10, Math.ceil((dataMax + span * .07) / 5) * 5));
  const x = value => margin.left + value / xMax * plotWidth;
  const y = value => margin.top + (yMax - value) / (yMax - yMin) * plotHeight;
  svg.setAttribute('viewBox', '0 0 ' + width + ' ' + height);
  svg.style.height = height + 'px';
  document.getElementById('clip-rect').setAttribute('x', margin.left);
  document.getElementById('clip-rect').setAttribute('y', margin.top);
  document.getElementById('clip-rect').setAttribute('width', plotWidth);
  document.getElementById('clip-rect').setAttribute('height', plotHeight);
  geometry = { width, height, margin, plotWidth, plotHeight, xMax, yMin, yMax, x, y };
  return geometry;
}

function renderAxes(g) {
  const grid = document.getElementById('grid');
  const axes = document.getElementById('axes');
  grid.replaceChildren();
  axes.replaceChildren();
  const bottom = g.margin.top + g.plotHeight;
  const right = g.margin.left + g.plotWidth;
  ticks(0, g.xMax, 6).forEach(value => {
    const x = g.x(value);
    grid.append(svgNode('line', { class: 'grid-line', x1: x, x2: x, y1: g.margin.top, y2: bottom }));
    axes.append(svgNode('text', { class: 'axis-text', x, y: bottom + 21, 'text-anchor': 'middle' }, '$' + (value < 1 ? value.toFixed(1) : value.toFixed(0))));
  });
  ticks(g.yMin, g.yMax, 6).forEach(value => {
    const y = g.y(value);
    grid.append(svgNode('line', { class: 'grid-line', x1: g.margin.left, x2: right, y1: y, y2: y }));
    axes.append(svgNode('text', { class: 'axis-text', x: g.margin.left - 9, y: y + 4, 'text-anchor': 'end' }, String(Math.round(value))));
  });
  axes.append(svgNode('line', { class: 'axis-line', x1: g.margin.left, x2: right, y1: bottom, y2: bottom }));
  axes.append(svgNode('line', { class: 'axis-line', x1: g.margin.left, x2: g.margin.left, y1: g.margin.top, y2: bottom }));
  axes.append(svgNode('text', { class: 'axis-title', x: g.margin.left + g.plotWidth / 2, y: g.height - 13, 'text-anchor': 'middle' }, state.metric === 'mean_cost' ? '平均成本（USD / task）' : '中位成本（USD / task）'));
  const yTitle = svgNode('text', { class: 'axis-title', x: -(g.margin.top + g.plotHeight / 2), y: 14, transform: 'rotate(-90)', 'text-anchor': 'middle' }, 'pass@1（%）');
  axes.append(yTitle);
}

function renderQualityBands(g) {
  const container = document.getElementById('quality-bands');
  container.replaceChildren();
  const bottom = g.margin.top + g.plotHeight;
  const right = g.margin.left + g.plotWidth;
  qualityBands.forEach(band => {
    const from = typeof band.from === 'function' ? band.from() : band.from;
    const to = typeof band.to === 'function' ? band.to() : band.to;
    const visibleFrom = Math.max(g.yMin, from);
    const visibleTo = Math.min(g.yMax, to);
    if (visibleTo <= visibleFrom) return;
    const yTop = g.y(visibleTo);
    const height = g.y(visibleFrom) - yTop;
    container.append(svgNode('rect', {
      class: 'quality-band', x: g.margin.left, y: yTop, width: g.plotWidth, height,
      fill: band.color, role: 'img', 'aria-label': band.label + ' 质量档',
    }));
  });
  [QUALITY_TIERS.normalMin, QUALITY_TIERS.highMin, QUALITY_TIERS.premiumMin].forEach((value, index) => {
    if (value < g.yMin || value > g.yMax) return;
    const y = g.y(value);
    const label = ['normal', 'high', 'premium'][index];
    container.append(svgNode('line', {
      class: 'quality-band-boundary', x1: g.margin.left, x2: right, y1: y, y2: y,
    }));
    container.append(svgNode('text', {
      class: 'quality-band-label', x: g.margin.left + 8, y: y - 4,
    }, label + ' ≥ ' + value.toFixed(1)));
  });
  container.append(svgNode('line', { x1: g.margin.left, x2: right, y1: bottom, y2: bottom, display: 'none' }));
}

function renderTrajectories(rows, g) {
  const container = document.getElementById('trajectories');
  container.replaceChildren();
  const groups = new Map();
  rows.forEach(row => {
    // 小样本点的 pass@1 是噪声（1 任务全过 = 100%），连进轨迹会画出误导性折线
    if (isLowSampleRow(row)) return;
    const key = row.model + '|' + row.source;
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push(row);
  });
  groups.forEach(points => {
    points.sort((a, b) => (effortRank.get(a.effort) ?? 99) - (effortRank.get(b.effort) ?? 99));
    if (points.length < 2) return;
    container.append(svgNode('path', { class: 'trajectory', d: linePath(points, g.x, g.y), stroke: colors.get(points[0].model + '|' + points[0].source), 'data-model': points[0].model, 'data-source': points[0].source }));
  });
}

function tooltipHtml(row) {
  const effort = row.effort || 'default';
  const costLabel = state.metric === 'mean_cost' ? '平均成本' : '中位成本';
  const taskLine = Number.isFinite(row.taskCount) && row.taskCount > 0
    ? '<div class="tooltip-line">任务数 ' + row.taskCount + (isLowSampleRow(row) ? '（样本不足，仅供参考）' : '') + '</div>'
    : '';
  return '<strong>' + escapeHtml(row.model) + ' · ' + escapeHtml(effort) + '</strong>'
    + '<div class="tooltip-line">pass@1 ' + (row.pass_rate * 100).toFixed(1) + '%</div>'
    + '<div class="tooltip-line">质量档 ' + (tierOf(row) ?? '未定（样本不足）') + '</div>'
    + (Number.isFinite(row.model_quality_score)
      ? '<div class="tooltip-line">常规档等效 ' + row.model_quality_score.toFixed(1) + '</div>'
      : '')
    + '<div class="tooltip-line">' + costLabel + ' $' + row.cost.toFixed(3) + '</div>'
    + taskLine
    + '<div class="tooltip-line">' + escapeHtml(row.source) + '</div>';
}

function escapeHtml(value) {
  return String(value).replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character]);
}

function showTooltip(row, pointX, pointY) {
  tooltip.innerHTML = tooltipHtml(row);
  tooltip.classList.add('visible');
  const shellRect = shell.getBoundingClientRect();
  const svgRect = svg.getBoundingClientRect();
  const scaleX = svgRect.width / geometry.width;
  const scaleY = svgRect.height / geometry.height;
  const anchorX = svgRect.left - shellRect.left + pointX * scaleX;
  const anchorY = svgRect.top - shellRect.top + pointY * scaleY;
  const tooltipRect = tooltip.getBoundingClientRect();
  const left = Math.min(shell.clientWidth - tooltipRect.width - 6, Math.max(6, anchorX + 12));
  const top = Math.min(shell.clientHeight - tooltipRect.height - 6, Math.max(6, anchorY - tooltipRect.height - 10));
  tooltip.style.left = left + 'px';
  tooltip.style.top = top + 'px';
}

function renderPoints(rows, frontierKeys, g) {
  const container = document.getElementById('points');
  container.replaceChildren();
  rows.forEach(row => {
      const isPareto = frontierKeys.has(row.key);
    const lowSample = isLowSampleRow(row);
    const circle = svgNode('circle', {
      class: 'point' + (isPareto ? ' is-pareto' : '') + (lowSample ? ' low-sample' : ''), cx: g.x(row.cost), cy: g.y(row.pass_rate * 100),
      r: isPareto ? 5.5 : 4.5, fill: colors.get(row.model + '|' + row.source), 'aria-label': row.model + ' ' + (row.effort || 'default') + ' ' + row.source,
      'data-model': row.model, 'data-source': row.source,
    });
    circle.append(svgNode('title', {}, row.model + ' · ' + (row.effort || 'default') + ' · ' + (row.pass_rate * 100).toFixed(1) + ' · $' + row.cost.toFixed(3)));
    circle.addEventListener('mouseenter', () => showTooltip(row, g.x(row.cost), g.y(row.pass_rate * 100)));
    circle.addEventListener('mouseleave', () => tooltip.classList.remove('visible'));
    container.append(circle);
  });
}

function renderLabels(frontier, g) {
  const container = document.getElementById('labels');
  container.replaceChildren();
  const bestByModel = new Map();
  frontier.forEach(row => {
    const current = bestByModel.get(row.model);
    if (!current || row.pass_rate > current.pass_rate) bestByModel.set(row.model, row);
  });
  const labels = [...bestByModel.values()].map(row => ({ row, pointX: g.x(row.cost), pointY: g.y(row.pass_rate * 100) }))
    .sort((a, b) => a.pointY - b.pointY);
  const minGap = 17;
  labels.forEach((label, index) => {
    label.labelY = Math.max(g.margin.top + 11, label.pointY - 7);
    if (index > 0) label.labelY = Math.max(label.labelY, labels[index - 1].labelY + minGap);
  });
  if (labels.length) {
    const overflow = labels[labels.length - 1].labelY - (g.margin.top + g.plotHeight - 3);
    if (overflow > 0) labels.forEach(label => { label.labelY -= overflow; });
  }
  for (let index = labels.length - 2; index >= 0; index--) {
    labels[index].labelY = Math.min(labels[index].labelY, labels[index + 1].labelY - minGap);
  }
  labels.forEach(label => {
    const labelText = label.row.model + ' · ' + label.row.source + (label.row.effort ? ' · ' + label.row.effort : '');
    const textWidth = Math.min(170, labelText.length * 6.2);
    const placeLeft = label.pointX > g.margin.left + g.plotWidth * .68;
    const textX = placeLeft
      ? Math.max(g.margin.left + 2, label.pointX - textWidth - 10)
      : Math.min(g.margin.left + g.plotWidth - textWidth - 2, label.pointX + 10);
    const linkX = placeLeft ? textX + textWidth + 3 : textX - 3;
    container.append(svgNode('line', { class: 'label-link', x1: label.pointX, y1: label.pointY, x2: linkX, y2: label.labelY - 4 }));
    container.append(svgNode('text', { class: 'label', x: textX, y: label.labelY }, labelText));
  });
}

function applyModelHighlight(series) {
  svg.querySelectorAll('[data-model]').forEach(node => {
    const match = series != null && (node.getAttribute('data-model') + '|' + node.getAttribute('data-source')) === series;
    node.classList.toggle('is-dim', series != null && !match);
    if (node.classList.contains('trajectory')) node.classList.toggle('is-highlight', series != null && match);
  });
  document.querySelectorAll('#legend .legend-item').forEach(item => {
    const match = item.dataset.series === series;
    item.classList.toggle('is-active', series != null && match);
    item.classList.toggle('is-dim', series != null && !match);
  });
}

function renderQualityLegend() {
  const legend = document.getElementById('quality-legend');
  legend.replaceChildren();
  const intro = document.createElement('span');
  intro.className = 'quality-legend-item';
  intro.textContent = '档位边界（按各点实测 pass@1 逐档划分）：';
  legend.append(intro);
  qualityBands.forEach(band => {
    const from = typeof band.from === 'function' ? band.from() : band.from;
    const to = typeof band.to === 'function' ? band.to() : band.to;
    const item = document.createElement('span');
    item.className = 'quality-legend-item';
    const swatch = document.createElement('span');
    swatch.className = 'quality-swatch';
    swatch.style.background = band.color;
    const label = document.createElement('span');
    label.textContent = band.label + ' ' + (band.key === 'premium' ? '≥ ' + from.toFixed(1) : band.key === 'low' ? '< ' + to.toFixed(1) : from.toFixed(1) + '–' + to.toFixed(1));
    item.append(swatch, label);
    legend.append(item);
  });
}

function renderLegend(rows) {
  const legend = document.getElementById('legend');
  legend.replaceChildren();
  const series = [...new Set(rows.map(row => row.model + '|' + row.source))]
    .sort((a, b) => compareModels(a.split('|')[0], b.split('|')[0]) || a.split('|')[1].localeCompare(b.split('|')[1]));
  series.forEach(value => {
    const [model, source] = value.split('|');
    const item = document.createElement('span');
    item.className = 'legend-item';
    item.dataset.series = value;
    const swatch = document.createElement('span');
    swatch.className = 'legend-line';
    swatch.style.background = colors.get(value);
    const label = document.createElement('span');
    label.textContent = model + ' · ' + source;
    item.append(swatch, label);
    item.addEventListener('mouseenter', () => applyModelHighlight(value));
    item.addEventListener('mouseleave', () => applyModelHighlight(null));
    legend.append(item);
  });
}

function renderTable(rows, frontierKeys) {
  const body = document.getElementById('table-body');
  body.replaceChildren();
  [...rows].sort((a, b) => (
    (TIER_RANK[tierOf(b)] ?? -1) - (TIER_RANK[tierOf(a)] ?? -1) || a.cost - b.cost
  )).forEach(row => {
    const cells = [row.model, row.effort || 'default', tierOf(row) ?? '—', (row.pass_rate * 100).toFixed(1) + '%', '$' + row.cost.toFixed(3), frontierKeys.has(row.key) ? 'Pareto' : '', row.source];
    const tr = document.createElement('tr');
    cells.forEach((value, index) => {
      const td = document.createElement('td');
      td.textContent = value;
      if (index === 2 || index === 3 || index === 4) td.className = 'numeric';
      if (index === 5) td.className = 'col-pareto pareto-mark';
      if (index === 6) td.className = 'col-source';
      tr.append(td);
    });
    body.append(tr);
  });
  document.getElementById('table-count').textContent = rows.length + ' 个点';
  document.getElementById('cost-heading').textContent = state.metric === 'mean_cost' ? '平均成本' : '中位成本';
}

function update() {
  const allRows = currentRows();
  if (!allRows.length) return;
  const g = setGeometry(allRows);
  const visibleRows = allRows.filter(row => row.cost <= g.xMax + 1e-9);
  const frontier = state.source === 'all' && sources.length > 1 ? [] : paretoFrontier(allRows.filter(row => !isLowSampleRow(row)));
  const visibleFrontier = frontier.filter(row => row.cost <= g.xMax + 1e-9);
  const frontierKeys = new Set(frontier.map(row => row.key));
  renderQualityBands(g);
  renderAxes(g);
  renderTrajectories(visibleRows, g);
  document.getElementById('frontier').setAttribute('d', linePath(visibleFrontier, g.x, g.y));
  renderPoints(visibleRows, frontierKeys, g);
  renderLabels(visibleFrontier, g);
  renderQualityLegend();
  renderLegend(visibleRows);
  renderTable(visibleRows, frontierKeys);
  const hidden = allRows.length - visibleRows.length;
  const modelCount = new Set(visibleRows.map(row => row.model)).size;
  const frontierSummary = frontier.length > 0
    ? ' · ' + visibleFrontier.length + ' 个 Pareto 点'
    : (sources.length > 1 ? ' · 跨来源不计算 Pareto' : '');
  document.getElementById('summary').textContent = visibleRows.length + ' 个点 · ' + modelCount + ' 个模型' + frontierSummary + (hidden ? ' · 隐藏 ' + hidden + ' 个高成本点' : '');
  tooltip.classList.remove('visible');
}

function comparisonCategoryLabel(category) {
  return categoryLabels[category] || category;
}

function currentComparisonRows() {
  return COMPARISON_ROWS
    .filter(row => row.category === comparisonState.category)
    .filter(row => Number.isFinite(row.score) && row.score >= 0 && row.score <= 100);
}

function renderComparisonLegend(rows) {
  const legend = document.getElementById('comparison-legend');
  legend.replaceChildren();
  [...new Set(rows.map(row => row.source))].sort().forEach(source => {
    const item = document.createElement('span');
    item.className = 'legend-item';
    const swatch = document.createElement('span');
    swatch.className = 'legend-line';
    swatch.style.background = comparisonColors.get(source);
    const label = document.createElement('span');
    label.textContent = source;
    item.append(swatch, label);
    legend.append(item);
  });
}

function showComparisonTooltip(row, pointX, pointY, g) {
  const effort = row.effort ? ' · ' + escapeHtml(row.effort) : '';
  const taskLine = Number.isFinite(row.taskCount) && row.taskCount > 0
    ? '<div class="tooltip-line">任务数 ' + row.taskCount + (isLowSampleRow(row) ? '（样本不足，仅供参考）' : '') + '</div>'
    : '';
  comparisonTooltip.innerHTML = '<strong>' + escapeHtml(row.model) + '</strong>'
    + '<div class="tooltip-line">' + escapeHtml(row.source) + effort + '</div>'
    + '<div class="tooltip-line">' + escapeHtml(comparisonCategoryLabel(row.category)) + ' ' + row.score.toFixed(1) + '</div>'
    + taskLine;
  comparisonTooltip.classList.add('visible');
  const shellRect = comparisonShell.getBoundingClientRect();
  const svgRect = comparisonSvg.getBoundingClientRect();
  const anchorX = svgRect.left - shellRect.left + pointX * svgRect.width / g.width;
  const anchorY = svgRect.top - shellRect.top + pointY * svgRect.height / g.height;
  const tooltipRect = comparisonTooltip.getBoundingClientRect();
  comparisonTooltip.style.left = Math.min(comparisonShell.clientWidth - tooltipRect.width - 6, Math.max(6, anchorX + 12)) + 'px';
  comparisonTooltip.style.top = Math.min(comparisonShell.clientHeight - tooltipRect.height - 6, Math.max(6, anchorY - tooltipRect.height - 10)) + 'px';
}

function updateComparison() {
  const rows = currentComparisonRows();
  if (!rows.length) return;

  const grouped = new Map();
  rows.forEach(row => {
    if (!grouped.has(row.model)) grouped.set(row.model, []);
    grouped.get(row.model).push(row);
  });
  // 排序按可信样本的最高分：小样本点（如新模型 1 任务 100%）不把模型顶到最前。
  const topScore = rows => {
    const reliable = rows.filter(row => !isLowSampleRow(row));
    const pool = reliable.length ? reliable : rows;
    return Math.max(...pool.map(row => row.score));
  };
  const models = [...grouped.keys()].sort((a, b) => (
    topScore(grouped.get(b)) - topScore(grouped.get(a)) || a.localeCompare(b)
  ));
  const width = Math.max(300, Math.round(comparisonShell.clientWidth));
  const margin = { top: 28, right: 52, bottom: 44, left: width < 560 ? 122 : 190 };
  const rowHeight = width < 560 ? 30 : 34;
  const height = Math.max(250, margin.top + margin.bottom + models.length * rowHeight);
  const plotWidth = Math.max(140, width - margin.left - margin.right);
  const x = value => margin.left + value / 100 * plotWidth;
  const y = index => margin.top + index * rowHeight + rowHeight / 2;
  const g = { width, height, margin, plotWidth, rowHeight, x, y };

  comparisonSvg.setAttribute('viewBox', '0 0 ' + width + ' ' + height);
  comparisonSvg.style.height = height + 'px';
  const grid = document.getElementById('comparison-grid');
  const axes = document.getElementById('comparison-axes');
  const ranges = document.getElementById('comparison-ranges');
  const points = document.getElementById('comparison-points');
  grid.replaceChildren();
  axes.replaceChildren();
  ranges.replaceChildren();
  points.replaceChildren();

  [0, 20, 40, 60, 80, 100].forEach(value => {
    const pointX = x(value);
    grid.append(svgNode('line', { class: 'grid-line', x1: pointX, x2: pointX, y1: margin.top, y2: height - margin.bottom }));
    axes.append(svgNode('text', { class: 'axis-text', x: pointX, y: height - 17, 'text-anchor': 'middle' }, String(value)));
  });
  axes.append(svgNode('text', { class: 'axis-title', x: margin.left + plotWidth / 2, y: height - 2, 'text-anchor': 'middle' }, '原始分数（0–100）'));

  models.forEach((model, modelIndex) => {
    const modelRows = grouped.get(model).sort((a, b) => a.source.localeCompare(b.source));
    const centerY = y(modelIndex);
    grid.append(svgNode('line', { class: 'comparison-row', x1: margin.left, x2: margin.left + plotWidth, y1: centerY, y2: centerY }));
    axes.append(svgNode('text', { class: 'axis-text', x: margin.left - 10, y: centerY + 4, 'text-anchor': 'end' }, model));
    if (modelRows.length > 1) {
      const scores = modelRows.map(row => row.score);
      ranges.append(svgNode('line', {
        class: 'comparison-range', x1: x(Math.min(...scores)), x2: x(Math.max(...scores)), y1: centerY, y2: centerY,
      }));
    }
    modelRows.forEach((row, sourceIndex) => {
      const offset = (sourceIndex - (modelRows.length - 1) / 2) * 8;
      const pointX = x(row.score);
      const pointY = centerY + offset;
      const lowSample = isLowSampleRow(row);
      const circle = svgNode('circle', {
        class: 'comparison-point' + (lowSample ? ' low-sample' : ''), cx: pointX, cy: pointY, r: 5, fill: comparisonColors.get(row.source),
      });
      circle.append(svgNode('title', {}, row.model + ' · ' + row.source + ' · ' + row.score.toFixed(1)
        + (Number.isFinite(row.taskCount) && row.taskCount > 0 ? ' · ' + row.taskCount + ' 任务' : '')));
      circle.addEventListener('mouseenter', () => showComparisonTooltip(row, pointX, pointY, g));
      circle.addEventListener('mouseleave', () => comparisonTooltip.classList.remove('visible'));
      points.append(circle);
      points.append(svgNode('text', { class: 'comparison-value' + (lowSample ? ' low-sample' : ''), x: pointX + 8, y: pointY + 3 }, row.score.toFixed(1)));
    });
  });

  renderComparisonLegend(rows);
  document.getElementById('comparison-summary').textContent = models.length + ' 个模型 · ' + new Set(rows.map(row => row.source)).size + ' 个来源';
  comparisonTooltip.classList.remove('visible');
}

function bindSegmented(id, field) {
  document.getElementById(id).addEventListener('click', event => {
    const button = event.target.closest('button[data-value]');
    if (!button) return;
    state[field] = button.dataset.value;
    document.querySelectorAll('#' + id + ' button').forEach(item => item.setAttribute('aria-pressed', String(item === button)));
    update();
  });
}

const sourceControl = document.getElementById('source-control');
const sources = [...new Set(RAW_ROWS.map(row => row.source))].sort();
[['all', '全部数据源'], ...sources.map(source => [source, source])].forEach(([value, label]) => {
  const option = document.createElement('option');
  option.value = value;
  option.textContent = label;
  sourceControl.append(option);
});
sourceControl.addEventListener('change', () => { state.source = sourceControl.value; update(); });
bindSegmented('metric-control', 'metric');
bindSegmented('range-control', 'range');
if (RAW_ROWS.length > 0) {
  new ResizeObserver(() => update()).observe(shell);
  update();
}

const comparisonSection = document.getElementById('comparison-section');
const comparisonCategoryControl = document.getElementById('comparison-category-control');
const categoryOrder = ['coding', 'overall', 'agentic', 'knowledge', 'math', 'multimodal'];
const comparisonCategories = [...new Set(COMPARISON_ROWS.map(row => row.category))]
  .sort((a, b) => {
    const rankA = categoryOrder.indexOf(a);
    const rankB = categoryOrder.indexOf(b);
    return (rankA < 0 ? 99 : rankA) - (rankB < 0 ? 99 : rankB) || a.localeCompare(b);
  });
if (comparisonCategories.length === 0) {
  comparisonSection.classList.add('hidden');
} else {
  comparisonState.category = comparisonCategories[0];
  comparisonCategories.forEach(category => {
    const option = document.createElement('option');
    option.value = category;
    option.textContent = comparisonCategoryLabel(category);
    comparisonCategoryControl.append(option);
  });
  comparisonCategoryControl.addEventListener('change', () => {
    comparisonState.category = comparisonCategoryControl.value;
    updateComparison();
  });
  new ResizeObserver(() => updateComparison()).observe(comparisonShell);
  updateComparison();
}
</script>
</body>
</html>
`
}

async function main() {
  const args = process.argv.slice(2)
  const input = resolve(optionValue(args, '--input', DEFAULT_INPUT))
  const output = resolve(optionValue(args, '--output', DEFAULT_OUTPUT))
  const raw = JSON.parse(await readFile(input, 'utf8'))
  const { rows, comparisons, qualityTiers } = validateVisualizationData(raw)
  await writeFile(output, renderBenchmarkChart(rows, comparisons, qualityTiers), 'utf8')
  console.log(`已生成 ${output}（${rows.length} 个成本点，${comparisons.length} 个多来源比较点）`)
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main().catch(error => {
    console.error(`生成能力-成本曲线失败: ${error.message}`)
    process.exitCode = 1
  })
}
