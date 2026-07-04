import { createVuetify } from 'vuetify'
import { h } from 'vue'
import type { IconSet, IconProps, ThemeDefinition } from 'vuetify'

// 按需导入 Vuetify 组件（减少约 50% 打包体积）
// 布局组件
import { VApp } from 'vuetify/components/VApp'
import { VMain } from 'vuetify/components/VMain'
import { VContainer, VRow, VCol, VSpacer } from 'vuetify/components/VGrid'
import { VAppBar } from 'vuetify/components/VAppBar'

// 卡片与容器
import { VCard, VCardTitle, VCardText, VCardActions } from 'vuetify/components/VCard'
import { VDialog } from 'vuetify/components/VDialog'
import { VOverlay } from 'vuetify/components/VOverlay'
import { VExpansionPanels, VExpansionPanel, VExpansionPanelTitle, VExpansionPanelText } from 'vuetify/components/VExpansionPanel'

// 表单组件
import { VForm } from 'vuetify/components/VForm'
import { VTextField } from 'vuetify/components/VTextField'
import { VTextarea } from 'vuetify/components/VTextarea'
import { VSelect } from 'vuetify/components/VSelect'
import { VCombobox } from 'vuetify/components/VCombobox'
import { VSwitch } from 'vuetify/components/VSwitch'
import { VBtn } from 'vuetify/components/VBtn'
import { VBtnToggle } from 'vuetify/components/VBtnToggle'

// 列表组件
import { VList, VListItem, VListItemTitle, VListItemSubtitle } from 'vuetify/components/VList'
import { VMenu } from 'vuetify/components/VMenu'

// 反馈组件
import { VAlert } from 'vuetify/components/VAlert'
import { VSnackbar } from 'vuetify/components/VSnackbar'
import { VProgressCircular } from 'vuetify/components/VProgressCircular'
import { VTooltip } from 'vuetify/components/VTooltip'

// 数据展示
import { VChip } from 'vuetify/components/VChip'
import { VChipGroup } from 'vuetify/components/VChipGroup'
import { VAvatar } from 'vuetify/components/VAvatar'
import { VIcon } from 'vuetify/components/VIcon'
import { VDivider } from 'vuetify/components/VDivider'

// 表格组件
import { VTable } from 'vuetify/components/VTable'

// 标签页组件
import { VTabs, VTab } from 'vuetify/components/VTabs'

// 窗口组件
import { VWindow, VWindowItem } from 'vuetify/components/VWindow'

// 过渡动画
import { VExpandTransition } from 'vuetify/components/transitions'

// 按需导入指令
import { Ripple, ClickOutside } from 'vuetify/directives'

// 引入样式
import 'vuetify/styles'

