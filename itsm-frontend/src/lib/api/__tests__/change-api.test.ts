import { creationReceipt, creationOptions } from '../creation.test-utils';
import { ChangeApi, type ChangeRequest } from '@/lib/api/change-api';

// Mock security module to avoid CSRF fetch calls
jest.mock('@/lib/security', () => ({
  security: {
    csrf: {
      getToken: jest.fn().mockResolvedValue('mock-csrf-token'),
      clearToken: jest.fn(),
    },
    network: {
      getSecureHeaders: jest.fn().mockReturnValue({
        'Content-Type': 'application/json',
        'X-Requested-With': 'XMLHttpRequest',
      }),
    },
  },
}));

// Mock fetch globally
global.fetch = jest.fn();

// Mock console methods to avoid noise in tests
const consoleSpy = {
  error: jest.spyOn(console, 'error').mockImplementation(() => {}),
  warn: jest.spyOn(console, 'warn').mockImplementation(() => {}),
  log: jest.spyOn(console, 'log').mockImplementation(() => {}),
};

describe('ChangeApi', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    (fetch as jest.Mock).mockClear();
  });

  afterAll(() => {
    Object.values(consoleSpy).forEach(spy => spy.mockRestore());
  });

  describe('getChanges', () => {
    it('should fetch changes successfully', async () => {
      const mockResponse = {
        code: 0,
        message: 'success',
        data: {
          changes: [
            {
              id: 1,
              title: 'Database Server Upgrade',
              description: 'Upgrade PostgreSQL from 14 to 15',
              justification: 'Performance improvements',
              type: 'normal',
              status: 'pending',
              priority: 'high',
              impactScope: 'high',
              riskLevel: 'medium',
              assigneeId: 2,
              assigneeName: 'John Doe',
              createdBy: 1,
              createdByName: 'Admin User',
              tenantId: 1,
              implementationPlan: 'Step 1: Prepare\nStep 2: Execute',
              rollbackPlan: 'Rollback steps',
              affectedCis: ['server-db-01'],
              relatedTickets: [],
              createdAt: '2024-01-01T10:00:00Z',
              updatedAt: '2024-01-01T10:00:00Z',
            },
          ],
          total: 1,
        },
      };

      (fetch as jest.Mock).mockResolvedValueOnce({
        ok: true,
        headers: new Headers(),
        status: 200,
        json: async () => mockResponse,
      });

      const result = await ChangeApi.getChanges();

      expect(fetch).toHaveBeenCalledWith(
        expect.stringContaining('/api/v1/changes'),
        expect.objectContaining({
          method: 'GET',
        })
      );

      expect(result.changes).toHaveLength(1);
      expect(result.changes[0].title).toBe('Database Server Upgrade');
    });

    it('should handle empty change list', async () => {
      const mockResponse = {
        code: 0,
        message: 'success',
        data: {
          changes: [],
          total: 0,
        },
      };

      (fetch as jest.Mock).mockResolvedValueOnce({
        ok: true,
        headers: new Headers(),
        status: 200,
        json: async () => mockResponse,
      });

      const result = await ChangeApi.getChanges();

      expect(result.changes).toHaveLength(0);
      expect(result.total).toBe(0);
    });

    it('should pass query parameters correctly', async () => {
      const mockResponse = {
        code: 0,
        message: 'success',
        data: { changes: [], total: 0 },
      };

      (fetch as jest.Mock).mockResolvedValueOnce({
        ok: true,
        headers: new Headers(),
        status: 200,
        json: async () => mockResponse,
      });

      await ChangeApi.getChanges({ page: 2, pageSize: 10, status: 'pending' });

      expect(fetch).toHaveBeenCalledWith(expect.stringContaining('page=2'), expect.any(Object));
    });

    it('should handle API error response', async () => {
      const mockResponse = {
        code: 5001,
        message: 'Internal server error',
        data: null,
      };

      (fetch as jest.Mock).mockResolvedValueOnce({
        ok: true,
        headers: new Headers(),
        status: 200,
        json: async () => mockResponse,
      });

      await expect(ChangeApi.getChanges({})).rejects.toThrow('Internal server error');
    });
  });

  describe('getChange', () => {
    it('should fetch single change successfully', async () => {
      const mockResponse = {
        code: 0,
        message: 'success',
        data: {
          id: 1,
          title: 'Test Change',
          description: 'Test description',
          justification: 'Test justification',
          type: 'normal',
          status: 'draft',
          priority: 'medium',
          impactScope: 'low',
          riskLevel: 'low',
          createdBy: 1,
          createdByName: 'Test User',
          tenantId: 1,
          implementationPlan: 'Plan',
          rollbackPlan: 'Rollback',
          affectedCis: [],
          relatedTickets: [],
          createdAt: '2024-01-01T10:00:00Z',
          updatedAt: '2024-01-01T10:00:00Z',
        },
      };

      (fetch as jest.Mock).mockResolvedValueOnce({
        ok: true,
        headers: new Headers(),
        status: 200,
        json: async () => mockResponse,
      });

      const result = await ChangeApi.getChange(1);

      expect(fetch).toHaveBeenCalledWith(
        expect.stringContaining('/api/v1/changes/1'),
        expect.objectContaining({
          method: 'GET',
        })
      );

      expect(result.id).toBe(1);
      expect(result.title).toBe('Test Change');
    });

    it('should handle change not found', async () => {
      const mockResponse = {
        code: 4004,
        message: 'Change not found',
        data: null,
      };

      (fetch as jest.Mock).mockResolvedValueOnce({
        ok: true,
        headers: new Headers(),
        status: 200,
        json: async () => mockResponse,
      });

      await expect(ChangeApi.getChange(999)).rejects.toBeDefined();
    });
  });

  describe('createChange', () => {
    it('should create change successfully', async () => {
      const mockResponse = {
        code: 0,
        message: 'success',
        data: {
          ...creationReceipt,
          recordClass: 'change_request',
          professionalReference: { type: 'change', id: 10 },
        },
      };

      (fetch as jest.Mock).mockResolvedValueOnce({
        ok: true,
        headers: new Headers(),
        status: 201,
        json: async () => mockResponse,
      });

      const newChange: ChangeRequest = {
        title: 'New Change Request',
        description: 'Description for new change',
        justification: 'Business justification',
        type: 'normal',
        priority: 'high',
        impactScope: 'medium',
        riskLevel: 'low',
        implementationPlan: 'Implementation steps',
        rollbackPlan: 'Rollback steps',
        affectedCis: ['server-01'],
        relatedTickets: [],
      };

      const result = await ChangeApi.createChange(newChange, creationOptions);

      expect(fetch).toHaveBeenCalledWith(
        expect.stringContaining('/api/v1/changes'),
        expect.objectContaining({
          method: 'POST',
        })
      );

      expect(result.number).toBe('WI-41');
      expect(result.professionalReference).toEqual({ type: 'change', id: 10 });
    });
  });

  describe('deleteChange', () => {
    it('should delete change successfully', async () => {
      const mockResponse = {
        code: 0,
        message: 'success',
        data: null,
      };

      (fetch as jest.Mock).mockResolvedValueOnce({
        ok: true,
        headers: new Headers(),
        status: 200,
        json: async () => mockResponse,
      });

      await ChangeApi.deleteChange(1);

      expect(fetch).toHaveBeenCalledWith(
        expect.stringContaining('/api/v1/changes/1'),
        expect.objectContaining({
          method: 'DELETE',
        })
      );
    });
  });

  describe('getChangeStats', () => {
    it('should fetch change statistics', async () => {
      const mockResponse = {
        code: 0,
        message: 'success',
        data: {
          total: 10,
          pending: 3,
          approved: 2,
          inProgress: 1,
          completed: 3,
          rolledBack: 0,
          rejected: 1,
          cancelled: 0,
        },
      };

      (fetch as jest.Mock).mockResolvedValueOnce({
        ok: true,
        headers: new Headers(),
        status: 200,
        json: async () => mockResponse,
      });

      const result = await ChangeApi.getChangeStats();

      expect(fetch).toHaveBeenCalledWith(
        expect.stringContaining('/api/v1/changes/stats'),
        expect.objectContaining({
          method: 'GET',
        })
      );

      expect(result.total).toBe(10);
      expect(result.pending).toBe(3);
    });
  });

  describe('getChangeApprovals', () => {
    it('should fetch approval history', async () => {
      const mockResponse = {
        code: 0,
        message: 'success',
        data: [
          {
            id: 1,
            changeId: 1,
            approverId: 10,
            approverName: 'CAB Chair',
            status: 'approved',
            comment: 'Approved',
            approvedAt: '2024-01-15T10:00:00Z',
            createdAt: '2024-01-15T09:00:00Z',
          },
        ],
      };

      (fetch as jest.Mock).mockResolvedValueOnce({
        ok: true,
        headers: new Headers(),
        status: 200,
        json: async () => mockResponse,
      });

      const result = await ChangeApi.getChangeApprovals(1);

      expect(fetch).toHaveBeenCalledWith(
        expect.stringContaining('/api/v1/changes/1/approvals'),
        expect.objectContaining({
          method: 'GET',
        })
      );

      expect(result).toHaveLength(1);
      expect(result[0].approverName).toBe('CAB Chair');
    });
  });

  describe('getRiskAssessment', () => {
    it('should fetch risk assessment', async () => {
      const mockResponse = {
        code: 0,
        message: 'success',
        data: {
          riskLevel: 'high',
          riskDescription: 'Database upgrade has inherent risks',
          impactAnalysis: 'Data loss possible if backup fails',
          mitigationMeasures: 'Multiple backup copies, dry-run in staging',
          contingencyPlan: 'Rollback to previous version',
          riskOwner: 'DBA Team Lead',
          riskScore: 75,
          riskFactors: ['Data migration complexity', 'Limited rollback window'],
        },
      };

      (fetch as jest.Mock).mockResolvedValueOnce({
        ok: true,
        headers: new Headers(),
        status: 200,
        json: async () => mockResponse,
      });

      const result = await ChangeApi.getRiskAssessment(1);

      expect(fetch).toHaveBeenCalledWith(
        expect.stringContaining('/api/v1/changes/1/risk'),
        expect.objectContaining({
          method: 'GET',
        })
      );

      expect(result?.riskLevel).toBe('high');
    });
  });
});
