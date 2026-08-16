import { ref } from 'vue'

type Translator = (key: string) => string

export function useEditChannelSectionNav(t: Translator) {
  const activeSection = ref('basic')
  const sectionRefs = ref<Record<string, HTMLElement | null>>({})
  let scrollRoot: Element | null = null
  let scrollHandler: (() => void) | null = null

  const sections = [
    { id: 'basic', label: t('channelEditor.nav.basic') },
    { id: 'auth', label: t('channelEditor.nav.auth') },
    { id: 'custom', label: t('channelEditor.nav.custom') },
  ]

  function detachScrollListener() {
    if (scrollRoot && scrollHandler) {
      scrollRoot.removeEventListener('scroll', scrollHandler)
    }
    scrollRoot = null
    scrollHandler = null
  }

  function scrollToSection(id: string) {
    activeSection.value = id
    const el = sectionRefs.value[id]
    if (el) {
      el.scrollIntoView({ behavior: 'smooth', block: 'start' })
    }
  }

  function setSectionRef(id: string, el: HTMLElement | null) {
    sectionRefs.value[id] = el
  }

  function updateActiveSectionFromScroll() {
    if (!scrollRoot) return
    const rootTop = scrollRoot.getBoundingClientRect().top
    let current = sections[0]?.id || 'basic'

    for (const s of sections) {
      const el = sectionRefs.value[s.id]
      if (!el) continue
      const top = el.getBoundingClientRect().top - rootTop
      if (top <= 120) {
        current = s.id
      } else {
        break
      }
    }

    activeSection.value = current
  }

  function attachScrollListener(selector = '.content-area') {
    detachScrollListener()
    scrollRoot = document.querySelector(selector)
    if (!scrollRoot) return
    scrollHandler = () => updateActiveSectionFromScroll()
    scrollRoot.addEventListener('scroll', scrollHandler, { passive: true })
    updateActiveSectionFromScroll()
  }

  return {
    activeSection,
    sectionRefs,
    sections,
    scrollToSection,
    setSectionRef,
    updateActiveSectionFromScroll,
    attachScrollListener,
    detachScrollListener,
  }
}
