import type { Metadata } from 'next';
import './globals.css';

export const metadata: Metadata = {
  title: 'OpenFireblocks Wallet',
  description: 'Institutional digital asset custody and signing platform',
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
