import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import react from '@vitejs/plugin-react'
import { defineConfig, loadEnv, type ProxyOptions } from 'vite'

const pkg = JSON.parse(
  readFileSync(new URL('./package.json', import.meta.url), 'utf-8'),
) as { version: string }

// The admin token is injected by the dev server only. It is never exposed as a
// VITE_* variable, so it cannot reach the browser bundle.
// 管理员令牌仅由开发服务器注入，不使用 VITE_ 前缀，因此不会进入浏览器产物。
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const apiTarget = env.QQBOT_API_TARGET || 'http://127.0.0.1:5080'
  const adminToken = env.QQBOT_ADMIN_TOKEN || ''

  const apiProxy: ProxyOptions = {
    target: apiTarget,
    changeOrigin: true,
    configure: (proxyServer) => {
      if (!adminToken) {
        return
      }
      proxyServer.on('proxyReq', (proxyRequest) => {
        proxyRequest.setHeader('Authorization', `Bearer ${adminToken}`)
      })
    },
  }

  return {
    plugins: [react()],
    define: {
      __APP_VERSION__: JSON.stringify(pkg.version),
    },
    resolve: {
      alias: {
        '@': fileURLToPath(new URL('./src', import.meta.url)),
      },
    },
    server: {
      host: '127.0.0.1',
      port: 5173,
      strictPort: true,
      allowedHosts: true,
      proxy: {
        '/api': apiProxy,
        '/livez': apiProxy,
        '/readyz': apiProxy,
      },
    },
    build: {
      outDir: 'dist',
      sourcemap: false,
      chunkSizeWarningLimit: 900,
    },
  }
})
