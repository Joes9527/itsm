import { prepareTicketEdit } from '../ticket-edit';
import { TicketApi } from '../ticket-api';
import { IncidentAPI } from '../incident-api';
import { httpClient } from '../http-client';

jest.mock('../http-client', () => ({ httpClient: { post: jest.fn(), put: jest.fn() } }));
jest.mock('../base-api-handler', () => ({ handleApiRequest: (value: unknown) => value }));

const receipt = { workItemId: 101, version: 4, status: 'in_progress', replayed: false };
const meta = { version: 3, operationId: 'confirmed-operation' };

beforeEach(() => {
  jest.clearAllMocks();
  (httpClient.post as jest.Mock).mockResolvedValue(receipt);
  (httpClient.put as jest.Mock).mockResolvedValue(receipt);
});

test('ticket editing refuses unversioned commands before sending HTTP', async () => {
  await expect(TicketApi.updateTicket(101, { title: 'Changed' } as never)).rejects.toThrow();
  expect(httpClient.put).not.toHaveBeenCalled();
});

test('ticket escalation sends the confirmed version and operation without inventing metadata', async () => {
  const body = { ...meta, reason: 'Needs specialist' };
  expect(await TicketApi.escalateTicket(101, body)).toEqual(receipt);
  expect(httpClient.post).toHaveBeenCalledWith('/api/v1/tickets/101/escalate', body);
});

test('incident acknowledgment sends the confirmed command identity', async () => {
  expect(await IncidentAPI.acknowledgeIncident(7, meta)).toEqual(receipt);
  expect(httpClient.post).toHaveBeenCalledWith('/api/v1/incidents/7/acknowledge', meta);
});

test('incident assignment sends a numeric assignee and command metadata at the top level', async () => {
  const body = { ...meta, assigneeId: 9, reason: 'Transfer to specialist' };
  expect(await IncidentAPI.assignIncident(7, body)).toEqual(receipt);
  expect(httpClient.post).toHaveBeenCalledWith('/api/v1/incidents/7/assign', body);
});

test('incident close sends the explicit reason and returns the command receipt', async () => {
  const body = { ...meta, reason: 'Requester verified restoration' };
  expect(await IncidentAPI.closeIncident(7, body)).toEqual(receipt);
  expect(httpClient.post).toHaveBeenCalledWith('/api/v1/incidents/7/close', body);
});

test('confirmed edit retains its snapshot when the visible version changes', () => {
  Object.defineProperty(crypto, 'randomUUID', {configurable:true,value:jest.fn().mockReturnValueOnce('first').mockReturnValue('second')});
  const first = prepareTicketEdit(undefined, {title:'Confirmed'}, 3);
  expect(prepareTicketEdit(first, {title:'Confirmed'}, 8)).toBe(first);
  expect(prepareTicketEdit(first, {title:'Changed'}, 8).payload).toEqual({title:'Changed',version:8,operationId:'second'});
});

test.each(['acknowledgeIncident','startIncident','reopenIncident'] as const)('%s rejects missing version before HTTP', async method => {
  await expect(IncidentAPI[method](7, {operationId:'intent'} as never)).rejects.toThrow();
  expect(httpClient.post).not.toHaveBeenCalled();
});
