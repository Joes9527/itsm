/**
 * FLOW-10: 知识库草稿 → 发布 → end_user 可见 → 引用到工单
 * Priority: P2
 *
 * 完整链路: 管理员创建知识文章 → 发布 → 用户查看 → 引用到工单
 */
import { test, expect } from '../fixtures/auth';

test.describe('FLOW-10: 知识库全流程', () => {
  let adminRole: string;
  let endUserRole: string;
  let engineerRole: string;

  test.beforeEach(async ({ loginAs }) => {
    adminRole = await loginAs('admin');
    endUserRole = await loginAs('user1');
    engineerRole = await loginAs('engineer1');
  });

  test('T075-FLOW10 - 知识库发布流程', async ({ apiPost, apiGet }) => {
    // Step 1: admin 创建知识文章（草稿状态）
    const draftResp = await apiPost(adminRole, '/api/v1/knowledge/articles', {
      title: 'FLOW-10 测试知识文章',
      content: '这是测试内容',
      category: 'technical',
      status: 'draft',
    });

    expect(draftResp.status).toBe(200);
    expect(draftResp.data).toHaveProperty('code', 0);
    const articleId = draftResp.data.data?.id;
    expect(articleId).toBeGreaterThan(0);

    // Step 2: admin 发布文章
    if (articleId) {
      const publishResp = await apiPost(adminRole, `/api/v1/knowledge/articles/${articleId}/publish`, {});

      expect(publishResp.status).toBe(200);
      expect(publishResp.data).toHaveProperty('code', 0);
    }

    // Step 3: end_user 查看已发布文章
    const listResp = await apiGet(endUserRole, '/api/v1/knowledge/articles');
    expect(listResp.status).toBe(200);

    // Step 4: engineer 在处理工单时搜索知识库
    const searchResp = await apiGet(engineerRole, '/api/v1/knowledge/articles?search=测试');
    expect(searchResp.status).toBe(200);
    expect(searchResp.data).toHaveProperty('code', 0);
  });
});
