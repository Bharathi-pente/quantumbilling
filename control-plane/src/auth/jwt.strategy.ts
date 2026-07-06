import { Injectable } from '@nestjs/common';
import { PassportStrategy } from '@nestjs/passport';
import { ExtractJwt, Strategy } from 'passport-jwt';
import * as jwt from 'jsonwebtoken';

export interface JwtUser {
  sub: string;
  email?: string;
  preferred_username?: string;
  realm_access?: { roles: string[] };
  org_id?: string;
  customer_id?: string;
}

@Injectable()
export class JwtStrategy extends PassportStrategy(Strategy) {
  constructor() {
    super({
      jwtFromRequest: ExtractJwt.fromAuthHeaderAsBearerToken(),
      ignoreExpiration: false,
      // A-01 F4: DEV-ONLY — validates HS256 tokens signed with the dev client secret.
      // PRODUCTION TODO: Replace with Keycloak JWKS endpoint (RS256):
      //   secretOrKeyProvider: passportJwtSecret({
      //     cache: true, rateLimit: true, jwksUri: `${keycloakUrl}/realms/quantumbilling/protocol/openid-connect/certs`,
      //   }),
      secretOrKeyProvider: (_request, _rawJwtToken, done) => {
        const secret = process.env.KEYCLOAK_CLIENT_SECRET ?? 'dev-bff-client-secret';
        done(null, secret);
      },
    });
  }

  async validate(payload: any): Promise<JwtUser> {
    return {
      sub: payload.sub,
      email: payload.email,
      preferred_username: payload.preferred_username,
      realm_access: payload.realm_access,
      org_id: payload.org_id,
      customer_id: payload.customer_id,
    };
  }
}
