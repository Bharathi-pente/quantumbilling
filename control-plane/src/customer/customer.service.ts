import { Injectable, NotFoundException, ConflictException } from '@nestjs/common';
import { PrismaService } from '../prisma/prisma.service';
import { CreateCustomerDto, UpdateCustomerDto } from './dto/customer.dto';
import { randomUUID } from 'crypto';

@Injectable()
export class CustomerService {
  constructor(private prisma: PrismaService) {}

  async create(dto: CreateCustomerDto, orgId: string, actorId: string) {
    const id = randomUUID();
    const customer = await this.prisma.customer.create({
      data: {
        id,
        orgId: orgId,
        name: dto.name,
        email: dto.email,
        billingEmail: dto.email,
        status: 'ACTIVE',
        createdAt: new Date(),
      } as any,
    });

    await this.prisma.auditLog.create({
      data: {
        id: randomUUID(),
        userId: null,
        orgId: orgId,
        action: 'CUSTOMER_CREATED',
        resourceType: 'customer',
        resourceId: id,
        newValue: customer as any,
        createdAt: new Date(),
      } as any,
    });

    return customer;
  }

  async findAll(orgId: string, page = 1, limit = 20, status?: string) {
    const skip = (page - 1) * limit;
    const where: any = { orgId: orgId };
    if (status) where.status = status;

    const [items, total] = await Promise.all([
      this.prisma.customer.findMany({ where, skip, take: limit, orderBy: { createdAt: 'desc' } }),
      this.prisma.customer.count({ where }),
    ]);
    return { items, total, page, limit, has_next_page: skip + limit < total };
  }

  async findOne(id: string) {
    const customer = await this.prisma.customer.findUnique({ where: { id } });
    if (!customer) throw new NotFoundException({ error: { code: 'NOT_FOUND', message: 'Customer not found' } });
    return customer;
  }

  async update(id: string, dto: UpdateCustomerDto, actorId: string) {
    const customer = await this.findOne(id);
    this.validateStatusTransition(customer.status, dto.status);

    const updated = await this.prisma.customer.update({
      where: { id },
      data: {
        ...(dto.name !== undefined && { name: dto.name }),
        ...(dto.email !== undefined && { email: dto.email }),
        ...(dto.status !== undefined && { status: dto.status as any }),
      },
    });

    await this.prisma.auditLog.create({
      data: {
        id: randomUUID(),
        userId: null,
        orgId: customer.orgId,
        action: dto.status ? `CUSTOMER_${dto.status}` : 'CUSTOMER_UPDATED',
        resourceType: 'customer',
        resourceId: id,
        oldValue: customer as any,
        newValue: updated as any,
        createdAt: new Date(),
      } as any,
    });

    return updated;
  }

  private validateStatusTransition(current: string, next?: string) {
    if (!next || current === next) return;
    const transitions: Record<string, string[]> = {
      ACTIVE: ['SUSPENDED'],
      SUSPENDED: ['ACTIVE', 'CHURNED'],
      CHURNED: [], // terminal
    };

    if (!transitions[current]?.includes(next)) {
      throw new ConflictException({
        error: {
          code: 'INVALID_STATUS_TRANSITION',
          message: `Cannot transition from ${current} to ${next}`,
        },
      });
    }
  }
}
