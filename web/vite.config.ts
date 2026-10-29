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
  const apiTarget = env.NAGI_API_TARGET || 'http://127.0.0.1:5080'
  const adminToken = env.NAGI_ADMIN_TOKEN || ''
  const extraAllowedHosts = (env.NAGI_DEV_ALLOWED_HOSTS || '')
    .split(',')
    .map((host) => host.trim())
    .filter(Boolean)

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
      // Dev binds every interface so other devices on the LAN can reach it.
      // 开发时监听所有网卡，便于局域网内其它设备访问。
      host: '0.0.0.0',
      port: 5173,
      strictPort: true,
      // Aside from localhost, only IP-literal hosts (e.g. 192.168.1.10) pass
      // automatically; other hostnames must be listed here. This dev server
      // injects the admin token into proxied requests, so accepting arbitrary
      // Host headers would hand that token to a DNS-rebinding page. Add hosts
      // through NAGI_DEV_ALLOWED_HOSTS only when a tunnel really needs them.
      // 除 localhost 外，Vite 只自动放行 IP 字面量（如 192.168.1.10），
      // 其它主机名必须在此列出。本开发服务器会向代理请求注入管理员令牌，
      // 接受任意 Host 头会把令牌交给 DNS rebinding 页面。
      // 仅在隧道确实需要时通过 NAGI_DEV_ALLOWED_HOSTS 追加主机。
      allowedHosts: ['localhost', ...extraAllowedHosts],
      proxy: {
        '/api': apiProxy,
        '/livez': apiProxy,
        '/readyz': apiProxy,
      },
    },
    build: {
      outDir: 'dist',
      sourcemap: false,
      chunkSizeWarningLimit: 600,
      rollupOptions: {
        output: {
          // Keep the framework, router/query and animation runtimes in stable
          // long-lived chunks instead of one growing entry bundle.
          // 把框架、路由/查询与动画运行时固定在稳定的长期缓存 chunk 中，
          // 避免入口 bundle 持续膨胀。
          manualChunks(id) {
            if (!id.includes('node_modules')) {
              return undefined
            }
            if (id.includes('/react/') || id.includes('/react-dom/') || id.includes('/scheduler/')) {
              return 'react-vendor'
            }
            if (id.includes('@tanstack')) {
              return 'tanstack-vendor'
            }
            if (
              id.includes('/motion/') ||
              id.includes('motion-dom') ||
              id.includes('motion-utils') ||
              id.includes('framer-motion')
            ) {
              return 'motion-vendor'
            }
            return undefined
          },
        },
      },
    },
  }
})
