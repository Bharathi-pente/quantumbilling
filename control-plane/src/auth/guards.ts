import { Injectable, CanActivate, ExecutionContext, ForbiddenException } from '@nestjs/common';
import { JwtUser } from './jwt.strategy';

/**
 * ORG_ADMIN guard — allows ORG_ADMIN and SUPER_ADMIN.
 * ORG_ADMIN scoped to their org_id in the JWT claim.
 */
@Injectable()
export class OrgAdminGuard implements CanActivate {
  canActivate(context: ExecutionContext): boolean {
    const { user } = context.switchToHttp().getRequest();
    const roles: string[] = user?.realm_access?.roles ?? [];
    if (roles.includes('SUPER_ADMIN') || roles.includes('ORG_ADMIN')) return true;
    throw new ForbiddenException({
      error: { code: 'FORBIDDEN', message: 'Requires ORG_ADMIN or SUPER_ADMIN role' },
    });
  }
}

/**
 * SUPER_ADMIN guard — only SUPER_ADMIN passes.
 */
@Injectable()
export class SuperAdminGuard implements CanActivate {
  canActivate(context: ExecutionContext): boolean {
    const { user } = context.switchToHttp().getRequest();
    const roles: string[] = user?.realm_access?.roles ?? [];
    if (roles.includes('SUPER_ADMIN')) return true;
    throw new ForbiddenException({
      error: { code: 'FORBIDDEN', message: 'Requires SUPER_ADMIN role' },
    });
  }
}

/**
 * CUSTOMER guard — allows CUSTOMER, ORG_ADMIN, SUPER_ADMIN.
 * CUSTOMER scoped to their customer_id.
 */
@Injectable()
export class CustomerGuard implements CanActivate {
  canActivate(context: ExecutionContext): boolean {
    const { user } = context.switchToHttp().getRequest();
    const roles: string[] = user?.realm_access?.roles ?? [];
    if (roles.includes('SUPER_ADMIN') || roles.includes('ORG_ADMIN') || roles.includes('CUSTOMER')) return true;
    throw new ForbiddenException({
      error: { code: 'FORBIDDEN', message: 'Requires CUSTOMER, ORG_ADMIN, or SUPER_ADMIN role' },
    });
  }
}

/** Extract org_id from JWT user — throws if missing */
export function extractOrgId(user: JwtUser): string {
  const orgId = user.org_id;
  if (!orgId) throw new ForbiddenException({
    error: { code: 'FORBIDDEN', message: 'No org_id in token' },
  });
  return orgId;
}

/** Extract customer_id from JWT user */
export function extractCustomerId(user: JwtUser): string | undefined {
  return user.customer_id;
}
