'use client';

import { useState, useEffect } from 'react';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';

interface EventPoint {
  timestamp: string;
  requests_count: number;
  total_tokens: number;
  cost: string;
}

export default function MyUsageEventsPage() {
  const [daily, setDaily] = useState<EventPoint[]>([]);
  const endUserId = '00000000-0000-4000-8000-000000000003';

  useEffect(() => {
    fetch(`/api/v1/usage/end-users/${endUserId}/daily`).then(r => r.json()).then(d => setDaily(d.points || []));
  }, []);

  return (
    <div className="p-6 space-y-6">
      <h1 className="text-2xl font-bold text-white">My Events</h1>
      <Card className="bg-gray-800 border-gray-700">
        <CardHeader><CardTitle className="text-white">Daily Activity</CardTitle></CardHeader>
        <CardContent>
          <table className="w-full text-left text-sm text-gray-300">
            <thead><tr className="border-b border-gray-700">
              <th className="py-2">Date</th><th className="py-2">Requests</th><th className="py-2">Tokens</th><th className="py-2">Cost</th>
            </tr></thead>
            <tbody>
              {daily.map((d, i) => (
                <tr key={i} className="border-b border-gray-700/50">
                  <td className="py-2">{d.timestamp?.slice(0,10)}</td>
                  <td className="py-2">{d.requests_count}</td>
                  <td className="py-2">{d.total_tokens?.toLocaleString()}</td>
                  <td className="py-2 text-green-400">${parseFloat(d.cost||'0').toFixed(4)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </CardContent>
      </Card>
    </div>
  );
}
