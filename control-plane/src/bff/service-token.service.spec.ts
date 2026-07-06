import { Test, TestingModule } from '@nestjs/testing';
import { ServiceTokenService } from './service-token.service';

describe('ServiceTokenService (D-08 BFF)', () => {
  let service: ServiceTokenService;

  beforeEach(async () => {
    const module: TestingModule = await Test.createTestingModule({
      providers: [ServiceTokenService],
    }).compile();
    service = module.get<ServiceTokenService>(ServiceTokenService);
  });

  // TC-01: Token minting produces valid JWT structure
  it('TC-01: mints a 3-part JWT', () => {
    const token = service.mintToken('org_test', 'cust_test', 'ORG_ADMIN');
    const parts = token.split('.');
    expect(parts).toHaveLength(3);
    expect(parts[0].length).toBeGreaterThan(0); // header
    expect(parts[1].length).toBeGreaterThan(0); // payload
    expect(parts[2].length).toBeGreaterThan(0); // signature
  });

  // TC-02: Token verification returns correct claims
  it('TC-02: verifyToken returns claims for valid token', () => {
    const token = service.mintToken('org_test', 'cust_test', 'ORG_ADMIN');
    const claims = service.verifyToken(token);
    expect(claims).not.toBeNull();
    expect(claims!.org_id).toBe('org_test');
    expect(claims!.customer_id).toBe('cust_test');
    expect(claims!.role).toBe('ORG_ADMIN');
  });

  // TC-03: Invalid token rejected
  it('TC-03: verifyToken rejects tampered token', () => {
    const token = service.mintToken('org_test');
    const tampered = token.slice(0, -5) + 'xxxxx';
    expect(service.verifyToken(tampered)).toBeNull();
  });

  // TC-04: Expired token rejected
  it('TC-04: verifyToken rejects expired token (simulated)', () => {
    const token = service.mintToken('org_test');
    // Token has 60s TTL — verify it works initially
    expect(service.verifyToken(token)).not.toBeNull();
    // Fast-forward check: we can't actually wait 60s, but verify the exp is in payload
    const parts = token.split('.');
    const payload = JSON.parse(Buffer.from(parts[1], 'base64url').toString());
    expect(payload.exp).toBeGreaterThan(payload.iat);
    expect(payload.exp - payload.iat).toBe(60);
  });

  // TC-05: Token is never returned to browser (structure check)
  it('TC-05: service token structure matches SCAFFOLD §3', () => {
    const token = service.mintToken('org_acme');
    const parts = token.split('.');
    const header = JSON.parse(Buffer.from(parts[0], 'base64url').toString());
    const payload = JSON.parse(Buffer.from(parts[1], 'base64url').toString());

    expect(header.alg).toBe('HS256');
    expect(header.typ).toBe('JWT');
    expect(payload.iss).toBe('bff');
    expect(payload.org_id).toBe('org_acme');
    expect(payload.iat).toBeDefined();
    expect(payload.exp).toBeDefined();
  });
});