// 从 @mdi/js 按需导入使用的图标 (SVG)
// ============================================================
// ⚠️ 重要：模板里写 `<v-icon>mdi-xxx</v-icon>` 并不会自动生效。
// 本项目使用自定义 iconMap，新增图标时必须同时完成下面两步：
//   1. 从 `@mdi/js` 添加导入（驼峰命名，如 `mdiNewIcon`）
//   2. 在下方 `iconMap` 中添加映射（kebab-case，如 `'new-icon': mdiNewIcon`）
// 少任一步都会导致图标找不到：开发环境出现警告，界面可能显示占位文本。
// 修改任何前端图标时，请顺手检查新增的 `mdi-xxx` 是否已完成这两处注册。
// 图标查找: https://pictogrammers.com/library/mdi/
// ============================================================
import {
  mdiSwapVerticalBold,
  mdiPlayCircle,
  mdiDragVertical,
  mdiOpenInNew,
  mdiKey,
  mdiRefresh,
  mdiDotsVertical,
  mdiPencil,
  mdiSpeedometer,
  mdiSpeedometerSlow,
  mdiRocketLaunch,
  mdiPauseCircle,
  mdiStopCircle,
  mdiStopCircleOutline,
  mdiDelete,
  mdiPlaylistRemove,
  mdiArchiveOutline,
  mdiPlus,
  mdiCheckCircle,
  mdiAlertCircle,
  mdiHelpCircle,
  mdiCloseCircle,
  mdiTag,
  mdiInformation,
  mdiCog,
  mdiCogOutline,
  mdiWeb,
  mdiShieldAlert,
  mdiText,
  mdiSwapHorizontal,
  mdiArrowRight,
  mdiArrowLeft,
  mdiArrowRightThin,
  mdiArrowRightBold,
  mdiClose,
  mdiArrowUpBold,
  mdiArrowDownBold,
  mdiArrowDown,
  mdiCheck,
  mdiCheckBold,
  mdiContentCopy,
  mdiAlert,
  mdiAlertOctagon,
  mdiWeatherNight,
  mdiWhiteBalanceSunny,
  mdiLogout,
  mdiServerNetwork,
  mdiHeartPulse,
  mdiChevronDown,
  mdiChevronUp,
  mdiChevronLeft,
  mdiChevronRight,
  mdiTune,
  mdiImageSync,
  mdiRotateRight,
  mdiDice6,
  mdiBackupRestore,
  mdiKeyPlus,
  mdiPin,
  mdiPinOutline,
  mdiKeyChain,
  mdiRobot,
  mdiRobotOutline,
  mdiChatProcessingOutline,
  mdiMessageProcessing,
  mdiDiamondStone,
  mdiApi,
  mdiConsole,
  mdiGoogle,
  mdiLightningBolt,
  mdiFormTextbox,
  mdiIdentifier,
  mdiMenuDown,
  mdiMenuUp,
  mdiCheckboxMarked,
  mdiCheckboxBlankOutline,
  mdiMinusBox,
  mdiCircle,
  mdiRadioboxMarked,
  mdiRadioboxBlank,
  mdiStar,
  mdiStarOutline,
  mdiStarHalf,
  mdiPageFirst,
  mdiPageLast,
  mdiUnfoldMoreHorizontal,
  mdiLoading,
  mdiClockOutline,
  mdiCalendar,
  mdiPaperclip,
  mdiEyedropper,
  mdiEye,
  mdiEyeOff,
  mdiShieldRefresh,
  mdiShieldOffOutline,
  mdiAlertCircleOutline,
  mdiChartLineVariant,
  mdiChartTimelineVariant,
  mdiChartAreaspline,
  mdiChartLine,
  mdiViewDashboardOutline,
  mdiCodeBraces,
  mdiDatabase,
  mdiImageOutline,
  mdiSignature,
  mdiTextBoxCheckOutline,
  mdiArrowCollapseUp,
  mdiArrowCollapseDown,
  mdiHistory,
  mdiFormatListBulleted,
  mdiFormatAlignJustify,
  mdiStethoscope,
  mdiNetwork,
  mdiTagOff,
  mdiShieldLockOutline,
  mdiBrain,
  mdiHeadSnowflake,
  mdiTestTube,
  mdiMinusCircle,
  mdiMagnify,
  mdiSkipNext,
  mdiTimerSand,
  mdiTimerOutline,
  mdiTools,
  mdiProgressClock,
  mdiRoutes,
  mdiPlay,
  mdiRestore,
  mdiKeyRemove,
  mdiKeyAlert,
  mdiCashRemove,
  mdiCashMultiple,
  mdiCash,
  mdiAccountSwitch,
  mdiChatOutline,
  mdiSourceBranch,
  mdiDragHorizontalVariant,
  mdiFilterRemove,
  mdiFilter,
  mdiTimer,
  mdiUpdate,
  mdiCached,
  mdiPlusCircleOutline,
  mdiFastForward,
  mdiImageMultiple,
  mdiShapeOutline,
  mdiVectorPoint,
  mdiVectorPolyline,
} from '@mdi/js'

