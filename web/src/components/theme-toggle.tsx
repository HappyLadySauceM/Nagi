import { Moon, Sun } from 'lucide-react'
import { Button } from './ui/button'
import {
  DropdownMenu,
  DropdownMenuCheck,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from './ui/dropdown-menu'
import { useTheme } from './theme-context'

export function ThemeToggle() {
  const { theme, setTheme } = useTheme()

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" aria-label="切换界面主题">
          <Sun className="size-4 dark:hidden" aria-hidden />
          <Moon className="hidden size-4 dark:block" aria-hidden />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent>
        <DropdownMenuLabel>界面主题</DropdownMenuLabel>
        <DropdownMenuItem onSelect={() => setTheme('dark')}>
          <DropdownMenuCheck active={theme === 'dark'} />
          深色
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => setTheme('light')}>
          <DropdownMenuCheck active={theme === 'light'} />
          浅色
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
