'use client';

import { useState, useEffect } from 'react';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';

export default function MyUsagePage() {
  const [data, setData] = useState<any>(null);
  const endUserId = '00000000-0000-4000-8000-000000000003';

  useEffect(() => {
    fetch(`/api/v1/usage/end-users/${endUserId}/summary`).then(r => r.json()).then(setData);
  }, []);

  const t = data?.totals;
  return (
    <div className="p-6 space-y-6">
      <h1 className="text-2xl font-bold text-white">My Usage</h1>
      <div className="grid grid-cols-3 gap-4">
        <Card className="bg-gray-800 border-gray-700">
          <CardHeader><CardTitle className="text-sm text-gray-400">Requests</CardTitle></CardHeader>
          <CardContent><p className="text-2xl font-bold text-white">{t?.requests_count ?? 0}</p></CardContent>
        </Card>
        <Card className="bg-gray-800 border-gray-700">
          <CardHeader><CardTitle className="text-sm text-gray-400">Tokens</CardTitle></CardHeader>
          <CardContent><p className="text-2xl font-bold text-white">{t?.total_tokens ?? 0}</p></CardContent>
        </Card>
        <Card className="bg-gray-800 border-gray-700">
          <CardHeader><CardTitle className="text-sm text-gray-400">Cost</CardTitle></CardHeader>
          <CardContent><p className="text-2xl font-bold text-green-400">${parseFloat(t?.cost ?? '0').toFixed(4)}</p></CardContent>
        </Card>
      </div>
    </div>
  );
}
