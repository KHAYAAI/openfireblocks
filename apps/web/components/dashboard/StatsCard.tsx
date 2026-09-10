import { LucideIcon } from 'lucide-react';

interface StatsCardProps {
  title: string;
  value: string | number;
  icon: LucideIcon;
  trend?: string;
  trendPositive?: boolean;
}

export function StatsCard({
  title,
  value,
  icon: Icon,
  trend,
  trendPositive,
}: StatsCardProps) {
  return (
    <div className="bg-slate-900 rounded-lg border border-slate-800 p-6 hover:border-slate-700 transition">
      <div className="flex justify-between items-start mb-4">
        <h3 className="text-sm font-medium text-slate-400">{title}</h3>
        <Icon className="w-5 h-5 text-blue-400" />
      </div>
      <div className="space-y-2">
        <p className="text-2xl font-bold text-white">{value}</p>
        {trend && (
          <p
            className={`text-xs ${
              trendPositive ? 'text-green-400' : 'text-red-400'
            }`}
          >
            {trend}
          </p>
        )}
      </div>
    </div>
  );
}
