/**
 * Auth Service 完整测试套件
 */

import { AuthService } from '../auth-service';

// Mock Zustand store
const mockLogin = jest.fn();
const mockLogout = jest.fn();

jest.mock('@/lib/store/auth-store', () => ({
  useAuthStore: {
    getState: jest.fn(() => ({
      user: {
        id: 1,
        username: 'testuser',
        email: 'test@example.com',
        name: 'Test User',
        role: 'agent',
        tenantId: 1,
      },
      isAuthenticated: true,
      login: mockLogin,
      logout: mockLogout,
    })),
  },
}));

// Mock fetch
const mockFetch = jest.fn();
global.fetch = mockFetch;

// Mock document.cookie
const mockCookieStore: Record<string, string> = {};
Object.defineProperty(document, 'cookie', {
  get: jest.fn(() => {
    return Object.entries(mockCookieStore)
      .map(([key, value]) => `${key}=${value}`)
      .join('; ');
  }),
  set: jest.fn((cookie: string) => {
    const [key, value] = cookie.split('=');
    if (value.includes('max-age=0')) {
      delete mockCookieStore[key.trim()];
    } else {
      mockCookieStore[key.trim()] = value.split(';')[0];
    }
  }),
});

describe('AuthService', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    // Clear cookie store
    Object.keys(mockCookieStore).forEach(key => delete mockCookieStore[key]);
  });

  describe('Authentication Status', () => {
    describe('getCurrentUser', () => {
      it('should return current user from store', () => {
        const user = AuthService.getCurrentUser();
        expect(user).toBeDefined();
        expect(user?.id).toBe(1);
        expect(user?.username).toBe('testuser');
      });
    });

    describe('isAuthenticated', () => {
      it('should return true when store has authenticated status', () => {
        const result = AuthService.isAuthenticated();
        expect(result).toBe(true);
      });

    });
  });

  describe('Login Functionality', () => {
    describe('login', () => {
      it('should login successfully with valid credentials', async () => {
        mockFetch.mockResolvedValueOnce({
          ok: true,
          json: () =>
            Promise.resolve({
              code: 0,
              message: 'success',
              data: {
                user: {
                  id: 1,
                  username: 'testuser',
                  email: 'test@example.com',
                  name: 'Test User',
                  role: 'agent',
                  tenantId: 1,
                },
                tenant: {
                  id: 1,
                  name: 'Test Tenant',
                  code: 'test',
                  type: 'standard',
                  status: 'active',
                },
              },
            }),
        });

        const result = await AuthService.login('testuser', 'password123', 'test');

        expect(result).toBe(true);
        expect(mockLogin).toHaveBeenCalled();
        expect(mockFetch).toHaveBeenCalledWith(
          expect.stringContaining('/api/v1/auth/login'),
          expect.objectContaining({
            method: 'POST',
            body: expect.stringContaining('testuser'),
          })
        );
      });

      it('should return false on login failure', async () => {
        mockFetch.mockResolvedValueOnce({
          ok: false,
          json: () => Promise.resolve({ code: 1001, message: 'Invalid credentials' }),
        });

        const result = await AuthService.login('wronguser', 'wrongpass');

        expect(result).toBe(false);
      });

      it('should handle network errors gracefully', async () => {
        mockFetch.mockRejectedValueOnce(new Error('Network error'));

        const result = await AuthService.login('testuser', 'password');

        expect(result).toBe(false);
      });
    });
  });

  describe('Registration', () => {
    it('should register user successfully', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () =>
          Promise.resolve({
            code: 0,
            message: 'success',
            data: { id: 2, username: 'newuser', email: 'new@example.com' },
          }),
      });

      const result = await AuthService.register({
        username: 'newuser',
        email: 'new@example.com',
        password: 'password123',
        fullName: 'New User',
        phone: '1234567890',
        company: 'Test Company',
        role: 'user',
      });

      expect(result).toBe(true);
    });

    it('should return false on registration failure', async () => {
      mockFetch.mockRejectedValueOnce(new Error('Registration failed'));

      const result = await AuthService.register({
        username: 'newuser',
        email: 'new@example.com',
        password: 'password123',
        fullName: 'New User',
      });

      expect(result).toBe(false);
    });
  });

  describe('Password Reset', () => {
    describe('forgotPassword', () => {
      it('should send password reset email successfully', async () => {
        mockFetch.mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve({ code: 0, message: 'Reset email sent' }),
        });

        const result = await AuthService.forgotPassword('test@example.com', 'test');

        expect(result).toBe(true);
      });

      it('should return false on forgot password failure', async () => {
        mockFetch.mockRejectedValueOnce(new Error('Failed to send email'));

        const result = await AuthService.forgotPassword('test@example.com');

        expect(result).toBe(false);
      });
    });

    describe('resetPassword', () => {
      it('should reset password successfully', async () => {
        mockFetch.mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve({ code: 0, message: 'Password reset' }),
        });

        const result = await AuthService.resetPassword({
          token: 'reset-token',
          email: 'test@example.com',
          password: 'newpassword123',
          passwordConfirm: 'newpassword123',
        });

        expect(result).toBe(true);
      });

      it('should return false on reset password failure', async () => {
        mockFetch.mockRejectedValueOnce(new Error('Invalid token'));

        const result = await AuthService.resetPassword({
          token: 'invalid-token',
          email: 'test@example.com',
          password: 'newpass',
          passwordConfirm: 'newpass',
        });

        expect(result).toBe(false);
      });
    });

    describe('validateResetToken', () => {
      it('should validate reset token successfully', async () => {
        mockFetch.mockResolvedValueOnce({
          ok: true,
          json: () =>
            Promise.resolve({
              code: 0,
              message: 'success',
              data: { valid: true, email: 'test@example.com' },
            }),
        });

        const result = await AuthService.validateResetToken('valid-token', 'test@example.com');

        expect(result).toBe(true);
      });

      it('should return false for invalid token', async () => {
        mockFetch.mockRejectedValueOnce(new Error('Invalid token'));

        const result = await AuthService.validateResetToken('invalid-token', 'test@example.com');

        expect(result).toBe(false);
      });
    });
  });

  describe('Logout', () => {
    it('should logout successfully', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({ code: 0, message: 'Logged out' }),
      });

      expect(() => AuthService.logout()).not.toThrow();
      expect(mockLogout).toHaveBeenCalled();
    });

    it('should handle logout network error gracefully', async () => {
      mockFetch.mockRejectedValueOnce(new Error('Network error'));

      expect(() => AuthService.logout()).not.toThrow();
      expect(mockLogout).toHaveBeenCalled();
    });
  });
});
