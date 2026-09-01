/**
 * HTTP Client 测试
 */

// 测试前需要模拟环境
const mockCookie = jest.fn();
const mockLocalStorage = jest.fn();

Object.defineProperty(document, 'cookie', {
  get: mockCookie,
  set: mockCookie,
});

Object.defineProperty(global, 'localStorage', {
  value: {
    getItem: mockLocalStorage,
    setItem: mockLocalStorage,
    removeItem: mockLocalStorage,
  },
});

// Mock security module
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

// Mock env module
jest.mock('@/lib/env', () => ({
  logger: {
    debug: jest.fn(),
    info: jest.fn(),
    error: jest.fn(),
    warn: jest.fn(),
  },
}));

// 延迟导入以确保 mock 生效
import HttpClient from '../api/http-client';

describe('HttpClient', () => {
  let client: HttpClient;

  beforeEach(() => {
    jest.clearAllMocks();
    mockCookie.mockReturnValue('');
    mockLocalStorage.mockReturnValue(null);
    client = new HttpClient('http://localhost:8080');
  });

  describe('constructor', () => {
    it('should create client with default base URL', () => {
      const defaultClient = new HttpClient();
      expect(defaultClient.getBaseURL()).toBeDefined();
    });

    it('should create client with custom base URL', () => {
      const customClient = new HttpClient('http://custom-api:9000');
      expect(customClient.getBaseURL()).toBe('http://custom-api:9000');
    });
  });

  describe('getBaseURL', () => {
    it('should return base URL', () => {
      expect(client.getBaseURL()).toBe('http://localhost:8080');
    });
  });
});
