import { Test, TestingModule } from '@nestjs/testing';
import { INestApplication, ValidationPipe } from '@nestjs/common';
import * as request from 'supertest';
import { AppModule } from '../src/app.module';
import { PrismaService } from '../src/prisma/prisma.service';
import { AuthGuard } from '@nestjs/passport';
import { SuperAdminGuard, OrgAdminGuard, CustomerGuard } from '../src/auth/guards';
import { RolesGuard } from '../src/auth/roles.guard';

function jwtUser(roles: string[], orgId?: string, customerId?: string) {
  return {
    sub: 'test-user-id',
    email: 'test@quantumbilling.local',
    preferred_username: 'testuser',
    realm_access: { roles },
    org_id: orgId,
    customer_id: customerId,
  };
}

const mockJwtGuard = {
  canActivate: (ctx: any) => {
    const req = ctx.switchToHttp().getRequest();
    const raw = req.headers['x-mock-user'] as string;
    if (!raw) return false;
    req.user = JSON.parse(raw);
    return true;
  },
};

const mockSuperAdminGuard = {
  canActivate: (ctx: any) => {
    const roles: string[] = ctx.switchToHttp().getRequest().user?.realm_access?.roles ?? [];
    return roles.includes('SUPER_ADMIN');
  },
};

const mockOrgAdminGuard = {
  canActivate: (ctx: any) => {
    const roles: string[] = ctx.switchToHttp().getRequest().user?.realm_access?.roles ?? [];
    return roles.includes('SUPER_ADMIN') || roles.includes('ORG_ADMIN');
  },
};

const mockCustomerGuard = {
  canActivate: (ctx: any) => {
    const roles: string[] = ctx.switchToHttp().getRequest().user?.realm_access?.roles ?? [];
    return roles.includes('SUPER_ADMIN') || roles.includes('ORG_ADMIN') || roles.includes('CUSTOMER');
  },
};

