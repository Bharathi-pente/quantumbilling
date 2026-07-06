import { Injectable, NotFoundException } from '@nestjs/common';
import { PrismaService } from '../prisma/prisma.service';
import { RedisService } from '../redis/redis.service';
import { CreateEndUserDto, UpdateEndUserDto } from './dto/enduser.dto';
import { randomUUID } from 'crypto';

@Injectable()
export class EndUserService {
  constructor(
    private prisma: PrismaService,
    private redis: RedisService,
  ) {}

  async create(dto: CreateEndUserDto, orgId: string, customerId: string, actorId: string) {
    const id = randomUUID();
    const endUser = await this.prisma.endUser.create({
      data: {
        id,
        customerId: customerId,
        orgId: orgId,
        externalUserId: dto.external_user_id ?? '',
        name: dto.name,
        email: dto.email,
        status: 'active',
        metadata: dto.metadata ?? null,
        createdAt: new Date(),
      } as any,
    });

    // Redis write-through
    await this.redis.setEndUserExistence(orgId, id);

    // Audit
    await this.prisma.auditLog.create({
      data: {
        id: randomUUID(),
        userId: null,
        orgId: orgId,
        action: 'END_USER_CREATED',
        resourceType: 'end_user',
        resourceId: id,
        newValue: endUser as any,
        createdAt: new Date(),
      } as any,
    });

    return endUser;
  }

  async findAll(orgId: string, customerId?: string, page = 1, limit = 20) {
    const skip = (page - 1) * limit;
    const where: any = { orgId: orgId };
    if (customerId) where.customerId = customerId;

    const [items, total] = await Promise.all([
      this.prisma.endUser.findMany({ where, skip, take: limit, orderBy: { createdAt: 'desc' } }),
      this.prisma.endUser.count({ where }),
    ]);
    return { items, total, page, limit, has_next_page: skip + limit < total };
  }

  async findOne(id: string) {
    const endUser = await this.prisma.endUser.findUnique({ where: { id } });
    if (!endUser) throw new NotFoundException({ error: { code: 'NOT_FOUND', message: 'End user not found' } });
    return endUser;
  }

  async update(id: string, dto: UpdateEndUserDto, actorId: string) {
    const endUser = await this.findOne(id);

    const updated = await this.prisma.endUser.update({
      where: { id },
      data: {
        ...(dto.name !== undefined && { name: dto.name }),
        ...(dto.email !== undefined && { email: dto.email }),
        ...(dto.external_user_id !== undefined && { externalUserId: dto.external_user_id }),
        ...(dto.status !== undefined && { status: dto.status }),
      },
    });

    // Redis write-through
    if (dto.status === 'canceled' || dto.status === 'suspended') {
      await this.redis.delEndUserExistence(endUser.orgId, id);
    } else if (dto.status === 'active' && endUser.status !== 'active') {
      await this.redis.setEndUserExistence(endUser.orgId, id);
    }

    await this.prisma.auditLog.create({
      data: {
        id: randomUUID(),
        userId: null,
        orgId: endUser.orgId,
        action: 'END_USER_UPDATED',
        resourceType: 'end_user',
        resourceId: id,
        oldValue: endUser as any,
        newValue: updated as any,
        createdAt: new Date(),
      } as any,
    });

    return updated;
  }
}
