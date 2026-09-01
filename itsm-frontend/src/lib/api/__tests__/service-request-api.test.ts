import { serviceRequestAPI } from '@/lib/api/service-request-api';

const mockGet = jest.fn();
const mockPost = jest.fn();
jest.mock('@/lib/api/http-client', () => ({
  httpClient: {
    get: (...args: unknown[]) => mockGet(...args),
    post: (...args: unknown[]) => mockPost(...args),
  },
}));

describe('ServiceRequestAPI', () => {
  beforeEach(() => { jest.clearAllMocks(); });

  describe('getUserServiceRequests', () => {
    it('should get user service requests', async () => {
      mockGet.mockResolvedValue({ requests: [{ id: 1 }], total: 1, page: 1, size: 10 });
      const result = await serviceRequestAPI.getUserServiceRequests({ page: 1, size: 10 });
      expect(mockGet).toHaveBeenCalledWith(expect.stringContaining('/api/v1/service-requests/me'));
      expect(result.requests).toHaveLength(1);
    });

    it('fails closed instead of reading a legacy items fallback', async () => {
      mockGet.mockResolvedValue({ items: [{ id: 1 }], total: 1, page: 1, size: 10 });

      await expect(serviceRequestAPI.getUserServiceRequests()).rejects.toThrow(
        'Invalid service request list contract'
      );
    });
  });

  describe('getServiceRequestDetails', () => {
    it('should get service request details', async () => {
      mockGet.mockResolvedValue({ id: 1, catalogId: 2, requesterId: 3, status: 'submitted', version: 1, createdAt: '' });
      const result = await serviceRequestAPI.getServiceRequestDetails(1);
      expect(mockGet).toHaveBeenCalledWith('/api/v1/service-requests/1');
      expect(result.id).toBe(1);
    });
  });

  describe('createServiceRequest', () => {
    it('should create a service request', async () => {
      const data = { catalogId: 1, complianceAck: true };
      mockPost.mockResolvedValue({ id: 10, ...data, status: 'submitted', version: 1, createdAt: '' });
      const result = await serviceRequestAPI.createServiceRequest(data);
      expect(mockPost).toHaveBeenCalledWith('/api/v1/service-requests', data);
      expect(result.id).toBe(10);
    });
  });

  describe('startProvisioning', () => {
    it('should start provisioning', async () => {
      mockPost.mockResolvedValue({ task: { id: 1, status: 'pending' } });
      const result = await serviceRequestAPI.startProvisioning(1);
      expect(mockPost).toHaveBeenCalledWith('/api/v1/service-requests/1/provision', {});
      expect(result.task.id).toBe(1);
    });
  });

  describe('healthCheck', () => {
    it('should check health', async () => {
      mockGet.mockResolvedValue({ status: 'ok' });
      const result = await serviceRequestAPI.healthCheck();
      expect(mockGet).toHaveBeenCalledWith('/api/v1/health');
      expect(result.status).toBe('ok');
    });
  });
});
