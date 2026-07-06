import { Module } from '@nestjs/common';
import { EndUserController } from './enduser.controller';
import { EndUserService } from './enduser.service';

@Module({
  controllers: [EndUserController],
  providers: [EndUserService],
  exports: [EndUserService],
})
export class EndUserModule {}