describe('D-01: Control-Plane Foundation (e2e)', () => {
  let app: INestApplication;
  let prisma: PrismaService;
  let orgId: string;
  let customerId: string;

  beforeAll(async () => {
    const moduleFixture: TestingModule = await Test.createTestingModule({
      imports: [AppModule],
    })
      .overrideGuard(AuthGuard('jwt')).useValue(mockJwtGuard)
      .overrideGuard(SuperAdminGuard).useValue(mockSuperAdminGuard)
      .overrideGuard(OrgAdminGuard).useValue(mockOrgAdminGuard)
      .overrideGuard(CustomerGuard).useValue(mockCustomerGuard)
      .overrideGuard(RolesGuard).useValue({ canActivate: () => true })
      .compile();

    app = moduleFixture.createNestApplication();
    app.useGlobalPipes(new ValidationPipe({ whitelist: true, forbidNonWhitelisted: true, transform: true, errorHttpStatusCode: 422 }));
    app.setGlobalPrefix('api/v1');
    await app.init();
    prisma = app.get(PrismaService);
  });

  afterAll(async () => {
    if (customerId) await prisma.endUser.deleteMany({ where: { customerId } }).catch(() => {});
    if (customerId) await prisma.customer.deleteMany({ where: { id: customerId } }).catch(() => {});
    if (orgId) await prisma.organization.deleteMany({ where: { id: orgId } }).catch(() => {});
    await app.close();
  });

  function hdr(roles: string[], oId?: string, cId?: string) {
    return { 'x-mock-user': JSON.stringify(jwtUser(roles, oId, cId)) };
  }

  it('TC-01: SUPER_ADMIN creates an organization', async () => {
    const res = await request(app.getHttpServer())
      .post('/api/v1/orgs').set(hdr(['SUPER_ADMIN']))
      .send({ name: 'Test Org', billing_email: 'billing@test.com', currency: 'USD', country: 'US', timezone: 'UTC' });
    if (res.status !== 201) console.log('TC-01 FAIL body:', JSON.stringify(res.body));
    expect(res.status).toBe(201);
    expect(res.body.name).toBe('Test Org');
    orgId = res.body.id;
  });

  it('TC-02: Missing name returns 422', async () => {
    const res = await request(app.getHttpServer())
      .post('/api/v1/orgs').set(hdr(['SUPER_ADMIN']))
      .send({ billing_email: 'b@t.com' });
    expect(res.status).toBe(422);
  });

  it('TC-03: ORG_ADMIN cannot create org', async () => {
    const res = await request(app.getHttpServer())
      .post('/api/v1/orgs').set(hdr(['ORG_ADMIN'], orgId))
      .send({ name: 'Fail' });
    expect(res.status).toBe(403);
  });

  it('TC-04: SUPER_ADMIN lists orgs', async () => {
    const res = await request(app.getHttpServer())
      .get('/api/v1/orgs').set(hdr(['SUPER_ADMIN']));
    expect(res.status).toBe(200);
    expect(res.body.total).toBeGreaterThanOrEqual(1);
  });

  it('TC-05: SUPER_ADMIN updates org', async () => {
    const res = await request(app.getHttpServer())
      .patch(`/api/v1/orgs/${orgId}`).set(hdr(['SUPER_ADMIN']))
      .send({ name: 'Updated Org' });
    if (res.status !== 200) console.log('TC-05 body:', JSON.stringify(res.body));
    expect(res.status).toBe(200);
    expect(res.body.name).toBe('Updated Org');
  });

  it('TC-06: SUPER_ADMIN suspends org', async () => {
    const res = await request(app.getHttpServer())
      .delete(`/api/v1/orgs/${orgId}`).set(hdr(['SUPER_ADMIN']));
    expect(res.status).toBe(200);
    expect(res.body.status).toBe('SUSPENDED');
  });

  it('TC-07: Patch reactivates suspended org', async () => {
    const res = await request(app.getHttpServer())
      .patch(`/api/v1/orgs/${orgId}`).set(hdr(['SUPER_ADMIN']))
      .send({ name: 'Reactivated' });
    expect(res.status).toBe(200);
    expect(res.body.status).toBe('ACTIVE');
  });

  it('TC-08: ORG_ADMIN creates a customer', async () => {
    const res = await request(app.getHttpServer())
      .post('/api/v1/customers').set(hdr(['ORG_ADMIN'], orgId))
      .send({ name: 'Test Cust', email: 'cust@test.com' });
    expect(res.status).toBe(201);
    customerId = res.body.id;
  });

  it('TC-09: Customer ACTIVE→SUSPENDED', async () => {
    const res = await request(app.getHttpServer())
      .patch(`/api/v1/customers/${customerId}`).set(hdr(['ORG_ADMIN'], orgId))
      .send({ status: 'SUSPENDED' });
    expect(res.status).toBe(200);
    expect(res.body.status).toBe('SUSPENDED');
  });

  it('TC-10: CHURNED is terminal', async () => {
    await request(app.getHttpServer())
      .patch(`/api/v1/customers/${customerId}`).set(hdr(['ORG_ADMIN'], orgId))
      .send({ status: 'CHURNED' });
    const res = await request(app.getHttpServer())
      .patch(`/api/v1/customers/${customerId}`).set(hdr(['ORG_ADMIN'], orgId))
      .send({ status: 'ACTIVE' });
    expect(res.status).toBe(409);
    expect(res.body.error.code).toBe('INVALID_STATUS_TRANSITION');
  });

  it('TC-11: Create end user', async () => {
    const cr = await request(app.getHttpServer())
      .post('/api/v1/customers').set(hdr(['ORG_ADMIN'], orgId))
      .send({ name: 'EU Cust', email: 'eu@test.com' });
    const custId = cr.body.id;

    const res = await request(app.getHttpServer())
      .post('/api/v1/end-users').set(hdr(['ORG_ADMIN'], orgId, custId))
      .send({ name: 'Alice', email: 'alice@test.com' });
    expect(res.status).toBe(201);
    expect(res.body.name).toBe('Alice');
  });

  it('TC-12: CUSTOMER cannot mutate orgs', async () => {
    const res = await request(app.getHttpServer())
      .patch(`/api/v1/orgs/${orgId}`).set(hdr(['CUSTOMER'], orgId))
      .send({ name: 'Hack' });
    expect(res.status).toBe(403);
  });

  it('TC-13: Audit log has entries', async () => {
    const count = await prisma.auditLog.count();
    expect(count).toBeGreaterThan(0);
  });
});
