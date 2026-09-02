import { getDefaultHomePath } from '../persona-config';

describe('getDefaultHomePath', () => {
  it.each(['admin', 'super_admin', 'sysadmin'])(
    'routes %s to the canonical admin overview',
    role => {
      expect(getDefaultHomePath(role)).toBe('/admin/overview');
    }
  );

  it('fails safe to the end-user portal for an unknown role', () => {
    expect(getDefaultHomePath('unknown-role')).toBe('/portal');
  });
});
