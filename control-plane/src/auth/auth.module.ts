import { Module } from '@nestjs/common';
import { PassportModule } from '@nestjs/passport';
import { JwtStrategy } from './jwt.strategy';
import { OrgAdminGuard, SuperAdminGuard, CustomerGuard } from './guards';
import { RolesGuard } from './roles.guard';

@Module({
  imports: [PassportModule.register({ defaultStrategy: 'jwt' })],
  providers: [JwtStrategy, RolesGuard, OrgAdminGuard, SuperAdminGuard, CustomerGuard],
  exports: [PassportModule, RolesGuard, OrgAdminGuard, SuperAdminGuard, CustomerGuard],
})
export class AuthModule {}
