import { Module } from '@nestjs/common';
import { HttpModule } from '@nestjs/axios';
import { BffProxyController } from './bff-proxy.controller';
import { BffProxyService } from './bff-proxy.service';
import { ServiceTokenService } from './service-token.service';

@Module({
  imports: [HttpModule],
  controllers: [BffProxyController],
  providers: [BffProxyService, ServiceTokenService],
  exports: [ServiceTokenService],
})
export class BffModule {}
