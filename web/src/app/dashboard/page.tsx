'use client';

import { useState, useEffect } from 'react';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer, AreaChart, Area } from 'recharts';

interface DashboardData {
  totals: {
    requests_count: number;
    input_tokens: number;
    output_tokens: number;
    total_tokens: number;
    cost: string;
  };
  models_used: string[];
  days_active: number;
}

interface EndUserRow {
  group_value: string;
  requests_count: number;
  input_tokens: number;
  output_tokens: number;
  cost: string;
}

export default function DashboardPage() {
  const [summary, setSummary] = useState<DashboardData | null>(null);
  const [endUsers, setEndUsers] = useState<EndUserRow[]>([]);
  const [trend, setTrend] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    // Fetch via BFF proxy
    const orgId = '00000000-0000-4000-8000-000000000001';
    Promise.all([
      fetch(`/api/v1/usage/orgs/${orgId}/summary`).then(r => r.json()),
      fetch(`/api/v1/usage/orgs/${orgId}/customers`).then(r => r.json()),
      fetch(`/api/v1/usage/analytics/daily?org_id=${orgId}`).then(r => r.json()),
    ]).then(([s, eu, t]) => {
      setSummary(s);
      setEndUsers(eu.series || []);
      setTrend(t.points || []);
    }).finally(() => setLoading(false));
  }, []);

  if (loading) return <div className="p-8 text-gray-400">Loading...</div>;

  const t = summary?.totals;
  const trendData = trend.map((p: any) => ({ date: p.timestamp?.slice(0,10), tokens: p.total_tokens, cost: parseFloat(p.cost||'0') }));

  return (
    <div className="p-6 space-y-6">
      <h1 className="text-2xl font-bold text-white">Organization Overview</h1>

      {/* Summary Cards */}
      <div className="grid grid-cols-4 gap-4">
        <Card className="bg-gray-800 border-gray-700">
          <CardHeader><CardTitle className="text-sm text-gray-400">Requests</CardTitle></CardHeader>
          <CardContent><p className="text-2xl font-bold text-white">{(t?.requests_count ?? 0).toLocaleString()}</p></CardContent>
        </Card>
        <Card className="bg-gray-800 border-gray-700">
          <CardHeader><CardTitle className="text-sm text-gray-400">Tokens</CardTitle></CardHeader>
          <CardContent><p className="text-2xl font-bold text-white">{(t?.total_tokens ?? 0).toLocaleString()}</p></CardContent>
        </Card>
        <Card className="bg-gray-800 border-gray-700">
          <CardHeader><CardTitle className="text-sm text-gray-400">Cost</CardTitle></CardHeader>
          <CardContent><p className="text-2xl font-bold text-green-400">${parseFloat(t?.cost ?? '0').toFixed(2)}</p></CardContent>
        </Card>
        <Card className="bg-gray-800 border-gray-700">
          <CardHeader><CardTitle className="text-sm text-gray-400">Active Days</CardTitle></CardHeader>
          <CardContent><p className="text-2xl font-bold text-white">{summary?.days_active ?? 0}</p></CardContent>
        </Card>
      </div>

      {/* Usage Trend Chart */}
      <Card className="bg-gray-800 border-gray-700">
        <CardHeader><CardTitle className="text-white">Usage Trend</CardTitle></CardHeader>
        <CardContent>
          <ResponsiveContainer width="100%" height={300}>
            <AreaChart data={trendData}>
              <CartesianGrid strokeDasharray="3 3" stroke="#374151" />
              <XAxis dataKey="date" stroke="#9CA3AF" />
              <YAxis stroke="#9CA3AF" />
              <Tooltip />
              <Area type="monotone" dataKey="tokens" stroke="#8B5CF6" fill="#8B5CF6" fillOpacity={0.2} />
            </AreaChart>
          </ResponsiveContainer>
        </CardContent>
      </Card>

      {/* Per-End-User Table */}
      <Card className="bg-gray-800 border-gray-700">
        <CardHeader><CardTitle className="text-white">Per End-User Usage</CardTitle></CardHeader>
        <CardContent>
          <table className="w-full text-left text-sm text-gray-300">
            <thead><tr className="border-b border-gray-700">
              <th className="py-2">User ID</th><th className="py-2">Requests</th><th className="py-2">Tokens</th><th className="py-2">Cost</th>
            </tr></thead>
            <tbody>
              {endUsers.map((eu, i) => (
                <tr key={i} className="border-b border-gray-700/50">
                  <td className="py-2 font-mono text-xs">{eu.group_value?.slice(0,12)}...</td>
                  <td className="py-2">{eu.requests_count}</td>
                  <td className="py-2">{eu.total_tokens.toLocaleString()}</td>
                  <td className="py-2 text-green-400">${parseFloat(eu.cost).toFixed(4)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </CardContent>
      </Card>
    </div>
  );
}
