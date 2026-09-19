import { catalogDeclaresReason, catalogOwnsRequiredReason } from '../catalog-reason';

const field = (name: string, required = true) => ({
  name,
  label: name,
  type: 'textarea',
  required,
});

// 「目录是否已经问过申请理由」是申请页决定要不要再问一次的依据，
// 判定只看字段名（label 是展示文案，不能作为契约依据），这里固化它的实际范围。
describe('catalogDeclaresReason', () => {
  it('matches reason field names at a word boundary', () => {
    for (const name of ['reason', 'access_reason', 'business_reason', 'apply_reason']) {
      expect(catalogDeclaresReason([field(name)] as never)).toBe(true);
    }
  });

  it('does not match names that only contain "reason" elsewhere', () => {
    for (const name of ['reason_detail', 'reason_text', 'businessReason', 'reasoning']) {
      expect(catalogDeclaresReason([field(name)] as never)).toBe(false);
    }
  });

  it('accepts a Catalog with no fields at all', () => {
    expect(catalogDeclaresReason([] as never)).toBe(false);
    expect(catalogDeclaresReason(undefined as never)).toBe(false);
  });

  it('已知边界：has_reason 这类否定式命名也会命中', () => {
    // 记录而非修复：命中后还要看该字段是否必填（见 catalogOwnsRequiredReason），
    // 只有"否定式命名且必填"这种组合才会真正顶掉必填的申请理由。
    expect(catalogDeclaresReason([field('has_reason', false)] as never)).toBe(true);
    expect(catalogOwnsRequiredReason([field('has_reason', false)] as never)).toBe(false);
  });
});

describe('catalogOwnsRequiredReason', () => {
  it('is true only when the declared reason field is itself required', () => {
    expect(catalogOwnsRequiredReason([field('access_reason', true)] as never)).toBe(true);
    expect(catalogOwnsRequiredReason([field('access_reason', false)] as never)).toBe(false);
    expect(catalogOwnsRequiredReason([field('office_location', true)] as never)).toBe(false);
  });
});
