import * as fs from 'fs';
import * as path from 'path';
import * as os from 'os';
const CREDENTIALS_DIR = process.env.ITSM_CREDENTIALS_DIR || path.join(os.homedir(), '.itsm');
const CREDENTIALS_FILE = path.join(CREDENTIALS_DIR, 'credentials');
export function saveCredentials(cred) {
    if (!fs.existsSync(CREDENTIALS_DIR)) {
        fs.mkdirSync(CREDENTIALS_DIR, { recursive: true, mode: 0o700 });
    }
    fs.chmodSync(CREDENTIALS_DIR, 0o700);
    fs.writeFileSync(CREDENTIALS_FILE, JSON.stringify(cred, null, 2), { encoding: 'utf-8', mode: 0o600 });
    fs.chmodSync(CREDENTIALS_FILE, 0o600);
}
export function loadCredentials() {
    try {
        if (!fs.existsSync(CREDENTIALS_FILE))
            return null;
        const raw = fs.readFileSync(CREDENTIALS_FILE, 'utf-8');
        const parsed = JSON.parse(raw);
        if (typeof parsed.cookieHeader !== 'string' || !parsed.cookieHeader)
            return null;
        return parsed;
    }
    catch {
        return null;
    }
}
export function clearCredentials() {
    try {
        if (fs.existsSync(CREDENTIALS_FILE)) {
            fs.unlinkSync(CREDENTIALS_FILE);
        }
    }
    catch {
        // ignore
    }
}
export function isLoggedIn() {
    const cred = loadCredentials();
    return Boolean(cred?.cookieHeader);
}
