import { buildTicketFormFields } from '../ticket-form-fields';

describe('ticket form execution payload', () => {
  const fields = [{ name: 'duration', label: 'Duration' }];
  const values = [{ name: 'duration', value: 30 }];

  it('submits static preset definitions and values without a preset instruction', () => {
    expect(buildTicketFormFields(fields, values)).toEqual({
      values: [{ name: 'duration', value: 30 }],
      fieldDefs: [{ name: 'duration', label: 'Duration' }],
    });
  });

  it('leaves database template definitions authoritative and retains values', () => {
    expect(buildTicketFormFields(fields, values, 12)).toEqual({
      values: [{ name: 'duration', value: 30 }],
    });
  });
});
