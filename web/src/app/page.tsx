import type { Metadata } from 'next';

export const metadata: Metadata = {
  title: 'QuantumBilling',
  description: 'Hybrid subscription + usage billing platform',
};

export default function Home() {
  return (
    <main className="flex min-h-screen items-center justify-center bg-gray-900">
      <h1 className="text-4xl font-bold text-white">QuantumBilling</h1>
    </main>
  );
}
