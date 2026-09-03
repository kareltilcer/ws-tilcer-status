import { QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import { Toaster } from 'sonner'
import { queryClient } from '@/app/queryClient'
import { ThemeProvider, useTheme } from '@/theme/theme'
import { AuthProvider } from '@/app/auth'
import { AppShell } from '@/components/AppShell'
import { Board } from '@/routes/Board'
import { SiteDetail } from '@/routes/SiteDetail'
import { AddSite } from '@/routes/AddSite'
import { CrashGroup } from '@/routes/CrashGroup'
import { Inbox } from '@/routes/Inbox'
import { ReportDetail } from '@/routes/ReportDetail'
import { paths } from '@/app/routes'

function AppToaster() {
  const { theme } = useTheme()
  return <Toaster position="bottom-center" theme={theme} />
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <AuthProvider>
          <BrowserRouter>
            <Routes>
              <Route element={<AppShell />}>
                <Route path={paths.board} element={<Board />} />
                <Route path={paths.addSite} element={<AddSite />} />
                <Route path={paths.sitePattern} element={<SiteDetail />} />
                <Route path={paths.groupPattern} element={<CrashGroup />} />
                <Route path={paths.reports} element={<Inbox />} />
                <Route path={paths.reportPattern} element={<ReportDetail />} />
                <Route path="*" element={<Navigate to={paths.board} replace />} />
              </Route>
            </Routes>
          </BrowserRouter>
          <AppToaster />
        </AuthProvider>
      </ThemeProvider>
    </QueryClientProvider>
  )
}
