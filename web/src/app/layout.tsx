import type { ReactNode } from 'react'

import '@/app/globals.css'
import 'katex/dist/katex.min.css'
import { I18nProvider } from '@/features/i18n/i18n-provider'
import { ThemeScript } from '@/features/theme/theme-script'
import { ThemeProvider } from '@/features/theme/theme-provider'

export const metadata = {
  title: 'Lumi - ACP Gateway Chat',
}

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="zh" suppressHydrationWarning>
      <body>
        <ThemeScript />
        <ThemeProvider>
          <I18nProvider>{children}</I18nProvider>
        </ThemeProvider>
      </body>
    </html>
  )
}
