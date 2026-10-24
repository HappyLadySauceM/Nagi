import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vitest/config'

export default defineConfig({
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  test: {
    environment: 'node',
    // Testing Library unmounts rendered trees between tests only when the
    // global hooks exist; without it a later query could match stale DOM.
    // 只有存在全局钩子时 Testing Library 才会在用例间卸载已渲染树，
    // 否则后续查询可能命中上一次用例残留的 DOM。
    globals: true,
    include: ['src/**/*.test.{ts,tsx}'],
    // Component tests opt in through the .tsx extension; adapter tests keep the
    // faster node environment.
    // 组件测试通过 .tsx 后缀选择 jsdom；适配器测试继续使用更快的 node 环境。
    environmentMatchGlobs: [['src/**/*.test.tsx', 'jsdom']],
    setupFiles: ['./src/test/setup.ts'],
  },
})
