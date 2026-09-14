import { toTargetType, getAttachmentPermissions } from '../toTargetType';

describe('toTargetType', () => {
  it.each([
    ['incident', 'incident'],
    ['problem', 'problem'],
    ['change_request', 'change'],
    ['generic', 'ticket'],
    ['service_request_item', 'ticket'],
    ['catalog_task', 'ticket'],
  ] as const)('maps recordClass %s to TargetType %s', (recordClass, expected) => {
    expect(toTargetType(recordClass)).toBe(expected);
  });
});


describe('Requested Item attachment permission boundary', () => {
  const check = (grants: string[]) => getAttachmentPermissions('service_request_item', p => grants.includes(p));
  it('allows coarse requester upload with service request grants', () => {
    expect(check(['service_request:read', 'service_request:write'])).toEqual({ canRead: true, canUpload: true, canDelete: false });
  });
  it('allows coarse helpdesk upload with provision but leaves row scope to the backend', () => {
    expect(check(['service_request:read', 'service_request:provision'])).toEqual({ canRead: true, canUpload: true, canDelete: false });
  });
  it('does not substitute generic ticket permissions for Requested Item permissions', () => {
    expect(check(['ticket:read', 'ticket:create', 'ticket:delete'])).toEqual({ canRead: false, canUpload: false, canDelete: false });
  });
  it('keeps read-only and deletion separate from collaboration', () => {
    expect(check(['service_request:read'])).toEqual({ canRead: true, canUpload: false, canDelete: false });
    expect(check(['service_request:read', 'service_request:delete'])).toEqual({ canRead: true, canUpload: false, canDelete: true });
  });
});
