import { Injectable, OnModuleDestroy } from '@nestjs/common';
import Redis from 'ioredis';

@Injectable()
export class RedisService implements OnModuleDestroy {
  readonly client: Redis;

  constructor() {
    const url = process.env.REDIS_URL ?? 'redis://localhost:6379/0';
    this.client = new Redis(url, {
      maxRetriesPerRequest: 1,
      enableReadyCheck: false,
      lazyConnect: true,
      retryStrategy: () => null, // don't retry in tests
    });
    // Connect in background — don't block startup
    this.client.connect().catch(() => {});
  }

  async onModuleDestroy() {
    await this.client.quit();
  }

  /** Write-through: set org existence key with 1h TTL */
  async setOrgExistence(orgId: string) {
    try { await this.client.set(`org:${orgId}`, '1', 'EX', 3600); } catch {}
  }

  /** Write-through: delete org existence key */
  async delOrgExistence(orgId: string) {
    try { await this.client.del(`org:${orgId}`); } catch {}
  }

  /** Write-through: set end-user existence key with 1h TTL */
  async setEndUserExistence(orgId: string, endUserId: string) {
    try { await this.client.set(`org:${orgId}:enduser:${endUserId}`, '1', 'EX', 3600); } catch {}
  }

  /** Write-through: delete end-user existence key */
  async delEndUserExistence(orgId: string, endUserId: string) {
    try { await this.client.del(`org:${orgId}:enduser:${endUserId}`); } catch {}
  }
}
