import { clearCredentials, loadCredentials, saveCredentials } from './credentials.js';
const DEFAULT_BASE_URL = 'http://localhost:8090';
const MUTATION_METHODS = new Set(['POST', 'PUT', 'PATCH', 'DELETE']);
function responseSetCookies(headers) {
    const values = headers.getSetCookie?.();
    if (values?.length)
        return values;
    const combined = headers.get('set-cookie');
    return combined ? combined.split(/,(?=\s*[^;,]+=)/) : [];
}
function mergeCookieHeader(current, setCookies) {
    const cookies = new Map();
    for (const pair of current.split(';')) {
        const separator = pair.indexOf('=');
        if (separator > 0)
            cookies.set(pair.slice(0, separator).trim(), pair.slice(separator + 1).trim());
    }
    for (const setCookie of setCookies) {
        const pair = setCookie.split(';', 1)[0];
        const separator = pair.indexOf('=');
        if (separator <= 0)
            continue;
        const name = pair.slice(0, separator).trim();
        const value = pair.slice(separator + 1).trim();
        const expired = /(?:^|;)\s*max-age\s*=\s*(?:0|-\d+)/i.test(setCookie);
        if (!value || expired)
            cookies.delete(name);
        else
            cookies.set(name, value);
    }
    return [...cookies.entries()].map(([name, value]) => `${name}=${value}`).join('; ');
}
export class ApiClient {
    constructor(baseURL = DEFAULT_BASE_URL) {
        this.baseURL = baseURL;
    }
    persistResponseCookies(response, current) {
        const cookieHeader = mergeCookieHeader(current.cookieHeader, responseSetCookies(response.headers));
        const updated = { ...current, cookieHeader };
        if (cookieHeader)
            saveCredentials(updated);
        else
            clearCredentials();
        return updated;
    }
    async refreshSession() {
        const current = loadCredentials();
        if (!current)
            throw new Error('Not logged in');
        try {
            const response = await fetch(`${this.baseURL}/api/v1/auth/refresh`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', Cookie: current.cookieHeader },
                body: '{}',
            });
            if (!response.ok)
                throw new Error(`HTTP ${response.status}: ${response.statusText}`);
            const body = await response.json();
            if (body.code !== 0)
                throw new Error(body.message || 'Session refresh failed');
            const updated = this.persistResponseCookies(response, current);
            if (!/(?:^|;\s*)access_token=/.test(updated.cookieHeader)
                || !/(?:^|;\s*)refresh_token=/.test(updated.cookieHeader)) {
                throw new Error('Session refresh did not rotate authentication cookies');
            }
        }
        catch (error) {
            clearCredentials();
            throw error;
        }
    }
    async csrfToken(current) {
        const response = await fetch(`${this.baseURL}/api/v1/csrf-token`, {
            method: 'GET',
            headers: { Cookie: current.cookieHeader },
        });
        if (!response.ok)
            throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        const body = await response.json();
        if (body.code !== 0 || !body.data?.csrf_token)
            throw new Error(body.message || 'CSRF token unavailable');
        return { token: body.data.csrf_token, credentials: this.persistResponseCookies(response, current) };
    }
    async request(method, endpoint, data, params, retryAfterRefresh = true) {
        let credentials = loadCredentials();
        if (!credentials)
            throw new Error('Not logged in');
        const url = new URL(`${this.baseURL}/api/v1${endpoint}`);
        if (params) {
            Object.entries(params).forEach(([key, value]) => {
                if (value !== undefined)
                    url.searchParams.set(key, String(value));
            });
        }
        const headers = {
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
            return this.request(method, endpoint, data, params, false);
        }
        if (!response.ok)
            throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        const body = await response.json();
        if (body.code !== 0)
            throw new Error(body.message || 'API error');
        return body.data;
    }
    // ---------- Auth ----------
    async login(loginData) {
        const url = `${this.baseURL}/api/v1/auth/login`;
        const res = await fetch(url, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(loginData),
        });
        if (!res.ok)
            throw new Error(`HTTP ${res.status}`);
        const json = await res.json();
        if (json.code !== 0)
            throw new Error(json.message || 'Login failed');
        const cookieHeader = mergeCookieHeader('', responseSetCookies(res.headers));
        if (!/(?:^|;\s*)access_token=/.test(cookieHeader)
            || !/(?:^|;\s*)refresh_token=/.test(cookieHeader)) {
            throw new Error('Login did not establish the required cookie session');
        }
        const user = json.data.user;
        if (!user?.tenantId) {
            clearCredentials();
            throw new Error('Login response did not identify the selected tenant');
        }
        saveCredentials({ cookieHeader, user, tenantId: user.tenantId });
        let tenant;
        try {
            const context = await this.request('GET', '/auth/tenants');
            const selected = context.tenants.find(candidate => candidate.id === user.tenantId);
            if (!selected)
                throw new Error('Selected tenant is unavailable');
            tenant = selected;
        }
        catch (error) {
            clearCredentials();
            throw error;
        }
        const result = { user, tenant };
        saveCredentials({
            cookieHeader,
            user: result.user,
            tenantId: result.tenant.id,
            tenantName: result.tenant.name,
        });
        return result;
    }
    async logout() {
        try {
            await this.request('POST', '/auth/logout');
        }
        finally {
            clearCredentials();
        }
    }
    async getUserInfo() { return this.request('GET', '/auth/me'); }
    // ---------- Tickets ----------
    async listTickets(params) {
        return this.request('GET', '/tickets', undefined, params);
    }
    async getTicket(id) {
        return this.request('GET', `/tickets/${id}`);
    }
    async createTicket(data) {
        return this.request('POST', '/tickets', data);
    }
    async searchTickets(query, params) {
        return this.request('GET', '/tickets/search', undefined, { ...params, search: query });
    }
    async getOverdueTickets() {
        return this.request('GET', '/tickets/overdue');
    }
    async globalSearch(q, page) {
        return this.request('GET', '/search', undefined, { q, page });
    }
    // ---------- Incidents ----------
    async listIncidents(params) {
        return this.request('GET', '/incidents', undefined, params);
    }
    async getIncident(id) {
        return this.request('GET', `/incidents/${id}`);
    }
    async aiTriage(payload) {
        return this.request('POST', '/ai/triage', payload);
    }
    // ---------- Changes ----------
    async listChanges(params) {
        return this.request('GET', '/changes', undefined, params);
    }
    async getChange(id) {
        return this.request('GET', `/changes/${id}`);
    }
    // ---------- CMDB ----------
    async listCIs(params) {
        return this.request('GET', '/cmdb/cis', undefined, params);
    }
    async getCI(id) {
        return this.request('GET', `/cmdb/cis/${id}`);
    }
    // ---------- Knowledge ----------
    async listKnowledge(params) {
        return this.request('GET', '/knowledge/articles', undefined, params);
    }
    async searchKnowledge(q) {
        return this.request('GET', '/knowledge/articles', undefined, { search: q, pageSize: 20 });
    }
    async getKnowledgeArticle(id) {
        return this.request('GET', `/knowledge/articles/${id}`);
    }
    // ---------- SLA ----------
    async getSLAStats() {
        return this.request('GET', '/sla/stats');
    }
    async getSLAOverdue() {
        return this.request('GET', '/sla/overdue');
    }
    // ---------- Workflow ----------
    async listProcessInstances(params) {
        const result = await this.request('GET', '/bpmn/process-instances', undefined, params);
        return { items: result.data, ...result.pagination };
    }
    async getProcessInstance(id) {
        return this.request('GET', `/bpmn/process-instances/${encodeURIComponent(id)}`);
    }
    async listUserTasks() {
        const result = await this.request('GET', '/bpmn/tasks');
        return { items: result.data, ...result.pagination };
    }
    async completeTask(id, outcome, comment) {
        await this.request('PUT', `/bpmn/tasks/${encodeURIComponent(id)}/complete`, { variables: { outcome }, comment });
    }
    async submitTaskDecision(id, action, comment) {
        await this.request('POST', `/bpmn/tasks/${encodeURIComponent(id)}/decisions`, { action, comment });
    }
    // ---------- Notifications ----------
    async listNotifications(params) {
        return this.request('GET', '/notifications', undefined, params);
    }
    async markNotificationRead(id) {
        return this.request('POST', `/notifications/${id}/read`);
    }
    // ---------- Connectors / IM / Plugin market ----------
    async listConnectors() {
        return this.request('GET', '/connectors');
    }
    async listConnectorConfigs() {
        return this.request('GET', '/connectors/configs');
    }
    async provisionConnector(cfg) {
        return this.request('POST', '/connectors/configs', cfg);
    }
    async testConnector(name) {
        return this.request('POST', `/connectors/${name}/test`);
    }
    async sendViaConnector(name, payload) {
        return this.request('POST', `/connectors/${name}/send`, payload);
    }
    async connectorHealth() {
        return this.request('GET', '/connectors/health');
    }
    // ---------- Dashboard ----------
    async getDashboardOverview() {
        return this.request('GET', '/dashboard/overview');
    }
}
export const apiClient = new ApiClient();
