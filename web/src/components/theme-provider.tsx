import * as React from 'react'

export type Theme = 'dark' | 'light'

const THEME_STORAGE_KEY = 'qqbot-theme'

interface ThemeContextValue {
  theme: Theme
  setTheme: (theme: Theme) => void
}

const ThemeContext = React.createContext<ThemeContextValue | null>(null)

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

export function useTheme(): ThemeContextValue {
  const context = React.useContext(ThemeContext)
  if (!context) {
    throw new Error('useTheme 必须在 ThemeProvider 内使用')
  }
  return context
}
