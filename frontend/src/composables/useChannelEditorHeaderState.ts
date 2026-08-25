import { computed } from 'vue'

type ThemeLike = {
  global: {
    current: {
      value: {
        dark: boolean
      }
    }
  }
}

export function useChannelEditorHeaderState(theme: ThemeLike) {
  const headerClasses = computed(() => {
    const isDark = theme.global.current.value.dark
    // 深色主题头部与卡片同为 surface 色，必须补一条分隔线才能形成标题栏边界
    return isDark ? 'bg-surface text-high-emphasis border-b' : 'bg-primary text-white'
  })

  // 浅色主题头部是 primary 蓝底，同色 avatar 轮廓不可见，改用半透明白底保持层次
  const avatarColor = computed(() => (theme.global.current.value.dark ? 'primary' : 'rgba(255, 255, 255, 0.16)'))

  const headerIconStyle = computed(() => ({
    color: 'rgb(var(--v-theme-on-primary))',
  }))

  const subtitleClasses = computed(() => {
    const isDark = theme.global.current.value.dark
    return isDark ? 'text-medium-emphasis' : 'text-white-subtitle'
  })

  return {
    headerClasses,
    avatarColor,
    headerIconStyle,
    subtitleClasses,
  }
}
