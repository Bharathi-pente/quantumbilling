import { IsString, IsEmail, IsOptional, IsIn, MaxLength, IsUUID } from 'class-validator';

export class CreateCustomerDto {
  @IsString()
  @MaxLength(255)
  name: string;

  @IsEmail()
  email: string;

  @IsUUID()
  @IsOptional()
  product_id?: string;
}

export class UpdateCustomerDto {
  @IsString()
  @MaxLength(255)
  @IsOptional()
  name?: string;

  @IsEmail()
  @IsOptional()
  email?: string;

  @IsString()
  @IsIn(['ACTIVE', 'SUSPENDED', 'CHURNED'])
  @IsOptional()
  status?: string;
}
