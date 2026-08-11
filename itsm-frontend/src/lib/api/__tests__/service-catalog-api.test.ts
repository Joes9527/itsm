import { ServiceCatalogApi } from '@/lib/api/service-catalog-api';
import { httpClient } from '@/lib/api/http-client';

jest.mock('@/lib/api/http-client', () => ({
  httpClient: {
    get: jest.fn(),
    post: jest.fn(),
    put: jest.fn(),
    delete: jest.fn(),
    patch: jest.fn(),
  },
}));

const mockGet = httpClient.get as jest.Mock;
const mockPost = httpClient.post as jest.Mock;
const mockPut = httpClient.put as jest.Mock;
const mockDelete = httpClient.delete as jest.Mock;

describe('ServiceCatalogApi', () => {
  beforeEach(() => { jest.clearAllMocks(); });

  describe('getServices', () => {
    it('should get services list', async () => {
      mockGet.mockResolvedValue({ catalogs: [{ id: 1, name: 'Email', status: 'enabled', category: 'it_service' }], total: 1 });
      const result = await ServiceCatalogApi.getServices({ page: 1, pageSize: 10 });
      expect(mockGet).toHaveBeenCalledWith('/api/v1/service-catalogs', expect.objectContaining({ page: 1, size: 10 }));
      expect(result.services).toHaveLength(1);
    });
  });

  describe('getService', () => {
    it('should get a single service', async () => {
      mockGet.mockResolvedValue({ id: 1, name: 'Email', status: 'enabled' });
      const result = await ServiceCatalogApi.getService('1');
      expect(mockGet).toHaveBeenCalledWith('/api/v1/service-catalogs/1');
      expect(result.name).toBe('Email');
    });
  });

  describe('createService', () => {
    it('should create a service', async () => {
      mockPost.mockResolvedValue({ id: 2, name: 'VPN', status: 'enabled' });
      const result = await ServiceCatalogApi.createService({ name: 'VPN', category: 'it_service' as any } as any);
      expect(mockPost).toHaveBeenCalledWith('/api/v1/service-catalogs', expect.objectContaining({ name: 'VPN' }));
    });
  });

  describe('updateService', () => {
    it('should update a service', async () => {
      mockPut.mockResolvedValue({ id: 1, name: 'Updated', status: 'enabled' });
      const result = await ServiceCatalogApi.updateService('1', { name: 'Updated' } as any);
      expect(mockPut).toHaveBeenCalledWith('/api/v1/service-catalogs/1', expect.objectContaining({ name: 'Updated' }));
    });
  });

  describe('deleteService', () => {
    it('should delete a service', async () => {
      mockDelete.mockResolvedValue(undefined);
      await ServiceCatalogApi.deleteService('1');
      expect(mockDelete).toHaveBeenCalledWith('/api/v1/service-catalogs/1');
    });
  });

  describe('publishService', () => {
    it('should publish a service', async () => {
      mockPut.mockResolvedValue({ id: 1, status: 'enabled' });
      await ServiceCatalogApi.publishService('1');
      expect(mockPut).toHaveBeenCalledWith('/api/v1/service-catalogs/1', { status: 'enabled' });
    });
  });

  describe('retireService', () => {
    it('should retire a service', async () => {
      mockPut.mockResolvedValue({ id: 1, status: 'disabled' });
      await ServiceCatalogApi.retireService('1');
      expect(mockPut).toHaveBeenCalledWith('/api/v1/service-catalogs/1', { status: 'disabled' });
    });
  });

  describe('getServiceRequests', () => {
    it('should get service requests', async () => {
      mockGet.mockResolvedValue({ requests: [{ id: 1 }], total: 1 });
      const result = await ServiceCatalogApi.getServiceRequests({ page: 1 } as any);
      expect(mockGet).toHaveBeenCalled();
      expect(result.total).toBe(1);
    });
  });

  describe('getCatalogStats', () => {
    it('should get catalog stats', async () => {
      mockGet.mockResolvedValue({ totalServices: 20, publishedServices: 15, categories: {} });
      const result = await ServiceCatalogApi.getCatalogStats();
      expect(mockGet).toHaveBeenCalledWith('/api/v1/service-catalogs/stats');
      expect(result.totalServices).toBe(20);
    });
  });

  describe('getFavorites', () => {
    it('should return empty array (not implemented)', async () => {
      const result = await ServiceCatalogApi.getFavorites();
      expect(result).toEqual([]);
    });
  });

  describe('getPortalConfig', () => {
    it('should return default config', async () => {
      const result = await ServiceCatalogApi.getPortalConfig();
      expect(result.name).toBe('默认门户');
    });
  });

  describe('getServiceRequests', () => {
    it('should get service requests', async () => {
      mockGet.mockResolvedValue({ requests: [{ id: 1 }], total: 1 });
      const result = await ServiceCatalogApi.getServiceRequests({ page: 1, pageSize: 10 });
      expect(mockGet).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ page: 1, size: 10 }));
      expect(result.total).toBe(1);
    });

    // The retired SR-approval route (/service-requests/approvals/pending) and its
    // isPendingApproval branch have been removed — approval now flows through the linked
    // ticket's own BPMN mechanism. getServiceRequests always hits /me regardless of the
    // status value passed (including legacy 'pending_approval'/'pending' filter values).
    it('always uses the /me endpoint, never the retired pending-approvals endpoint', async () => {
      mockGet.mockResolvedValue({ requests: [], total: 0 });
      await ServiceCatalogApi.getServiceRequests({ status: 'pending_approval' } as any);
      expect(mockGet).toHaveBeenCalledWith('/api/v1/service-requests/me', expect.any(Object));
    });
  });

  describe('getServiceRequest', () => {
    it('should get single request', async () => {
      mockGet.mockResolvedValue({ id: 1, status: 'open' });
      await ServiceCatalogApi.getServiceRequest(1);
      expect(mockGet).toHaveBeenCalledWith('/api/v1/service-requests/1');
    });
  });

  describe('createServiceRequest', () => {
    it('should create service request', async () => {
      mockPost.mockResolvedValue({ id: 1 });
      await ServiceCatalogApi.createServiceRequest({ serviceId: '5', formData: { reason: 'Need access' } } as any);
      expect(mockPost).toHaveBeenCalledWith('/api/v1/service-requests', expect.objectContaining({ catalogId: 5 }));
    });
  });

  describe('getCatalogStats', () => {
    it('should get catalog stats', async () => {
      mockGet.mockResolvedValue({ totalServices: 10, publishedServices: 8, categories: {} });
      const result = await ServiceCatalogApi.getCatalogStats();
      expect(mockGet).toHaveBeenCalledWith('/api/v1/service-catalogs/stats');
      expect(result.totalServices).toBe(10);
    });
  });

  describe('getServiceAnalytics', () => {
    it('should return default analytics', async () => {
      const result = await ServiceCatalogApi.getServiceAnalytics('1', { startDate: '2024-01-01' });
      expect(result.serviceId).toBe('1');
      expect(result.metrics.totalRequests).toBe(0);
    });
  });

  describe('getFavorites', () => {
    it('should return empty array', async () => {
      const result = await ServiceCatalogApi.getFavorites();
      expect(result).toEqual([]);
    });
  });

  describe('getServiceRatings', () => {
    it('should return empty ratings', async () => {
      const result = await ServiceCatalogApi.getServiceRatings('1');
      expect(result).toEqual({ ratings: [], total: 0, avgRating: 0 });
    });
  });

  describe('recordServiceView', () => {
    it('should not throw', async () => {
      await expect(ServiceCatalogApi.recordServiceView('1')).resolves.toBeUndefined();
    });
  });

  describe('exportCatalog', () => {
    it('should export catalog as CSV', async () => {
      mockGet.mockResolvedValue({ services: [{ id: '1', name: 'Svc', category: 'IT', status: 'active', shortDescription: 'desc', availability: { responseTime: '1h' }, createdAt: '2024-01-01', updatedAt: '2024-01-02' }], total: 1 });
      const result = await ServiceCatalogApi.exportCatalog('excel');
      expect(result).toBeInstanceOf(Blob);
    });
  });

  describe('retireService', () => {
    it('should retire service', async () => {
      mockPut.mockResolvedValue({ id: '1', status: 'disabled' });
      await ServiceCatalogApi.retireService('1');
      expect(mockPut).toHaveBeenCalledWith('/api/v1/service-catalogs/1', { status: 'disabled' });
    });
  });

  describe('cloneService', () => {
    it('should clone service', async () => {
      mockGet.mockResolvedValue({ id: '1', name: 'Original', category: 'IT' });
      mockPost.mockResolvedValue({ id: '2', name: 'Clone' });
      await ServiceCatalogApi.cloneService('1', 'Clone');
      expect(mockGet).toHaveBeenCalledWith('/api/v1/service-catalogs/1');
      expect(mockPost).toHaveBeenCalled();
    });
  });
});
