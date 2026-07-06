import { Controller, Get, Param, Query, Req, UseGuards } from '@nestjs/common';
import { AuthGuard } from '@nestjs/passport';
import { BffProxyService } from './bff-proxy.service';
import { OrgAdminGuard } from '../auth/guards';
import { Request } from 'express';

/**
 * BFF Usage Proxy Controller — forwards analytics requests to the Go engine.
 * Role-gated: ORG_ADMIN for org-scoped, CustomerGuard for customer-scoped.
 */
@Controller('usage')
@UseGuards(AuthGuard('jwt'), OrgAdminGuard)
export class BffProxyController {
  constructor(private readonly proxy: BffProxyService) {}

  private async forward(path: string, req: Request, query: Record<string, string> = {}) {
    const user = req.user as any;
    return this.proxy.proxyRequest(path, user, { ...req.query as any, ...query });
  }

  // Org endpoints
  @Get('orgs/:orgId/summary')
  orgSummary(@Param('orgId') orgId: string, @Req() req: Request) {
    return this.forward(`/v1/orgs/${orgId}/summary`, req, { org_id: orgId });
  }

  @Get('orgs/:orgId/customers')
  orgCustomers(@Param('orgId') orgId: string, @Req() req: Request) {
    return this.forward(`/v1/orgs/${orgId}/customers/usage`, req, { org_id: orgId });
  }

  @Get('orgs/:orgId/models')
  orgModels(@Param('orgId') orgId: string, @Req() req: Request) {
    return this.forward(`/v1/orgs/${orgId}/models/usage`, req, { org_id: orgId });
  }

  @Get('orgs/:orgId/services')
  orgServices(@Param('orgId') orgId: string, @Req() req: Request) {
    return this.forward(`/v1/orgs/${orgId}/services/usage`, req, { org_id: orgId });
  }

  @Get('orgs/:orgId/cost')
  orgCost(@Param('orgId') orgId: string, @Req() req: Request) {
    return this.forward(`/v1/orgs/${orgId}/cost`, req, { org_id: orgId });
  }

  // Trend endpoints
  @Get('analytics/hourly')
  hourly(@Req() req: Request) {
    return this.forward('/v1/analytics/hourly', req);
  }

  @Get('analytics/daily')
  daily(@Req() req: Request) {
    return this.forward('/v1/analytics/daily', req);
  }

  @Get('analytics/weekly')
  weekly(@Req() req: Request) {
    return this.forward('/v1/analytics/weekly', req);
  }

  @Get('analytics/monthly')
  monthly(@Req() req: Request) {
    return this.forward('/v1/analytics/monthly', req);
  }

  // Platform endpoints
  @Get('analytics/models')
  models(@Req() req: Request) {
    return this.forward('/v1/analytics/models/usage', req);
  }

  @Get('analytics/services')
  services(@Req() req: Request) {
    return this.forward('/v1/analytics/services/usage', req);
  }

  @Get('analytics/cost')
  cost(@Req() req: Request) {
    return this.forward('/v1/analytics/cost', req);
  }
}
