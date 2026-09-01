import { clearCredentials, loadCredentials, saveCredentials, type Credentials } from './credentials.js';
import type {
  LoginRequest,
  LoginResponse,
  User,
  Ticket,
  TicketListResponse,
  CreateTicketRequest,
  PaginationParams,
  Incident,
  IncidentListResponse,
  Change,
  ChangeListResponse,
  CI,
  CIListResponse,
  KnowledgeArticle,
  KnowledgeListResponse,
  Notification,
  NotificationListResponse,
  ConnectorManifest,
  ProcessInstance,
  ProcessInstanceListResponse,
  ApprovalTask,
  ApprovalTaskListResponse,
} from './types.js';

const DEFAULT_BASE_URL = 'http://localhost:8090';
const MUTATION_METHODS = new Set(['POST', 'PUT', 'PATCH', 'DELETE']);

type ApiEnvelope<T> = { code: number; message: string; data: T };
type BackendListResponse<T> = {
  data: T[];
  pagination: { total: number; page: number; pageSize: number };
};

function responseSetCookies(headers: Headers): string[] {
  const values = (headers as Headers & { getSetCookie?: () => string[] }).getSetCookie?.();
  if (values?.length) return values;
  const combined = headers.get('set-cookie');
  return combined ? combined.split(/,(?=\s*[^;,]+=)/) : [];
}

function mergeCookieHeader(current: string, setCookies: string[]): string {
  const cookies = new Map<string, string>();
  for (const pair of current.split(';')) {
    const separator = pair.indexOf('=');
    if (separator > 0) cookies.set(pair.slice(0, separator).trim(), pair.slice(separator + 1).trim());
  }
  for (const setCookie of setCookies) {
    const pair = setCookie.split(';', 1)[0];
    const separator = pair.indexOf('=');
    if (separator <= 0) continue;
    const name = pair.slice(0, separator).trim();
    const value = pair.slice(separator + 1).trim();
    const expired = /(?:^|;)\s*max-age\s*=\s*(?:0|-\d+)/i.test(setCookie);
    if (!value || expired) cookies.delete(name);
    else cookies.set(name, value);
  }
  return [...cookies.entries()].map(([name, value]) => `${name}=${value}`).join('; ');
}

export class ApiClient {
  constructor(private baseURL: string = DEFAULT_BASE_URL) {}

  private persistResponseCookies(response: Response, current: Credentials): Credentials {
    const cookieHeader = mergeCookieHeader(current.cookieHeader, responseSetCookies(response.headers));
    const updated = { ...current, cookieHeader };
    if (cookieHeader) saveCredentials(updated);
    else clearCredentials();
    return updated;
  }

