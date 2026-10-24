import type { ReactNode } from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import { Toaster } from 'sonner'
import { ThemeProvider } from '../components/theme-provider'
import { useTheme } from '../components/theme-context'
import { TooltipProvider } from '../components/ui/tooltip'
import { queryClient } from './query-client'

function AppToaster() {
  const { theme } = useTheme()
  return (
    <Toaster
      theme={theme}
      position="bottom-right"
      closeButton
      toastOptions={{
        classNames: {
          toast: 'border border-border bg-card text-card-foreground shadow-float',
          description: 'text-muted-foreground',
        },
      }}
    />
  )
}

export function Providers({ children }: { children: ReactNode }) {
  return (
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <TooltipProvider delayDuration={180}>
          {children}
          <AppToaster />
        </TooltipProvider>
      </ThemeProvider>
    </QueryClientProvider>
  )
}
