import { afterEach, describe, expect, it } from 'vitest'
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { checkIcons } from './check-icons.js'

const temporaryRoots = []

afterEach(() => {
  for (const root of temporaryRoots.splice(0)) {
    rmSync(root, { recursive: true, force: true })
  }
})

function createFixture(configSource, componentSource) {
  const root = mkdtempSync(join(tmpdir(), 'ccx-icon-check-'))
  temporaryRoots.push(root)
  mkdirSync(join(root, 'src/plugins'), { recursive: true })
  mkdirSync(join(root, 'src/components'), { recursive: true })
  writeFileSync(join(root, 'src/plugins/vuetify.ts'), configSource)
  writeFileSync(join(root, 'src/components/Example.vue'), componentSource)
  return root
}

describe('SVG 图标静态检查', () => {
  it('接受已完成导入和映射的图标', () => {
    const root = createFixture(
      `import { mdiAccountMultipleOutline } from '@mdi/js'
const iconMap = {
  'account-multiple-outline': mdiAccountMultipleOutline,
}`,
      '<template><v-icon>mdi-account-multiple-outline</v-icon></template>',
    )

    expect(checkIcons(root).issues).toEqual([])
  })

  it('拒绝使用了图标但未注册 iconMap 映射的源码', () => {
    const root = createFixture(
      `import { mdiAccountMultipleOutline } from '@mdi/js'
const iconMap = {
  'other-icon': mdiAccountMultipleOutline,
}`,
      '<template><v-icon>mdi-account-multiple-outline</v-icon></template>',
    )

    const result = checkIcons(root)
    expect(result.issues).toHaveLength(1)
    expect(result.issues[0]).toMatchObject({
      file: 'src/components/Example.vue',
      line: 1,
    })
    expect(result.issues[0].message).toContain('iconMap 未注册 account-multiple-outline')
  })

  it('拒绝 iconMap 引用了图标但未从 @mdi/js 导入的配置', () => {
    const root = createFixture(
      `const iconMap = {
  'account-multiple-outline': mdiAccountMultipleOutline,
}`,
      '<template><v-icon>mdi-account-multiple-outline</v-icon></template>',
    )

    const result = checkIcons(root)
    expect(result.issues).toHaveLength(1)
    expect(result.issues[0].file).toBe('src/plugins/vuetify.ts')
    expect(result.issues[0].message).toContain('没有从 @mdi/js 导入')
  })
})
