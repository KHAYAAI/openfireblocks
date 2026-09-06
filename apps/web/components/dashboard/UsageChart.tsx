'use client';

import { PieChart, Pie, Cell, ResponsiveContainer, Legend, Tooltip } from 'recharts';

const data = [
  { name: 'Used', value: 240 },
  { name: 'Available', value: 760 },
];

const COLORS = ['#3b82f6', '#1e293b'];

export function UsageChart() {
  return (
    <div className="w-full h-40">
      <ResponsiveContainer width="100%" height="100%">
        <PieChart>
          <Pie
            data={data}
            cx="50%"
            cy="50%"
            innerRadius={45}
            outerRadius={70}
            paddingAngle={2}
            dataKey="value"
          >
            {data.map((entry, index) => (
              <Cell key={`cell-${index}`} fill={COLORS[index]} />
            ))}
          </Pie>
          <Tooltip formatter={(value) => `${value} requests`} />
        </PieChart>
      </ResponsiveContainer>
      <div className="mt-4 space-y-2">
        <div className="flex justify-between text-xs">
          <span className="text-slate-400">Signings Used</span>
          <span className="text-white font-medium">240 / 1000</span>
        </div>
        <div className="w-full bg-slate-800 rounded-full h-2">
          <div
            className="bg-blue-600 h-2 rounded-full"
            style={{ width: '24%' }}
          />
        </div>
      </div>
    </div>
  );
}
