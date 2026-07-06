import {
  Controller, Get, Post, Patch, Delete, Body, Param, Query, UseGuards, Req, ParseUUIDPipe,
} from '@nestjs/common';
import { AuthGuard } from '@nestjs/passport';
import { IdentityService } from './identity.service';
import { CreateOrganizationDto, UpdateOrganizationDto } from './dto/organization.dto';
import { SuperAdminGuard, OrgAdminGuard } from '../auth/guards';
import { Request } from 'express';

@Controller('orgs')
@UseGuards(AuthGuard('jwt'))
export class IdentityController {
  constructor(private readonly identityService: IdentityService) {}

  @Post()
  @UseGuards(SuperAdminGuard)
  create(@Body() dto: CreateOrganizationDto, @Req() req: Request) {
    const actorId = (req.user as any)?.sub ?? 'unknown';
    return this.identityService.create(dto, actorId);
  }

  @Get()
  @UseGuards(SuperAdminGuard)
  findAll(@Query('page') page = '1', @Query('limit') limit = '20') {
    return this.identityService.findAll(+page, +limit);
  }

  @Get(':orgId')
  @UseGuards(OrgAdminGuard)
  findOne(@Param('orgId', ParseUUIDPipe) id: string) {
    return this.identityService.findOne(id);
  }

  @Patch(':orgId')
  @UseGuards(SuperAdminGuard)
  update(
    @Param('orgId', ParseUUIDPipe) id: string,
    @Body() dto: UpdateOrganizationDto,
    @Req() req: Request,
  ) {
    const actorId = (req.user as any)?.sub ?? 'unknown';
    return this.identityService.update(id, dto, actorId);
  }

  @Delete(':orgId')
  @UseGuards(SuperAdminGuard)
  suspend(
    @Param('orgId', ParseUUIDPipe) id: string,
    @Query('force') force = 'false',
    @Req() req: Request,
  ) {
    const actorId = (req.user as any)?.sub ?? 'unknown';
    return this.identityService.suspend(id, force === 'true', actorId);
  }
}
