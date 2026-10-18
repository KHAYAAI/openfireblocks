'use client';

import { useState } from 'react';
import Link from 'next/link';
import { Menu, X, Key, GitSignIcon, Settings, LogOut, CreditCard } from 'lucide-react';

interface DashboardLayoutProps {
  children: React.ReactNode;
}

export function DashboardLayout({ children }: DashboardLayoutProps) {
  const [sidebarOpen, setSidebarOpen] = useState(false);

  const navItems = [
    { href: '/dashboard', label: 'Dashboard', icon: GitSignIcon },
    { href: '/keys', label: 'Keys', icon: Key },
    { href: '/signings', label: 'Signings', icon: GitSignIcon },
    { href: '/billing', label: 'Billing', icon: CreditCard },
    { href: '/settings', label: 'Settings', icon: Settings },
  ];

  return (
    <div className="flex h-screen bg-slate-950">
      {/* Sidebar */}
      <div
        className={`fixed inset-y-0 left-0 bg-slate-900 border-r border-slate-800 w-64 p-4 space-y-8 transform transition-transform lg:translate-x-0 lg:relative lg:w-auto ${
          sidebarOpen ? 'translate-x-0' : '-translate-x-full'
        }`}
      >
        <div className="flex items-center gap-2">
          <div className="w-8 h-8 bg-blue-600 rounded flex items-center justify-center">
            <Key className="w-4 h-4 text-white" />
          </div>
          <span className="text-lg font-bold text-white">OpenFireblocks</span>
        </div>

        <nav className="space-y-2">
          {navItems.map((item) => (
            <Link
              key={item.href}
              href={item.href}
              className="flex items-center gap-3 px-4 py-2 text-sm text-slate-300 hover:text-white hover:bg-slate-800 rounded-lg transition"
            >
              <item.icon className="w-4 h-4" />
              {item.label}
            </Link>
          ))}
        </nav>

        <div className="border-t border-slate-800 pt-4">
          <button className="flex items-center gap-3 px-4 py-2 text-sm text-slate-300 hover:text-white w-full rounded-lg transition">
            <LogOut className="w-4 h-4" />
            Logout
          </button>
        </div>
      </div>

      {/* Main Content */}
      <div className="flex-1 flex flex-col overflow-hidden">
        {/* Top Bar */}
        <div className="bg-slate-900 border-b border-slate-800 px-6 py-4 flex items-center justify-between">
          <button
            onClick={() => setSidebarOpen(!sidebarOpen)}
            className="lg:hidden p-2 text-slate-400 hover:text-white"
          >
            {sidebarOpen ? (
              <X className="w-6 h-6" />
            ) : (
              <Menu className="w-6 h-6" />
            )}
          </button>
          <div className="flex-1" />
          <div className="flex items-center gap-4">
            <div className="w-8 h-8 bg-blue-600 rounded-full" />
          </div>
        </div>

        {/* Page Content */}
        <div className="flex-1 overflow-auto">
          <div className="p-8 max-w-7xl mx-auto w-full">
            {children}
          </div>
        </div>
      </div>
    </div>
  );
}
