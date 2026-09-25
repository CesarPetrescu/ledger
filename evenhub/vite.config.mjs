import { defineConfig } from 'vite'

export default defineConfig(() => ({
  define: {
    'import.meta.env.VITE_LEDGER_SERVER': JSON.stringify(process.env.LEDGER_SERVER ?? ''),
  },
}))