// 图标名称到 SVG path 的映射 (使用 kebab-case)
const iconMap: Record<string, string> = {
  // Vuetify 内部使用的图标别名
  'complete': mdiCheck,
  'cancel': mdiCloseCircle,
  'close': mdiClose,
  'delete': mdiDelete,
  'clear': mdiClose,
  'success': mdiCheckCircle,
  'info': mdiInformation,
  'warning': mdiAlert,
  'alert-octagon': mdiAlertOctagon,
  'error': mdiAlertCircle,
  'prev': mdiChevronLeft,
  'next': mdiChevronRight,
  'checkboxOn': mdiCheckboxMarked,
  'checkboxOff': mdiCheckboxBlankOutline,
  'checkboxIndeterminate': mdiMinusBox,
  'delimiter': mdiCircle,
  'sortAsc': mdiArrowUpBold,
  'sortDesc': mdiArrowDownBold,
  'expand': mdiChevronDown,
  'menu': mdiMenuDown,
  'subgroup': mdiMenuDown,
  'dropdown': mdiMenuDown,
  'radioOn': mdiRadioboxMarked,
  'radioOff': mdiRadioboxBlank,
  'edit': mdiPencil,
  'ratingEmpty': mdiStarOutline,
  'ratingFull': mdiStar,
  'ratingHalf': mdiStarHalf,
  'loading': mdiLoading,
  'first': mdiPageFirst,
  'last': mdiPageLast,
  'unfold': mdiUnfoldMoreHorizontal,
  'file': mdiPaperclip,
  'plus': mdiPlus,
  'plus-circle-outline': mdiPlusCircleOutline,
  'minus': mdiMinusBox,
  'calendar': mdiCalendar,
  'treeviewCollapse': mdiMenuDown,
  'treeviewExpand': mdiMenuUp,
  'eyeDropper': mdiEyedropper,
  'eye': mdiEye,
  'eye-off': mdiEyeOff,

  // 布局与导航
  'swap-vertical-bold': mdiSwapVerticalBold,
  'drag-vertical': mdiDragVertical,
  'open-in-new': mdiOpenInNew,
  'chevron-down': mdiChevronDown,
  'chevron-up': mdiChevronUp,
  'chevron-left': mdiChevronLeft,
  'chevron-right': mdiChevronRight,
  'dots-vertical': mdiDotsVertical,
  'logout': mdiLogout,
  'archive-outline': mdiArchiveOutline,
  'menu-down': mdiMenuDown,
  'menu-up': mdiMenuUp,

  // 操作按钮
  'pencil': mdiPencil,
  'refresh': mdiRefresh,
  'check': mdiCheck,
  'check-bold': mdiCheckBold,
  'content-copy': mdiContentCopy,
  'arrow-up-bold': mdiArrowUpBold,
  'arrow-down-bold': mdiArrowDownBold,
  'arrow-down': mdiArrowDown,
  'arrow-right': mdiArrowRight,
  'arrow-left': mdiArrowLeft,
  'arrow-right-thin': mdiArrowRightThin,
  'arrow-right-bold': mdiArrowRightBold,
  'swap-horizontal': mdiSwapHorizontal,
  'rotate-right': mdiRotateRight,
  'backup-restore': mdiBackupRestore,

  // 状态图标
  'play-circle': mdiPlayCircle,
  'pause-circle': mdiPauseCircle,
  'stop-circle': mdiStopCircle,
  'stop-circle-outline': mdiStopCircleOutline,
  'check-circle': mdiCheckCircle,
  'alert-circle': mdiAlertCircle,
  'alert-circle-outline': mdiAlertCircleOutline,
  'close-circle': mdiCloseCircle,
  'help-circle': mdiHelpCircle,
  'alert': mdiAlert,

  // 防护盾牌图标
  'shield-refresh': mdiShieldRefresh,
  'shield-off-outline': mdiShieldOffOutline,
  'shield-lock-outline': mdiShieldLockOutline,
  'shape-outline': mdiShapeOutline,

  // 功能图标
  'key': mdiKey,
  'key-plus': mdiKeyPlus,
  'key-chain': mdiKeyChain,
  'speedometer': mdiSpeedometer,
  'speedometer-slow': mdiSpeedometerSlow,
  'rocket-launch': mdiRocketLaunch,
  'playlist-remove': mdiPlaylistRemove,
  'tag': mdiTag,
  'information': mdiInformation,
  'cog': mdiCog,
  'cog-outline': mdiCogOutline,
  'web': mdiWeb,
  'shield-alert': mdiShieldAlert,
  'text': mdiText,
  'tune': mdiTune,
  'image-sync': mdiImageSync,
  'image-outline': mdiImageOutline,
  'image-multiple': mdiImageMultiple,
  'dice-6': mdiDice6,
  'heart-pulse': mdiHeartPulse,
  'server-network': mdiServerNetwork,
  'network': mdiNetwork,
  'pin': mdiPin,
  'pin-outline': mdiPinOutline,
  'lightning-bolt': mdiLightningBolt,
  'fast-forward': mdiFastForward,
  'form-textbox': mdiFormTextbox,
  'identifier': mdiIdentifier,
  'clock-outline': mdiClockOutline,
  'paperclip': mdiPaperclip,
  'eye-dropper': mdiEyedropper,

  // 主题切换
  'weather-night': mdiWeatherNight,
  'white-balance-sunny': mdiWhiteBalanceSunny,

  // 服务类型图标
  'robot': mdiRobot,
  'robot-outline': mdiRobotOutline,
  'chat-processing-outline': mdiChatProcessingOutline,
  'message-processing': mdiMessageProcessing,
  'diamond-stone': mdiDiamondStone,
  'api': mdiApi,
  'console': mdiConsole,
  'google': mdiGoogle,

  // 复选框和单选框
  'checkbox-marked': mdiCheckboxMarked,
  'checkbox-blank-outline': mdiCheckboxBlankOutline,
  'minus-box': mdiMinusBox,
  'radiobox-marked': mdiRadioboxMarked,
  'radiobox-blank': mdiRadioboxBlank,

  // 评分
  'star': mdiStar,
  'star-outline': mdiStarOutline,
  'star-half': mdiStarHalf,

  // 分页
  'page-first': mdiPageFirst,
  'page-last': mdiPageLast,

  // 其他
  'unfold-more-horizontal': mdiUnfoldMoreHorizontal,
  'circle': mdiCircle,

  // 图表与数据
  'chart-timeline-variant': mdiChartTimelineVariant,
  'chart-areaspline': mdiChartAreaspline,
  'chart-line': mdiChartLine,
  'chart-line-variant': mdiChartLineVariant,
  'view-dashboard-outline': mdiViewDashboardOutline,
  'code-braces': mdiCodeBraces,
  'database': mdiDatabase,
  'text-box-check-outline': mdiTextBoxCheckOutline,

  // 签名图标
  'signature': mdiSignature,

  // 置顶/置底操作
  'arrow-collapse-up': mdiArrowCollapseUp,
  'arrow-collapse-down': mdiArrowCollapseDown,

  // 日志与历史
  'history': mdiHistory,
  'format-list-bulleted': mdiFormatListBulleted,
  'format-align-justify': mdiFormatAlignJustify,
  'stethoscope': mdiStethoscope,

  // 计费头
  'tag-off': mdiTagOff,

  // 模型白名单
  'brain': mdiBrain,
  'head-snowflake': mdiHeadSnowflake,

  // 能力测试
  'test-tube': mdiTestTube,
  'minus-circle': mdiMinusCircle,
  'magnify': mdiMagnify,
  'skip-next': mdiSkipNext,
  'timer-sand': mdiTimerSand,
  'timer-outline': mdiTimerOutline,
  'tools': mdiTools,
  'progress-clock': mdiProgressClock,
  'routes': mdiRoutes,
  'play': mdiPlay,
  'restore': mdiRestore,
  'key-remove': mdiKeyRemove,
  'key-alert': mdiKeyAlert,
  'cash-remove': mdiCashRemove,
  'cash-multiple': mdiCashMultiple,
  'cash': mdiCash,

  // 渠道配置
  'account-switch': mdiAccountSwitch,
  'chat-outline': mdiChatOutline,
  'vector-point': mdiVectorPoint,
  'vector-polyline': mdiVectorPolyline,
  'source-branch': mdiSourceBranch,
  'drag-horizontal-variant': mdiDragHorizontalVariant,
  'filter-remove': mdiFilterRemove,
  'filter': mdiFilter,
  'timer': mdiTimer,
  'update': mdiUpdate,
  'cached': mdiCached,
}

