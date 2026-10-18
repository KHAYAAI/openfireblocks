import type { Metadata } from 'next';
import './globals.css';

export const metadata: Metadata = {
  title: 'OpenFireblocks - Enterprise Crypto Key Management',
  description: 'Threshold signing and institutional key management platform',
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en">
      <body className="bg-slate-950 text-slate-100">
        {children}
      </body>
    </html>
  );
}
