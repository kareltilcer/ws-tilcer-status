import { QueryClient } from '@tanstack/react-query'

// Auth failures (401/403) are not retried — the fetch wrapper routes 401 to login.
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: true,
      retry: (failureCount, error) => {
        const status = (error as { status?: number } | null)?.status
        if (status === 401 || status === 403) return false
        return failureCount < 2
      },
      staleTime: 10_000,
    },
  },
})
