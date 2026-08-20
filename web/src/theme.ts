export type ThemeMode = 'system' | 'light' | 'dark'

const themeKey = 'nimotsu.theme'

export function getThemeMode(): ThemeMode {
  const stored = localStorage.getItem(themeKey)
  return stored === 'light' || stored === 'dark' ? stored : 'system'
}

export function saveThemeMode(mode: ThemeMode): void {
  if (mode === 'system') {
    localStorage.removeItem(themeKey)
  } else {
    localStorage.setItem(themeKey, mode)
  }
  applyTheme(mode)
}

export function applyTheme(mode: ThemeMode): void {
  if (mode === 'system') {
    document.documentElement.removeAttribute('data-theme')
  } else {
    document.documentElement.dataset.theme = mode
  }

  const lightThemeColor = document.querySelector<HTMLMetaElement>('meta[name="theme-color"][data-theme="light"]')
  const darkThemeColor = document.querySelector<HTMLMetaElement>('meta[name="theme-color"][data-theme="dark"]')
  if (!lightThemeColor || !darkThemeColor) return

  lightThemeColor.media = mode === 'dark' ? 'not all' : mode === 'light' ? 'all' : '(prefers-color-scheme: light)'
  darkThemeColor.media = mode === 'light' ? 'not all' : mode === 'dark' ? 'all' : '(prefers-color-scheme: dark)'
}
