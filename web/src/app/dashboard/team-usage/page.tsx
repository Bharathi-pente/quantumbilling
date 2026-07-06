'use client';

import { useState, useEffect } from 'react';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer } from 'recharts';

export default function TeamUsagePage() {
  const [customers, setCustomers] = useState<any[]>([]);
  const orgId = '00000000-0000-4000-8000-000000000001';

  useEffect(() => {
    fetch(`/api/v1/usage/orgs/${orgId}/customers`).then(r => r.json()).then(d => setCustomers(d.series || []));
  }, []);

  const chartData = customers.map((c: any) => ({ name: c.group_value?.slice(0,8), tokens: c.total_tokens, cost: parseFloat(c.cost||'0') }));

  return (
    <div className="p-6 space-y-6">
      <h1 className="text-2xl font-bold text-white">Team Usage</h1>
      <Card className="bg-gray-800 border-gray-700">
        <CardHeader><CardTitle className="text-white">Usage by Customer</CardTitle></CardHeader>
        <CardContent>
          <ResponsiveContainer width="100%" height={300}>
            <BarChart data={chartData}>
              <CartesianGrid strokeDasharray="3 3" stroke="#374151" />
              <XAxis dataKey="name" stroke="#9CA3AF" />
              <YAxis stroke="#9CA3AF" />
              <Tooltip />
              <Bar dataKey="tokens" fill="#8B5CF6" />
            </BarChart>
          </ResponsiveContainer>
        </CardContent>
      </Card>
    </div>
  );
}
