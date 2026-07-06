import {
  Controller, Get, Post, Patch, Body, Param, Query, UseGuards, Req, ParseUUIDPipe,
} from '@nestjs/common';
import { AuthGuard } from '@nestjs/passport';
import { EndUserService } from './enduser.service';
import { CreateEndUserDto, UpdateEndUserDto } from './dto/enduser.dto';
import { OrgAdminGuard, CustomerGuard, extractOrgId, extractCustomerId } from '../auth/guards';
import { Request } from 'express';

@Controller('end-users')
@UseGuards(AuthGuard('jwt'))
export class EndUserController {
  constructor(private readonly endUserService: EndUserService) {}

  @Post()
  @UseGuards(CustomerGuard)
  create(@Body() dto: CreateEndUserDto, @Req() req: Request) {
    const orgId = extractOrgId(req.user as any);
    const customerId = extractCustomerId(req.user as any) ?? dto['customer_id'];
    const actorId = (req.user as any)?.sub || null;
    return this.endUserService.create(dto, orgId, customerId, actorId);
  }

  @Get()
  @UseGuards(OrgAdminGuard)
  findAll(
    @Req() req: Request,
    @Query('page') page = '1',
    @Query('limit') limit = '20',
    @Query('customer_id') customerId?: string,
  ) {
    const orgId = extractOrgId(req.user as any);
    return this.endUserService.findAll(orgId, customerId, +page, +limit);
  }

  @Get(':endUserId')
  @UseGuards(CustomerGuard)
  findOne(@Param('endUserId', ParseUUIDPipe) id: string) {
    return this.endUserService.findOne(id);
  }

  @Patch(':endUserId')
  @UseGuards(OrgAdminGuard)
  update(@Param('endUserId', ParseUUIDPipe) id: string, @Body() dto: UpdateEndUserDto, @Req() req: Request) {
    const actorId = (req.user as any)?.sub || null;
    return this.endUserService.update(id, dto, actorId);
  }
}
