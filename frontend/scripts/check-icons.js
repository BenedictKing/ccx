#!/usr/bin/env node

import { readdirSync, readFileSync } from 'node:fs'
import { extname, relative, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const SOURCE_EXTENSIONS = new Set(['.vue', '.ts', '.tsx', '.js', '.jsx', '.mts', '.cts', '.mjs', '.cjs'])

function sourceFiles(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap(entry => {
    const path = resolve(directory, entry.name)
    if (entry.isDirectory()) return sourceFiles(path)
    return SOURCE_EXTENSIONS.has(extname(entry.name)) ? [path] : []
  })
}

function lineAt(source, index) {
  return source.slice(0, index).split('\n').length
}

export function parseIconConfig(source) {
  const importBlock = source.match(/import\s*\{([\s\S]*?)\}\s*from\s*['"]@mdi\/js['"]/)
  const imports = new Set(importBlock?.[1].match(/\bmdi[A-Z]\w*\b/g) || [])
  const mapBlock = source.match(/const\s+iconMap[^=]*=\s*\{([\s\S]*?)\n\}/)
  const mappings = new Map()
  const pattern = /['"]([\w-]+)['"]\s*:\s*(mdi[A-Z]\w*)/g
  for (const match of mapBlock?.[1].matchAll(pattern) || []) {
    mappings.set(match[1], { binding: match[2], line: lineAt(source, mapBlock.index + match.index) })
  }
  return { imports, mappings }
}

export function checkIcons(frontendRoot) {
  const configPath = resolve(frontendRoot, 'src/plugins/vuetify.ts')
  const configSource = readFileSync(configPath, 'utf8')
  const { imports, mappings } = parseIconConfig(configSource)
  const issues = []

  for (const [name, mapping] of mappings) {
    if (!imports.has(mapping.binding)) {
      issues.push({
        file: 'src/plugins/vuetify.ts',
        line: mapping.line,
        message: `iconMap 的 ${name} 引用了 ${mapping.binding}，但没有从 @mdi/js 导入`,
      })
    }
  }

  const files = sourceFiles(resolve(frontendRoot, 'src'))
    .filter(path => path !== configPath)
    .sort()
  let usageCount = 0
  for (const path of files) {
    const source = readFileSync(path, 'utf8')
    const seen = new Set()
    for (const match of source.matchAll(/\bmdi-[a-z0-9-]+\b/g)) {
      const icon = match[0]
      if (seen.has(icon)) continue
      seen.add(icon)
      usageCount++
      const name = icon.slice(4)
      if (!mappings.has(name)) {
        issues.push({
          file: relative(frontendRoot, path).split(sep).join('/'),
          line: lineAt(source, match.index),
          message: `使用了 ${icon}，但 vuetify.ts 的 iconMap 未注册 ${name}`,
        })
      }
    }
  }

  return { issues, usageCount, mappingCount: mappings.size, fileCount: files.length }
}

function main() {
  const frontendRoot = fileURLToPath(new URL('../', import.meta.url))
  const result = checkIcons(frontendRoot)
  if (result.issues.length > 0) {
    console.error('SVG 图标检查失败：')
    for (const issue of result.issues) {
      console.error(`- ${issue.file}:${issue.line} ${issue.message}`)
    }
    console.error('请同时完成 @mdi/js 导入和 iconMap 映射；模板中的 mdi-* 不会自动加载。')
    process.exitCode = 1
    return
  }
  console.log(`✓ SVG 图标检查通过：${result.fileCount} 个源文件，${result.usageCount} 处使用，${result.mappingCount} 个映射`)
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main()
}