  private async refreshSession(): Promise<void> {
    const current = loadCredentials();
    if (!current) throw new Error('Not logged in');
    try {
      const response = await fetch(`${this.baseURL}/api/v1/auth/refresh`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Cookie: current.cookieHeader },
        body: '{}',
      });
      if (!response.ok) throw new Error(`HTTP ${response.status}: ${response.statusText}`);
      const body = await response.json() as ApiEnvelope<unknown>;
      if (body.code !== 0) throw new Error(body.message || 'Session refresh failed');
      const updated = this.persistResponseCookies(response, current);
      if (!/(?:^|;\s*)access_token=/.test(updated.cookieHeader)
        || !/(?:^|;\s*)refresh_token=/.test(updated.cookieHeader)) {
        throw new Error('Session refresh did not rotate authentication cookies');
      }
    } catch (error) {
      clearCredentials();
      throw error;
    }
  }

  private async csrfToken(current: Credentials): Promise<{ token: string; credentials: Credentials }> {
    const response = await fetch(`${this.baseURL}/api/v1/csrf-token`, {
      method: 'GET',
      headers: { Cookie: current.cookieHeader },
    });
    if (!response.ok) throw new Error(`HTTP ${response.status}: ${response.statusText}`);
    const body = await response.json() as ApiEnvelope<{ csrf_token: string }>;
    if (body.code !== 0 || !body.data?.csrf_token) throw new Error(body.message || 'CSRF token unavailable');
    return { token: body.data.csrf_token, credentials: this.persistResponseCookies(response, current) };
  }

  private async request<T>(
    method: string,
    endpoint: string,
    data?: unknown,
    params?: Record<string, string | number | boolean | undefined>,
    retryAfterRefresh = true,
  ): Promise<T> {
    let credentials = loadCredentials();
    if (!credentials) throw new Error('Not logged in');
    const url = new URL(`${this.baseURL}/api/v1${endpoint}`);
    if (params) {
      Object.entries(params).forEach(([key, value]) => {
        if (value !== undefined) url.searchParams.set(key, String(value));
      });
    }

    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      Cookie: credentials.cookieHeader,
    };
    if (MUTATION_METHODS.has(method)) {
      const csrf = await this.csrfToken(credentials);
      credentials = csrf.credentials;
      headers.Cookie = credentials.cookieHeader;
      headers['X-CSRF-Token'] = csrf.token;
    }

    const response = await fetch(url, {
      method,
      headers,
      body: data === undefined ? undefined : JSON.stringify(data),
    });
    this.persistResponseCookies(response, credentials);
    if (response.status === 401 && retryAfterRefresh) {
      await this.refreshSession();
      return this.request<T>(method, endpoint, data, params, false);
    }
    if (!response.ok) throw new Error(`HTTP ${response.status}: ${response.statusText}`);
    const body = await response.json() as ApiEnvelope<T>;
    if (body.code !== 0) throw new Error(body.message || 'API error');
    return body.data;
  }

  // ---------- Auth ----------
  async login(loginData: LoginRequest): Promise<LoginResponse> {
    const url = `${this.baseURL}/api/v1/auth/login`;
    const res = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(loginData),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const json = await res.json() as ApiEnvelope<LoginResponse>;
    if (json.code !== 0) throw new Error(json.message || 'Login failed');
    const cookieHeader = mergeCookieHeader('', responseSetCookies(res.headers));
    if (!/(?:^|;\s*)access_token=/.test(cookieHeader)
      || !/(?:^|;\s*)refresh_token=/.test(cookieHeader)) {
      throw new Error('Login did not establish the required cookie session');
    }
    const result: LoginResponse = { user: json.data.user, tenant: json.data.tenant };
    saveCredentials({
      cookieHeader,
      user: result.user,
      tenantId: result.tenant.id,
      tenantName: result.tenant.name,
    });
    return result;
  }
  async logout(): Promise<void> {
    try {
      await this.request<void>('POST', '/auth/logout');
    } finally {
      clearCredentials();
    }
  }
  async getUserInfo(): Promise<User> { return this.request<User>('GET', '/auth/me'); }

  // ---------- Tickets ----------
  async listTickets(params?: PaginationParams): Promise<TicketListResponse> {
    return this.request<TicketListResponse>('GET', '/tickets', undefined, params as Record<string, string | number | undefined>);
  }
  async getTicket(id: number): Promise<Ticket> {
    return this.request<Ticket>('GET', `/tickets/${id}`);
  }
  async createTicket(data: CreateTicketRequest): Promise<Ticket> {
    return this.request<Ticket>('POST', '/tickets', data);
  }
  async searchTickets(query: string, params?: PaginationParams): Promise<TicketListResponse> {
    return this.request<TicketListResponse>('GET', '/tickets/search', undefined, { ...params, search: query } as unknown as Record<string, string | number | boolean | undefined>);
  }
  async getOverdueTickets(): Promise<Ticket[]> {
    return this.request<Ticket[]>('GET', '/tickets/overdue');
  }
  async globalSearch(q: string, page?: number): Promise<Record<string, unknown>> {
    return this.request<Record<string, unknown>>('GET', '/search', undefined, { q, page });
  }

  // ---------- Incidents ----------
  async listIncidents(params?: PaginationParams & { status?: string; priority?: string }): Promise<IncidentListResponse> {
    return this.request<IncidentListResponse>('GET', '/incidents', undefined, params as Record<string, string | number | undefined>);
  }
  async getIncident(id: number): Promise<Incident> {
    return this.request<Incident>('GET', `/incidents/${id}`);
  }
  async aiTriage(payload: { title: string; description: string }): Promise<Record<string, unknown>> {
    return this.request<Record<string, unknown>>('POST', '/ai/triage', payload);
  }

  // ---------- Changes ----------
  async listChanges(params?: PaginationParams & { status?: string }): Promise<ChangeListResponse> {
    return this.request<ChangeListResponse>('GET', '/changes', undefined, params as Record<string, string | number | undefined>);
  }
  async getChange(id: number): Promise<Change> {
    return this.request<Change>('GET', `/changes/${id}`);
  }

  // ---------- CMDB ----------
  async listCIs(params?: PaginationParams & { type?: string; status?: string }): Promise<CIListResponse> {
    return this.request<CIListResponse>('GET', '/cmdb/cis', undefined, params as Record<string, string | number | undefined>);
  }
  async getCI(id: number): Promise<CI> {
    return this.request<CI>('GET', `/cmdb/cis/${id}`);
  }

  // ---------- Knowledge ----------
  async listKnowledge(params?: PaginationParams & { category?: string }): Promise<KnowledgeListResponse> {
    return this.request<KnowledgeListResponse>('GET', '/knowledge/articles', undefined, params as Record<string, string | number | undefined>);
  }
  async searchKnowledge(q: string): Promise<KnowledgeListResponse> {
    return this.request<KnowledgeListResponse>('GET', '/knowledge/articles', undefined, { search: q, pageSize: 20 });
  }
  async getKnowledgeArticle(id: number): Promise<KnowledgeArticle> {
    return this.request<KnowledgeArticle>('GET', `/knowledge/articles/${id}`);
  }

  // ---------- SLA ----------
  async getSLAStats(): Promise<Record<string, unknown>> {
    return this.request<Record<string, unknown>>('GET', '/sla/stats');
  }
  async getSLAOverdue(): Promise<Ticket[]> {
    return this.request<Ticket[]>('GET', '/sla/overdue');
  }

  // ---------- Workflow ----------
  async listProcessInstances(params?: PaginationParams): Promise<ProcessInstanceListResponse> {
    const result = await this.request<BackendListResponse<ProcessInstance>>(
      'GET',
      '/bpmn/process-instances',
      undefined,
      params as Record<string, string | number | undefined>,
    );
    return { items: result.data, ...result.pagination };
  }
  async getProcessInstance(id: string): Promise<ProcessInstance> {
    return this.request<ProcessInstance>('GET', `/bpmn/process-instances/${encodeURIComponent(id)}`);
  }
  async listUserTasks(): Promise<ApprovalTaskListResponse> {
    const result = await this.request<BackendListResponse<ApprovalTask>>('GET', '/bpmn/tasks');
    return { items: result.data, ...result.pagination };
  }
  async completeTask(id: string, outcome: string, comment?: string): Promise<void> {
    await this.request<unknown>(
      'PUT',
      `/bpmn/tasks/${encodeURIComponent(id)}/complete`,
      { variables: { outcome }, comment },
    );
  }

  async submitTaskDecision(
    id: string,
    action: 'approve' | 'reject',
    comment?: string,
  ): Promise<void> {
    await this.request<unknown>(
      'POST',
      `/bpmn/tasks/${encodeURIComponent(id)}/decisions`,
      { action, comment },
    );
  }

  // ---------- Notifications ----------
  async listNotifications(params?: PaginationParams & { unread?: boolean }): Promise<NotificationListResponse> {
    return this.request<NotificationListResponse>('GET', '/notifications', undefined, params as unknown as Record<string, string | number | boolean | undefined>);
  }
  async markNotificationRead(id: number): Promise<Record<string, unknown>> {
    return this.request<Record<string, unknown>>('POST', `/notifications/${id}/read`);
  }

  // ---------- Connectors / IM / Plugin market ----------
  async listConnectors(): Promise<{ items: ConnectorManifest[]; total: number }> {
    return this.request<{ items: ConnectorManifest[]; total: number }>('GET', '/connectors');
  }
  async listConnectorConfigs(): Promise<{ items: unknown[]; total: number }> {
    return this.request<{ items: unknown[]; total: number }>('GET', '/connectors/configs');
  }
  async provisionConnector(cfg: { name: string; provider: string; enabled: boolean; credentials: Record<string, string>; settings: Record<string, unknown> }): Promise<Record<string, unknown>> {
    return this.request<Record<string, unknown>>('POST', '/connectors/configs', cfg);
  }
  async testConnector(name: string): Promise<Record<string, unknown>> {
    return this.request<Record<string, unknown>>('POST', `/connectors/${name}/test`);
  }
  async sendViaConnector(name: string, payload: { channel: string; type: string; title?: string; content: string; card?: unknown }): Promise<Record<string, unknown>> {
    return this.request<Record<string, unknown>>('POST', `/connectors/${name}/send`, payload);
  }
  async connectorHealth(): Promise<Record<string, unknown>> {
    return this.request<Record<string, unknown>>('GET', '/connectors/health');
  }

  // ---------- Dashboard ----------
  async getDashboardOverview(): Promise<Record<string, unknown>> {
    return this.request<Record<string, unknown>>('GET', '/dashboard/overview');
  }
}

export const apiClient = new ApiClient();
