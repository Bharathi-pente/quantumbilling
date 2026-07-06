import {
  Controller, Get, Post, Patch, Body, Param, Query, UseGuards, Req, ParseUUIDPipe,
} from '@nestjs/common';
import { AuthGuard } from '@nestjs/passport';
import { CustomerService } from './customer.service';
import { CreateCustomerDto, UpdateCustomerDto } from './dto/customer.dto';
import { OrgAdminGuard, CustomerGuard, extractOrgId } from '../auth/guards';
import { Request } from 'express';

@Controller('customers')
@UseGuards(AuthGuard('jwt'))
export class CustomerController {
  constructor(private readonly customerService: CustomerService) {}

  @Post()
  @UseGuards(OrgAdminGuard)
  create(@Body() dto: CreateCustomerDto, @Req() req: Request) {
    const orgId = extractOrgId(req.user as any);
    const actorId = (req.user as any)?.sub || null;
    return this.customerService.create(dto, orgId, actorId);
  }

  @Get()
  @UseGuards(OrgAdminGuard)
  findAll(@Req() req: Request, @Query('page') page = '1', @Query('limit') limit = '20', @Query('status') status?: string) {
    const orgId = extractOrgId(req.user as any);
    return this.customerService.findAll(orgId, +page, +limit, status);
  }

  @Get(':customerId')
  @UseGuards(CustomerGuard)
  findOne(@Param('customerId', ParseUUIDPipe) id: string) {
    return this.customerService.findOne(id);
  }

  @Patch(':customerId')
  @UseGuards(OrgAdminGuard)
  update(@Param('customerId', ParseUUIDPipe) id: string, @Body() dto: UpdateCustomerDto, @Req() req: Request) {
    const actorId = (req.user as any)?.sub || null;
    return this.customerService.update(id, dto, actorId);
  }
}
