/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_API_BASE_URL: string
  readonly VITE_AUTH_AUDIENCE: string
  readonly VITE_AUTH_CLIENT_ID: string
  readonly VITE_AUTH_DOMAIN: string
  readonly VITE_DEMO_MODE: string
  readonly VITE_E2E_MODE?: string
  readonly VITE_MCP_SERVER_URL?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
