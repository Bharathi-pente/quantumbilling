import { Module } from '@nestjs/common';
import { APP_FILTER } from '@nestjs/core';
import { HealthController } from './health.controller';
import { PrismaModule } from './prisma/prisma.module';
import { RedisModule } from './redis/redis.module';
import { AuthModule } from './auth/auth.module';
import { IdentityModule } from './identity/identity.module';
import { CustomerModule } from './customer/customer.module';
import { EndUserModule } from './enduser/enduser.module';
import { BffModule } from './bff/bff.module';
import { ErrorEnvelopeFilter } from './error-envelope.filter';

@Module({
  imports: [PrismaModule, RedisModule, AuthModule, IdentityModule, CustomerModule, EndUserModule, BffModule],
  controllers: [HealthController],
  providers: [
    { provide: APP_FILTER, useClass: ErrorEnvelopeFilter },
  ],
})
export class AppModule {}
