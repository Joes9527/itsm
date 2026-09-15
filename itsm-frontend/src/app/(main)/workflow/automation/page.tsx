import { redirect } from 'next/navigation';

export default function LegacyAutomationPage() {
  redirect('/admin/tickets/automation-rules');
}
