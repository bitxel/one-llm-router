import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createRouter, RouterProvider } from '@tanstack/react-router'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { Toaster } from 'sonner'

import './styles/globals.css'
import { routeTree } from './router'

// Singleton QueryClient — shared across all routes.
//
// Defaults balance responsiveness (staleTime low enough to feel live)
// with network chattiness (retry:1 so transient upstream blips don't
// spam the operator's screen).
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5_000,
      refetchOnWindowFocus: false,
      retry: 1,
    },
    mutations: { retry: 0 },
  },
})

const router = createRouter({
  routeTree,
  defaultPreload: 'intent',
  context: { queryClient },
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}

const rootEl = document.getElementById('root')
if (!rootEl) {
  throw new Error('root element missing — index.html must ship <div id="root">')
}

createRoot(rootEl).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
      <Toaster position="top-right" theme="system" richColors />
    </QueryClientProvider>
  </StrictMode>,
)
