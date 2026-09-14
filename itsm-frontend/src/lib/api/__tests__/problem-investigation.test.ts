import { ProblemInvestigationAPI } from '../problem-investigation';
import { httpClient } from '../http-client';

jest.mock('../http-client', () => ({
  httpClient: {
    get: jest.fn(),
    post: jest.fn(),
    put: jest.fn(),
    delete: jest.fn(),
  },
}));

const mockGet = httpClient.get as jest.Mock;
const mockPost = httpClient.post as jest.Mock;
const mockPut = httpClient.put as jest.Mock;

describe('ProblemInvestigationAPI', () => {
  beforeEach(() => { jest.clearAllMocks(); });

  describe('getSummary', () => {
    it('should get summary', async () => {
      mockGet.mockResolvedValue({ investigation: {}, steps: [], solutions: [] });
      await ProblemInvestigationAPI.getSummary(1);
      expect(mockGet).toHaveBeenCalledWith('/api/v1/problem-investigation/problems/1/summary');
    });
  });

  describe('createInvestigation', () => {
    it('should create investigation', async () => {
      mockPost.mockResolvedValue({ workItemId: 1, version: 2, status: 'investigating', replayed: false });
      const result = await ProblemInvestigationAPI.createInvestigation({ problemId: 1, version: 1, operationId: 'start-1' });
      expect(mockPost).toHaveBeenCalledWith('/api/v1/problem-investigation/investigations', { problemId: 1, version: 1, operationId: 'start-1' });
      expect(result.workItemId).toBe(1);
    });
  });

  describe('updateInvestigation', () => {
    it('should update investigation', async () => {
      mockPut.mockResolvedValue({workItemId:91,version:8,status:"investigating",replayed:false});
      const result = await ProblemInvestigationAPI.updateInvestigation(1, { problemId:4, version:7, operationId:'evidence-1', status: 'in_progress' });
      expect(mockPut).toHaveBeenCalledWith('/api/v1/problem-investigation/investigations/1', { problemId:4, version:7, operationId:'evidence-1', status: 'in_progress' });
      expect(result.workItemId).toBe(91);
    });
  });

  describe('getSteps', () => {
    it('should get steps', async () => {
      mockGet.mockResolvedValue({ steps: [{ id: 1, stepTitle: 'Step 1' }] });
      const result = await ProblemInvestigationAPI.getSteps(1);
      expect(mockGet).toHaveBeenCalledWith('/api/v1/problem-investigation/investigations/1/steps');
      expect(result).toHaveLength(1);
    });

    it('should return empty array when no steps', async () => {
      mockGet.mockResolvedValue({});
      const result = await ProblemInvestigationAPI.getSteps(1);
      expect(result).toEqual([]);
    });
  });

  describe('createStep', () => {
    it('should create step', async () => {
      mockPost.mockResolvedValue({workItemId:91,version:8,status:"investigating",replayed:false});
      const result = await ProblemInvestigationAPI.createStep({ problemId:4, version:7, operationId:"evidence-1", investigationId: 1, stepNumber: 1, stepTitle: 'Step 1', stepDescription: 'desc' });
      expect(mockPost).toHaveBeenCalledWith('/api/v1/problem-investigation/steps', expect.any(Object));
      expect(result.workItemId).toBe(91);
    });
  });

  describe('updateStep', () => {
    it('should update step', async () => {
      mockPut.mockResolvedValue({workItemId:91,version:8,status:"investigating",replayed:false});
      const result = await ProblemInvestigationAPI.updateStep(1, { problemId:4, version:7, operationId:'evidence-1', status: 'completed' });
      expect(mockPut).toHaveBeenCalledWith('/api/v1/problem-investigation/steps/1', { problemId:4, version:7, operationId:'evidence-1', status: 'completed' });
      expect(result.workItemId).toBe(91);
    });
  });

  describe('createRootCause', () => {
    it('should create root cause', async () => {
      const data = { problemId: 1, version: 7, operationId: 'rca-create', analysisMethod: '5-why', rootCauseDescription: 'server config', confidenceLevel: 'high' as const };
      mockPost.mockResolvedValue({ analysis: { id: 1, ...data } });
      const result = await ProblemInvestigationAPI.createRootCause(data);
      expect(mockPost).toHaveBeenCalledWith('/api/v1/problem-investigation/root-cause-analysis', data);
      expect(result.analysisMethod).toBe('5-why');
    });
  });

  describe('updateRootCause', () => {
    it('should update root cause', async () => {
      mockPut.mockResolvedValue({ analysis: { id: 1, confidenceLevel: 'medium' } });
      const result = await ProblemInvestigationAPI.updateRootCause(1, { confidenceLevel: 'medium', version: 8, operationId: 'rca-update' });
      expect(mockPut).toHaveBeenCalledWith('/api/v1/problem-investigation/root-cause-analysis/1', { confidenceLevel: 'medium', version: 8, operationId: 'rca-update' });
      expect(result.confidenceLevel).toBe('medium');
    });
  });

  describe('getSolutions', () => {
    it('should get solutions', async () => {
      mockGet.mockResolvedValue({ solutions: [{ id: 1 }] });
      const result = await ProblemInvestigationAPI.getSolutions(1);
      expect(mockGet).toHaveBeenCalledWith('/api/v1/problem-investigation/problems/1/solutions');
      expect(result).toHaveLength(1);
    });

    it('should return empty array when no solutions', async () => {
      mockGet.mockResolvedValue({});
      const result = await ProblemInvestigationAPI.getSolutions(1);
      expect(result).toEqual([]);
    });
  });

  describe('createSolution', () => {
    it('should create solution', async () => {
      const data = { problemId: 4, version:7, operationId:"evidence-1", solutionType: 'fix' as const, solutionDescription: 'patch', priority: 'high' };
      mockPost.mockResolvedValue({workItemId:91,version:8,status:"investigating",replayed:false});
      const result = await ProblemInvestigationAPI.createSolution(data);
      expect(mockPost).toHaveBeenCalledWith('/api/v1/problem-investigation/solutions', data);
      expect(result.workItemId).toBe(91);
    });
  });

  describe('updateSolution', () => {
    it('should update solution', async () => {
      mockPut.mockResolvedValue({workItemId:91,version:8,status:"investigating",replayed:false});
      const result = await ProblemInvestigationAPI.updateSolution(1, { problemId:4, version:7, operationId:'evidence-1', status: 'approved' });
      expect(mockPut).toHaveBeenCalledWith('/api/v1/problem-investigation/solutions/1', { problemId:4, version:7, operationId:'evidence-1', status: 'approved' });
      expect(result.workItemId).toBe(91);
    });
  });

  describe('getRelationships', () => {
    it('should get relationships', async () => {
      mockGet.mockResolvedValue({ relationships: [{ id: 1 }] });
      const result = await ProblemInvestigationAPI.getRelationships(1);
      expect(mockGet).toHaveBeenCalledWith('/api/v1/problems/1/relationships');
      expect(result).toHaveLength(1);
    });
  });

  describe('createRelationship', () => {
    it('should create relationship', async () => {
      const data = { problemId: 1, relatedType: 'ticket', relatedId: 5, relationshipType: 'caused_by' };
      mockPost.mockResolvedValue(data);
      await ProblemInvestigationAPI.createRelationship(data);
      expect(mockPost).toHaveBeenCalledWith('/api/v1/problem-relationships', data);
    });
  });

  describe('createKnowledgeArticle', () => {
    it('should create knowledge article', async () => {
      const data = { problemId: 1, articleTitle: 'How to fix', articleContent: 'Steps', articleType: 'solution' };
      mockPost.mockResolvedValue({ article: { id: 1, ...data } });
      const result = await ProblemInvestigationAPI.createKnowledgeArticle(data);
      expect(mockPost).toHaveBeenCalledWith('/api/v1/problem-knowledge-articles', data);
      expect(result.articleTitle).toBe('How to fix');
    });
  });

  describe('getKnowledgeArticles', () => {
    it('should get knowledge articles', async () => {
      mockGet.mockResolvedValue({ knowledgeArticles: [{ id: 1 }] });
      const result = await ProblemInvestigationAPI.getKnowledgeArticles(1);
      expect(mockGet).toHaveBeenCalledWith('/api/v1/problem-knowledge-articles/problems/1');
      expect(result).toHaveLength(1);
    });
  });

  describe('error propagation', () => {
    it('should propagate errors', async () => {
      mockGet.mockRejectedValue(new Error('Server error'));
      await expect(ProblemInvestigationAPI.getSummary(999)).rejects.toThrow('Server error');
    });
  });
});
