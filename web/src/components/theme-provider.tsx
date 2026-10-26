import * as React from 'react'
import { ThemeContext, type Theme } from './theme-context'

const THEME_STORAGE_KEY = 'nagi-theme'

function readInitialTheme(): Theme {
  if (typeof document === 'undefined') {
    return 'dark'
  }
  // index.html applies the stored theme before paint, so the DOM is the source
  // of truth for the first render and no flash is introduced.
  return document.documentElement.classList.contains('dark') ? 'dark' : 'light'
}

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const [theme, setThemeState] = React.useState<Theme>(readInitialTheme)

  const setTheme = React.useCallback((next: Theme) => {
    const root = document.documentElement
    root.classList.toggle('dark', next === 'dark')
    root.style.colorScheme = next
    window.localStorage.setItem(THEME_STORAGE_KEY, next)
    setThemeState(next)
  }, [])

  const value = React.useMemo(() => ({ theme, setTheme }), [theme, setTheme])

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
}
