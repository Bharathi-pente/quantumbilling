import { Injectable, Logger } from '@nestjs/common';
import { ServiceTokenService } from './service-token.service';
import { RedisService } from '../redis/redis.service';
import { HttpService } from '@nestjs/axios';
import { firstValueFrom } from 'rxjs';

/**
 * BFF Usage Proxy — forwards analytics requests to the Go analytics-api
 * with a minted service token. Validates Keycloak JWT → resolves scope →
 * mints 60s HS256 token → forwards with trusted headers.
 */
@Injectable()
export class BffProxyService {
  private readonly logger = new Logger(BffProxyService.name);
  private readonly analyticsUrl: string;

  constructor(
    private readonly tokenService: ServiceTokenService,
    private readonly redis: RedisService,
    private readonly http: HttpService,
  ) {
    this.analyticsUrl = process.env.ANALYTICS_API_URL ?? 'http://localhost:8014';
  }

  /**
   * Proxy an analytics request to the Go analytics-api.
   * @param path - the analytics path (e.g. /v1/orgs/{id}/summary)
   * @param user - the Keycloak-authenticated user (from JWT guard)
   * @param query - optional query params
   */
  async proxyRequest(path: string, user: any, query: Record<string, string> = {}): Promise<any> {
    const orgId = user.org_id ?? query['org_id'] ?? '';
    const customerId = user.customer_id ?? query['customer_id'];
    const role = user.realm_access?.roles?.[0] ?? 'ORG_ADMIN';

    // Mint service token
    const token = this.tokenService.mintToken(orgId, customerId, role);

    // Check Redis cache for platform analytics (60s TTL per story)
    const cacheKey = `bff:analytics:${path}:${JSON.stringify(query)}`;
    if (path.includes('/analytics/')) {
      try {
        const cached = await this.redis.client.get(cacheKey);
        if (cached) {
          this.logger.debug(`cache hit: ${cacheKey}`);
          return JSON.parse(cached);
        }
      } catch {}
    }

    // Forward to analytics-api
    try {
      const response = await firstValueFrom(
        this.http.get(`${this.analyticsUrl}${path}`, {
          headers: {
            'X-QB-Service-Token': token,
            'X-QB-Org-Id': orgId,
            'X-QB-Customer-Id': customerId ?? '',
            'X-QB-Role': role,
          },
          params: query,
          timeout: 10000,
        }),
      );

      const data = response.data;

      // Cache platform analytics responses (60s)
      if (path.includes('/analytics/')) {
        try {
          await this.redis.client.set(cacheKey, JSON.stringify(data), 'EX', 60);
        } catch {}
      }

      return data;
    } catch (err) {
      this.logger.error(`analytics proxy failed: path=${path}`, err);
      throw err;
    }
  }
}
