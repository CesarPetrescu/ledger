/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_LEDGER_SERVER: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
