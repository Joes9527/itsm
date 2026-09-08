/** Authoritative ITSM finite access policy. Keys are original FieldDefinition values. */
export interface CatalogAccessPolicy {
  id: number;
  version: number;
  provider: 'graph';
  externalSystem: string;
  groupId: string;
  durationField: string;
  durationOptions: Array<{ key: string; label: string; seconds: number }>;
}

/** verifiedAt is first confirmation, never an asserted original grant timestamp. */
export type AccessResultView =
  | { outcome: 'granted'; verifiedAt: string; expiresAt: string; managed: true }
  | { outcome: 'already_present'; verifiedAt: string; expiresAt: null; managed: false };

export type FulfillmentState =
  | 'awaiting_approval'
  | 'fulfilling'
  | 'completed'
  | 'rejected'
  | 'cancelled'
  | 'unknown';
