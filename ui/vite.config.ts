import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  base: '/ui/',
  build: {
    outDir: '../internal/ui/dist',
    emptyOutDir: true,
  },
})
