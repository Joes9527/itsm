import {test, expect} from '@playwright/test';
import {mkdtempSync, writeFileSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import path from 'node:path';
import {matchesSSLVPNPostcheck} from '../sslvpn-postcheck.test-utils';

const result = {verifiedAt: '2026-09-07T14:06:23.182049Z', expiresAt: '2026-10-07T14:06:23.182049Z'};
const current = {passed: true, workItemId: 13, beforeVerifiedAt: '2026-09-07 14:06:23.182049+00:00', beforeExpiresAt: '2026-10-07 14:06:23.182049+00:00', afterVerifiedAt: result.verifiedAt, afterExpiresAt: result.expiresAt};
const cases: [string, unknown, boolean][] = [
 ['current original request', current, true],
 ['same instant in another timezone', {...current, beforeVerifiedAt: '2026-09-07T22:06:23.182049+08:00'}, true],
 ['prior passed request', {...current, workItemId: 12}, false],
 ['missing identity', {...current, workItemId: undefined}, false],
 ['string identity', {...current, workItemId: '13'}, false],
 ['changed original verification by one microsecond', {...current, beforeVerifiedAt: '2026-09-07T14:06:23.182050Z'}, false],
 ['changed replay expiry', {...current, afterExpiresAt: '2026-10-08T14:06:23.182049Z'}, false],
 ['missing original time', {...current, beforeVerifiedAt: undefined}, false],
 ['invalid timestamp', {...current, beforeExpiresAt: 'not-a-date'}, false],
 ['failed evidence', {...current, passed: false}, false],
 ['null evidence', null, false],
];
for (const [name, evidence, accepted] of cases) {
 test(name, () => {
  const dir = mkdtempSync(path.join(tmpdir(), 'sslvpn-postcheck-'));
  try {
   const file = path.join(dir, 'postcheck.json');
   writeFileSync(file, JSON.stringify(evidence));
   expect(matchesSSLVPNPostcheck(file, 13, result)).toBe(accepted);
  } finally {rmSync(dir, {recursive: true, force: true});}
 });
}
test('unavailable or incomplete file fails closed', () => {
 const dir = mkdtempSync(path.join(tmpdir(), 'sslvpn-postcheck-'));
 try {
  const file = path.join(dir, 'postcheck.json');
  expect(matchesSSLVPNPostcheck(file, 13, result)).toBe(false);
  writeFileSync(file, '{');
  expect(matchesSSLVPNPostcheck(file, 13, result)).toBe(false);
 } finally {rmSync(dir, {recursive: true, force: true});}
});
