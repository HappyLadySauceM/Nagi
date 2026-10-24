import * as React from 'react'

export type Theme = 'dark' | 'light'

export interface ThemeContextValue {
  theme: Theme
  setTheme: (theme: Theme) => void
}

/**
 * Shared context so the provider module only exports a component, which keeps
 * Fast Refresh working.
 * 共享上下文，使 provider 模块只导出组件，从而保持 Fast Refresh 可用。
 */
export const ThemeContext = React.createContext<ThemeContextValue | null>(null)

/** Reads the active theme; throws when used outside ThemeProvider. */
export function useTheme(): ThemeContextValue {
  const context = React.useContext(ThemeContext)
  if (!context) {
    throw new Error('useTheme 必须在 ThemeProvider 内使用')
  }
  return context
}
