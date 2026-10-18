import { useState } from 'react';
import Link from 'next/link';
import { Menu, X, LogOut, BarChart3, Users, Zap, Key, Shield, Settings } from 'lucide-react';

export function AdminLayout({ children }: { children: React.ReactNode }) {
  const [sidebarOpen, setSidebarOpen] = useState(true);

  const navItems = [
    {
      label: 'Dashboard',
      href: '/dashboard',
      icon: BarChart3,
    },
    {
      label: 'Customers',
      href: '/customers',
      icon: Users,
    },
    {
      label: 'Transactions',
      href: '/transactions',
      icon: Zap,
    },
    {
      label: 'API Keys',
      href: '/api-keys',
      icon: Key,
    },
    {
      label: 'Policies',
      href: '/policies',
      icon: Shield,
    },
    {
      label: 'Settings',
      href: '/settings',
      icon: Settings,
    },
  ];

  return (
    <div className="flex h-screen bg-slate-950 text-white">
      {/* Sidebar */}
      <div
        className={`${
          sidebarOpen ? 'w-64' : 'w-20'
        } bg-slate-900 border-r border-slate-800 transition-all duration-300 flex flex-col`}
      >
        {/* Logo */}
        <div className="h-16 border-b border-slate-800 flex items-center justify-between px-4">
          {sidebarOpen && (
            <h1 className="text-lg font-bold text-white">OpenFireblocks</h1>
          )}
          <button
            onClick={() => setSidebarOpen(!sidebarOpen)}
            className="p-1 hover:bg-slate-800 rounded"
          >
            {sidebarOpen ? (
              <X className="w-5 h-5" />
            ) : (
              <Menu className="w-5 h-5" />
            )}
          </button>
        </div>

        {/* Navigation */}
        <nav className="flex-1 overflow-y-auto py-6 space-y-2 px-2">
          {navItems.map((item) => {
            const Icon = item.icon;
            return (
              <Link key={item.href} href={item.href}>
                <a className="flex items-center gap-3 px-3 py-2 rounded-lg hover:bg-slate-800 transition-colors group">
                  <Icon className="w-5 h-5 text-slate-400 group-hover:text-blue-400" />
                  {sidebarOpen && (
                    <span className="text-sm font-medium text-slate-300 group-hover:text-white">
                      {item.label}
                    </span>
                  )}
                </a>
              </Link>
            );
          })}
        </nav>

        {/* Footer */}
        <div className="h-16 border-t border-slate-800 flex items-center px-4">
          <button className="flex items-center gap-3 w-full hover:bg-slate-800 rounded px-2 py-2 transition-colors">
            <LogOut className="w-5 h-5 text-slate-400" />
            {sidebarOpen && (
              <span className="text-sm font-medium text-slate-300">Logout</span>
            )}
          </button>
        </div>
      </div>

      {/* Main Content */}
      <div className="flex-1 flex flex-col overflow-hidden">
        {/* Top Bar */}
        <div className="h-16 bg-slate-900 border-b border-slate-800 px-8 flex items-center justify-between">
          <div>
            <h2 className="text-sm text-slate-400">Admin Panel</h2>
          </div>
          <div className="flex items-center gap-4">
            <button className="text-sm text-slate-300 hover:text-white">Help</button>
            <div className="w-10 h-10 rounded-full bg-blue-600 flex items-center justify-center">
              <span className="text-sm font-bold">A</span>
            </div>
          </div>
        </div>

        {/* Content Area */}
        <div className="flex-1 overflow-auto bg-slate-950 p-8">
          {children}
        </div>
      </div>
    </div>
  );
}
