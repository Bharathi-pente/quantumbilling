import { Injectable, NotFoundException, ConflictException, BadRequestException } from '@nestjs/common';
import { randomUUID } from 'crypto';
import { PrismaService } from '../prisma/prisma.service';
import { RedisService } from '../redis/redis.service';
import { CreateOrganizationDto, UpdateOrganizationDto } from './dto/organization.dto';

@Injectable()
export class IdentityService {
  constructor(
    private prisma: PrismaService,
    private redis: RedisService,
  ) {}

  async create(dto: CreateOrganizationDto, actorId: string | null) {
    const org = await this.prisma.organization.create({
      data: {
        name: dto.name,
        billingEmail: dto.billing_email ?? 'placeholder@org.local',
        currency: dto.currency ?? 'USD',
        country: dto.country ?? 'US',
        timezone: dto.timezone ?? 'UTC',
        status: 'ACTIVE',
      } as any,
    });

    await this.redis.setOrgExistence(org.id);

    // Audit
    await this.prisma.auditLog.create({
      data: {
        id: randomUUID(),
        userId: null,
        orgId: org.id,
        action: 'ORGANIZATION_CREATED',
        resourceType: 'organization',
        resourceId: org.id,
        newValue: org as any,
        createdAt: new Date(),
      } as any,
    });

    return org;
  }

  async findAll(page = 1, limit = 20) {
    const skip = (page - 1) * limit;
    const [items, total] = await Promise.all([
      this.prisma.organization.findMany({
        skip,
        take: limit,
        orderBy: { createdAt: 'desc' },
      }),
      this.prisma.organization.count(),
    ]);
    return { items, total, page, limit, has_next_page: skip + limit < total };
  }

  async findOne(id: string) {
    const org = await this.prisma.organization.findUnique({ where: { id } });
    if (!org) throw new NotFoundException({ error: { code: 'NOT_FOUND', message: 'Organization not found' } });
    return org;
  }

  async update(id: string, dto: UpdateOrganizationDto, actorId: string) {
    const org = await this.findOne(id);

    const updated = await this.prisma.organization.update({
      where: { id },
      data: {
        ...(dto.name !== undefined && { name: dto.name }),
        ...(dto.billing_email !== undefined && { billingEmail: dto.billing_email }),
        ...(dto.currency !== undefined && { currency: dto.currency }),
        ...(dto.country !== undefined && { country: dto.country }),
        ...(dto.industry !== undefined && { industry: dto.industry }),
        ...(dto.timezone !== undefined && { timezone: dto.timezone }),
        // Reactivate if currently SUSPENDED
        ...(org.status === 'SUSPENDED' && { status: 'ACTIVE' as const, suspendedAt: null }),
      } as any,
    });

    // Audit
    await this.prisma.auditLog.create({
      data: {
        id: randomUUID(),
        userId: null,
        orgId: id,
        action: 'ORGANIZATION_UPDATED',
        resourceType: 'organization',
        resourceId: id,
        oldValue: org as any,
        newValue: updated as any,
        createdAt: new Date(),
      } as any,
    });

    return updated;
  }

  async suspend(id: string, force: boolean, actorId: string) {
    const org = await this.findOne(id);
    if (org.status === 'SUSPENDED') return org; // idempotent
    if (org.status === 'DELETED') throw new BadRequestException({
      error: { code: 'ORG_DELETED', message: 'Cannot suspend a deleted organization' },
    });

    // Check for active subscriptions
    if (!force) {
      const activeSub = await this.prisma.subscription.findFirst({
        where: { orgId: id, status: { in: ['active', 'trialing', 'past_due'] } },
      });
      if (activeSub) throw new ConflictException({
        error: { code: 'SUBSCRIPTION_ACTIVE', message: 'Organization has active subscriptions. Use ?force=true to bypass.' },
      });
    }

    const updated = await this.prisma.organization.update({
      where: { id },
      data: { status: 'SUSPENDED', suspendedAt: new Date() },
    });

    // Redis: remove existence key (suspended orgs can't ingest)
    await this.redis.delOrgExistence(id);

    // Audit
    await this.prisma.auditLog.create({
      data: {
        id: randomUUID(),
        userId: null,
        orgId: id,
        action: 'ORGANIZATION_SUSPENDED',
        resourceType: 'organization',
        resourceId: id,
        oldValue: org as any,
        newValue: updated as any,
        createdAt: new Date(),
      } as any,
    });

    return updated;
  }
}
