// Editing uses the version the user observed, never a refreshed server version.
export function ticketEditVersion(version: unknown): number {
  if (typeof version !== 'number' || !Number.isSafeInteger(version) || version <= 0) {
    throw new Error('工单版本无效，请刷新工单后重新确认修改');
  }
  return version;
}
