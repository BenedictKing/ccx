/// <reference types="vite/client" />
/// <reference types="vuetify" />

declare module '*.vue' {
  import type { DefineComponent } from 'vue'

  const component: DefineComponent<object, object, any> // eslint-disable-line @typescript-eslint/no-explicit-any
  export default component
}

declare module 'vuetify/styles' {}

import type { SupportedLocale } from './i18n/messages'

export {}

declare global {
  var __APP_UI_LANGUAGE__: string

  interface Window {
    __CCX_RUNTIME_CONFIG__?: {
      uiLanguage?: string
    }
  }

  var __CCX_I18N__: {
    global: {
      locale: { value: SupportedLocale }
      t: {
        (_key: string, _params?: Record<string, string | number>): string
      }
    }
  } | undefined
}