// 自定义 SVG iconset - 处理 mdi-xxx 字符串格式
const customSvgIconSet: IconSet = {
  component: (props: IconProps) => {
    // 获取图标名称，去掉 mdi- 前缀
    let iconName = props.icon as string
    if (iconName.startsWith('mdi-')) {
      iconName = iconName.substring(4)
    }

    // 查找对应的 SVG path
    const svgPath = iconMap[iconName]

    if (!svgPath) {
      if (import.meta.env.DEV) {
        console.warn(`[Vuetify Icon] 未找到图标: ${iconName}，请在 vuetify.ts 的 iconMap 中添加映射`)
      }
      return h('span', `[${iconName}]`)
    }

    return h('svg', {
      class: 'v-icon__svg',
      xmlns: 'http://www.w3.org/2000/svg',
      viewBox: '0 0 24 24',
      role: 'img',
      'aria-hidden': 'true',
      style: {
        fontSize: 'inherit',
        width: '1em',
        height: '1em',
      },
    }, [
      h('path', {
        d: svgPath,
        fill: 'currentColor',
      })
    ])
  }
}

// 🎨 精心设计的现代化配色方案
// Light Theme - 清新专业，柔和渐变
const lightTheme: ThemeDefinition = {
  dark: false,
  colors: {
    // 主色调 - 现代蓝紫渐变感
    primary: '#6366F1', // Indigo - 沉稳专业
    secondary: '#8B5CF6', // Violet - 辅助强调
    accent: '#EC4899', // Pink - 活力点缀

    // 语义色彩 - 清晰易辨
    info: '#3B82F6', // Blue
    success: '#10B981', // Emerald
    warning: '#F59E0B', // Amber
    error: '#EF4444', // Red

    // 表面色 - 柔和分层
    background: '#F8FAFC', // Slate-50
    surface: '#FFFFFF', // Pure white cards
    'surface-variant': '#F1F5F9', // Slate-100 for secondary surfaces
    'on-surface': '#1E293B', // Slate-800
    'on-background': '#334155' // Slate-700
  }
}

