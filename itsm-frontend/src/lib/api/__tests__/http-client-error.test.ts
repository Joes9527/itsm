import { ApiError, apiErrorMessage } from '../http-client';

describe('apiErrorMessage', () => {
  it('appends backend field errors so configuration causes are visible', () => {
    const error = new ApiError(
      'catalog publication configuration is incomplete',
      400,
      1001,
      'DomainValidationFailed',
      false,
      [{ field: 'publication', message: 'publication requires a declared process or no_process binding' }]
    );
    expect(apiErrorMessage(error, '创建失败')).toBe(
      'catalog publication configuration is incomplete：publication requires a declared process or no_process binding'
    );
  });

  it('keeps the backend message when there are no field errors', () => {
    const error = new ApiError('该流程定义存在 4 个历史实例，为保留流程执行历史不能删除；可停用该流程定义', 409, 4090);
    expect(apiErrorMessage(error, '删除失败')).toBe(
      '该流程定义存在 4 个历史实例，为保留流程执行历史不能删除；可停用该流程定义'
    );
  });

  it('falls back to plain Error messages and then to the caller default', () => {
    expect(apiErrorMessage(new Error('boom'), '删除失败')).toBe('boom');
    expect(apiErrorMessage(undefined, '删除失败')).toBe('删除失败');
    expect(apiErrorMessage({}, '删除失败')).toBe('删除失败');
  });
});
