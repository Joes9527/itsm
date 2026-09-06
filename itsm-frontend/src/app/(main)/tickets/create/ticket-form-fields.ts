export function buildTicketFormFields(
  fields: Array<{ name: string; label: string }>,
  values: Array<{ name: string; value: unknown }>,
  templateId?: number
) {
  return {
    values,
    ...(templateId ? {} : { fieldDefs: fields.map(({ name, label }) => ({ name, label })) }),
  };
}
