import { IsString, IsEmail, IsOptional, IsIn, MaxLength } from 'class-validator';

export class CreateOrganizationDto {
  @IsString()
  @MaxLength(255)
  name: string;

  @IsEmail()
  @IsOptional()
  billing_email?: string;

  @IsString()
  @IsIn(['USD', 'EUR', 'GBP', 'INR', 'JPY', 'AUD', 'CAD'])
  @IsOptional()
  currency?: string;

  @IsString()
  @MaxLength(3)
  @IsOptional()
  country?: string;

  @IsString()
  @IsOptional()
  industry?: string;

  @IsString()
  @IsOptional()
  timezone?: string;
}

export class UpdateOrganizationDto {
  @IsString()
  @MaxLength(255)
  @IsOptional()
  name?: string;

  @IsEmail()
  @IsOptional()
  billing_email?: string;

  @IsString()
  @IsIn(['USD', 'EUR', 'GBP', 'INR', 'JPY', 'AUD', 'CAD'])
  @IsOptional()
  currency?: string;

  @IsString()
  @MaxLength(3)
  @IsOptional()
  country?: string;

  @IsString()
  @IsOptional()
  industry?: string;

  @IsString()
  @IsOptional()
  timezone?: string;
}
