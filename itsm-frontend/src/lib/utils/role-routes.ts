/**
 * Role-based default routes.
 * Determines landing page after login based on user role.
 */
export function getDefaultRoute(role: string): string {
  switch (role) {
    case 'end_user':
      return '/service-catalog';
    case 'agent':
    case 'technician':
      return '/tickets';
    case 'manager':
    case 'security':
      return '/approvals';
    case 'admin':
    case 'super_admin':
      return '/dashboard';
    default:
      return '/dashboard';
  }
}
