"""Run the real smoke script against bounded local HTTP fixtures."""
import json
import os
from pathlib import Path
import secrets
import subprocess
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SCRIPT = Path(__file__).resolve().parents[1] / 'smoke-test.sh'


class SmokeFrontendRedirectTests(unittest.TestCase):
    def run_smoke(self, target):
        visits = []
        token = secrets.token_hex(24)

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_POST(self):
                self.rfile.read(int(self.headers.get('Content-Length', 0)))
                self.send_response(200)
                self.send_header('Set-Cookie', 'access_token=' + token + '; Path=/; HttpOnly')
                self.send_header('Set-Cookie', 'refresh_token=' + token + '; Path=/; HttpOnly')
                self.end_headers()
                self.wfile.write(json.dumps({'data': {'user': {'id': 1}}}).encode())

            def do_GET(self):
                visits.append(self.path)
                if self.path == '/':
                    self.send_response(307)
                    self.send_header('Location', target)
                    self.end_headers()
                    return
                if self.path == '/missing':
                    self.send_response(404)
                    self.end_headers()
                    return
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b'<html>Login page</html>' if self.path == '/login' else b'{"database":"ok"}')

        server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            url = 'http://127.0.0.1:' + str(server.server_port)
            result = subprocess.run(['bash', str(SCRIPT)], capture_output=True, text=True, timeout=10,
                env=dict(os.environ, ITSM_BACKEND_URL=url, ITSM_FRONTEND_URL=url,
                         ITSM_ADMIN_PASS=secrets.token_hex(24), MAX_RETRIES='1', RETRY_INTERVAL='0'))
            return result.returncode, visits
        finally:
            server.shutdown()
            server.server_close()
            thread.join()

    def test_root_redirect_must_reach_actual_login_page(self):
        code, visits = self.run_smoke('/login')
        self.assertEqual(code, 0)
        self.assertIn('/login', visits)

    def test_redirect_loop_fails_with_bounded_requests(self):
        code, visits = self.run_smoke('/')
        self.assertNotEqual(code, 0)
        self.assertLessEqual(visits.count('/'), 7)  # Readiness + initial GET + five redirects.

    def test_redirect_to_missing_page_fails(self):
        code, visits = self.run_smoke('/missing')
        self.assertNotEqual(code, 0)
        self.assertIn('/missing', visits)


if __name__ == '__main__':
    unittest.main()
