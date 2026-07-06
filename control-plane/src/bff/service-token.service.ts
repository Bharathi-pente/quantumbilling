import { Injectable, Logger } from '@nestjs/common';
import { createHmac } from 'crypto';

/**
 * BFF Service Token minting per SCAFFOLD.md §3.
 * Mints HS256 JWTs carrying org_id, customer_id, role claims for 60s TTL.
 */
@Injectable()
export class ServiceTokenService {
  private readonly logger = new Logger(ServiceTokenService.name);
  private readonly secret: string;

  constructor() {
    this.secret = process.env.QB_SERVICE_TOKEN_SECRET ?? 'dev-service-token-secret-change-me';
  }

  /**
   * Mint a 60s HS256 service token with the resolved scope claims.
   */
  mintToken(orgId: string, customerId?: string, role?: string): string {
    const header = Buffer.from(JSON.stringify({ alg: 'HS256', typ: 'JWT' })).toString('base64url');
    const now = Math.floor(Date.now() / 1000);
    const payload = Buffer.from(JSON.stringify({
      iss: 'bff',
      org_id: orgId,
      customer_id: customerId ?? null,
      end_user_id: null,
      role: role ?? 'ORG_ADMIN',
      iat: now,
      exp: now + 60,
    })).toString('base64url');

    const signature = createHmac('sha256', this.secret)
      .update(`${header}.${payload}`)
      .digest('base64url');

    return `${header}.${payload}.${signature}`;
  }

  /** Verify a service token (for analytics-api side). */
  verifyToken(token: string): { org_id: string; customer_id?: string; role?: string } | null {
    const parts = token.split('.');
    if (parts.length !== 3) return null;

    const expectedSig = createHmac('sha256', this.secret)
      .update(`${parts[0]}.${parts[1]}`)
      .digest('base64url');

    if (expectedSig !== parts[2]) return null;

    const payload = JSON.parse(Buffer.from(parts[1], 'base64url').toString());
    if (payload.exp * 1000 < Date.now()) return null;

    return {
      org_id: payload.org_id,
      customer_id: payload.customer_id,
      role: payload.role,
    };
  }
}
