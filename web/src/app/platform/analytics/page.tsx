'use client';

import { useState, useEffect } from 'react';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';

export default function PlatformAnalyticsPage() {
  const [models, setModels] = useState<any[]>([]);
  const [cost, setCost] = useState('0');

  useEffect(() => {
    Promise.all([
      fetch('/api/v1/usage/analytics/models').then(r => r.json()),
      fetch('/api/v1/usage/analytics/cost').then(r => r.json()),
    ]).then(([m, c]) => {
      setModels(m.series || []);
      setCost(c.total_cost || '0');
    });
  }, []);

  return (
    <div className="p-6 space-y-6">
      <h1 className="text-2xl font-bold text-white">Platform Analytics</h1>
      <Card className="bg-gray-800 border-gray-700">
        <CardHeader><CardTitle className="text-white">Total Platform Cost: ${parseFloat(cost).toFixed(2)}</CardTitle></CardHeader>
      </Card>
      <Card className="bg-gray-800 border-gray-700">
        <CardHeader><CardTitle className="text-white">Model Usage</CardTitle></CardHeader>
        <CardContent>
          <table className="w-full text-left text-sm text-gray-300">
            <thead><tr className="border-b border-gray-700">
              <th className="py-2">Model</th><th className="py-2">Requests</th><th className="py-2">Tokens</th><th className="py-2">Cost</th>
            </tr></thead>
            <tbody>
              {models.map((m, i) => (
                <tr key={i} className="border-b border-gray-700/50">
                  <td className="py-2">{m.group_value}</td>
                  <td className="py-2">{m.requests_count}</td>
                  <td className="py-2">{m.total_tokens?.toLocaleString()}</td>
                  <td className="py-2 text-green-400">${parseFloat(m.cost||'0').toFixed(4)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </CardContent>
      </Card>
    </div>
  );
}
