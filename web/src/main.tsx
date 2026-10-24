import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { RouterProvider } from '@tanstack/react-router'
import { Providers } from './app/providers'
import { router } from './app/router'
import './styles/globals.css'

const container = document.getElementById('root')
if (!container) {
  throw new Error('缺少 #root 挂载节点')
}

createRoot(container).render(
  <StrictMode>
    <Providers>
      <RouterProvider router={router} />
    </Providers>
  </StrictMode>,
)
