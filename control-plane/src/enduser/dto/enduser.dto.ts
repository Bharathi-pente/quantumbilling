import { IsString, IsEmail, IsOptional, MaxLength, IsObject } from 'class-validator';

export class CreateEndUserDto {
  @IsString()
  @MaxLength(255)
  name: string;

  @IsEmail()
  email: string;

  @IsString()
  @IsOptional()
  external_user_id?: string;

  @IsObject()
  @IsOptional()
  metadata?: Record<string, any>;
}

export class UpdateEndUserDto {
  @IsString()
  @MaxLength(255)
  @IsOptional()
  name?: string;

  @IsEmail()
  @IsOptional()
  email?: string;

  @IsString()
  @IsOptional()
  external_user_id?: string;

  @IsString()
  @IsOptional()
  status?: 'active' | 'suspended' | 'canceled';
}