// Dark Theme - 深邃优雅，护眼舒适
const darkTheme: ThemeDefinition = {
  dark: true,
  colors: {
    // 主色调 - 亮度适中，不刺眼
    primary: '#818CF8', // Indigo-400
    secondary: '#A78BFA', // Violet-400
    accent: '#F472B6', // Pink-400

    // 语义色彩 - 暗色适配
    info: '#60A5FA', // Blue-400
    success: '#34D399', // Emerald-400
    warning: '#FBBF24', // Amber-400
    error: '#F87171', // Red-400

    // 表面色 - 深色层次分明
    background: '#0F172A', // Slate-900
    surface: '#1E293B', // Slate-800
    'surface-variant': '#334155', // Slate-700
    'on-surface': '#F1F5F9', // Slate-100
    'on-background': '#E2E8F0' // Slate-200
  }
}

export default createVuetify({
  components: {
    // 布局
    VApp,
    VMain,
    VContainer,
    VRow,
    VCol,
    VSpacer,
    VAppBar,
    // 卡片与容器
    VCard,
    VCardTitle,
    VCardText,
    VCardActions,
    VDialog,
    VOverlay,
    VExpansionPanels,
    VExpansionPanel,
    VExpansionPanelTitle,
    VExpansionPanelText,
    // 表单
    VForm,
    VTextField,
    VTextarea,
    VSelect,
    VCombobox,
    VSwitch,
    VBtn,
    VBtnToggle,
    // 列表
    VList,
    VListItem,
    VListItemTitle,
    VListItemSubtitle,
    VMenu,
    // 反馈
    VAlert,
    VSnackbar,
    VProgressCircular,
    VTooltip,
    // 数据展示
    VChip,
    VChipGroup,
    VAvatar,
    VIcon,
    VDivider,
    // 表格
    VTable,
    // 标签页
    VTabs,
    VTab,
    // 窗口
    VWindow,
    VWindowItem,
    // 过渡
    VExpandTransition,
  },
  directives: {
    Ripple,
    ClickOutside,
  },
  icons: {
    defaultSet: 'mdi',
    sets: {
      mdi: customSvgIconSet
    }
  },
  theme: {
    defaultTheme: 'light',
    themes: {
      light: lightTheme,
      dark: darkTheme
    }
  }
})
