"""Disposable HTTPS owner-API fixture for the Android smoke test. No production data."""
import argparse
import json
import ssl
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit, parse_qs

TOKEN = 'a' * 43
CSRF = 'b' * 43
PROJECT = dict(slug='atlas', name='Atlas', tier='focus', hours_wk=8, goal='Ship the next milestone', description='A fictional test project', type='', deadline='', needs_me='', automate='', stack='')
ENTRIES = []
MESSAGES = [dict(id='1', handoff_id='1', body='Review the Atlas plan', target='', work_state='draft', delivery_state='unseen', source='owner', created_at='2026-09-06T10:00:00Z', files=[])]
HANDOFF = dict(id='1', title='Atlas handoff', description='A fictional handoff', scope='Planning', project_slug='atlas', project_name='Atlas', updated_at='2026-09-06T10:00:00Z')
EVENT = dict(id='event-1', calendar_id='calendar-1', calendar_name='Planning', title='Plan the week', start='2026-09-06T10:00:00Z', end='2026-09-06T11:00:00Z', all_day=False, recurring=False, etag='"v1"')

class Handler(BaseHTTPRequestHandler):
    entry_attempts = 0
    def log_message(self, *_):
        pass

    def send_json(self, status, value=None, headers=None):
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        for key, value_ in (headers or {}).items():
            self.send_header(key, value_)
        body = json.dumps(value).encode() if value is not None else b''
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def handle_api(self):
        path = urlsplit(self.path).path.removeprefix('/admin/api')
        method = self.command
        size = int(self.headers.get('Content-Length', '0'))
        if size > 200_000:
            return self.send_json(413, {'error': 'Too large'})
        body = json.loads(self.rfile.read(size)) if size else {}
        if method != 'GET' and self.headers.get('Origin') != 'https://localhost:8443':
            return self.send_json(403, {'error': 'Wrong Origin'})
        if path == '/login':
            if body.get('password') != 'fixture-password':
                return self.send_json(401, {'error': 'Invalid credentials'})
            return self.send_json(200, {'csrf_token': CSRF, 'expires_at': '2030-01-01T00:00:00Z'}, {'Set-Cookie': f'ledger_admin_session={TOKEN}; Path=/admin; HttpOnly; Secure; SameSite=Strict'})
        if self.headers.get('Cookie') != f'ledger_admin_session={TOKEN}':
            return self.send_json(401, {'error': 'Missing session'})
        if method != 'GET' and self.headers.get('X-CSRF-Token') != CSRF:
            return self.send_json(403, {'error': 'Missing CSRF token'})
        if path == '/redirect-test':
            return self.send_json(302, {}, {'Location': 'https://localhost:8443/leaked'})
        if path == '/leaked':
            raise AssertionError('The client followed a credentialed redirect')
        if path == '/large-export':
            self.send_response(200)
            self.send_header('Content-Type', 'text/markdown')
            self.send_header('Content-Length', str(28 * 1024 * 1024))
            self.end_headers()
            chunk = b'x' * (64 * 1024)
            for _ in range(448):
                self.wfile.write(chunk)
            return
        if path == '/handoffs/large':
            count = int(parse_qs(urlsplit(self.path).query).get('messages', ['50'])[0])
            if not 1 <= count <= 100:
                return self.send_json(400, {'error': 'messages must be between 1 and 100'})
            messages = [dict(MESSAGES[0], id=str(i + 1), body='\x01' * 100000) for i in range(count)]
            return self.send_json(200, {'handoff': HANDOFF, 'messages': messages})
        if path == '/logout':
            return self.send_json(204)
        if path == '/session':
            return self.send_json(200, {'authenticated': True, 'csrf_token': CSRF})
        if path == '/overview':
            return self.send_json(200, {'counts': {'projects': 1, 'entries': len(ENTRIES), 'oauth_clients': 1}, 'projects': [PROJECT], 'recent_entries': ENTRIES})
        if path == '/projects':
            return self.send_json(200, {'projects': [PROJECT]})
        if path == '/projects/atlas':
            if method == 'PUT':
                PROJECT.update(body)
                return self.send_json(200, PROJECT)
            return self.send_json(200, {'project': PROJECT, 'entries': ENTRIES})
        if path == '/projects/atlas/entries':
            Handler.entry_attempts += 1
            if Handler.entry_attempts == 1:
                return self.send_json(503, {'error': 'temporary fixture failure'})
            if Handler.entry_attempts == 2:
                return self.send_json(401, {'error': 'session expired'})
            entry = dict(id=str(len(ENTRIES) + 1), slug='atlas', project_name='Atlas', kind=body['kind'], body=body['body'], source='owner', created_at='2026-09-06T12:00:00Z')
            ENTRIES.insert(0, entry)
            return self.send_json(201, entry)
        if path.endswith('/files'):
            return self.send_json(200, {'files': []})
        if path == '/search':
            if not 1 <= body.get('limit', 10) <= 30:
                return self.send_json(400, {'error': 'limit must be between 1 and 30'})
            return self.send_json(200, {'hits': [dict(ref='project:atlas', kind='project', project_slug='atlas', project_name='Atlas search result', snippet=PROJECT['goal'])], 'degraded': []})
        if path == '/handoffs':
            return self.send_json(200, {'handoffs': [HANDOFF]})
        if path == '/handoffs/1':
            if method == 'PUT':
                HANDOFF.update(body)
            return self.send_json(200, {'handoff': HANDOFF, 'messages': MESSAGES})
        if path == '/handoff-messages/1/actions':
            if body['action'] == 'retarget' and (MESSAGES[0]['work_state'] not in ('draft', 'ready') or len(body.get('target', '')) > 100):
                return self.send_json(400, {'error': 'invalid retarget'})
            actions = {'publish': 'ready', 'claim': 'in_progress', 'block': 'blocked', 'complete': 'done', 'release': 'ready', 'reopen': 'ready'}
            if body['action'] in actions:
                MESSAGES[0]['work_state'] = actions[body['action']]
            if body['action'] == 'acknowledge':
                MESSAGES[0]['delivery_state'] = 'seen'
            return self.send_json(200, MESSAGES[0])
        if path == '/calendar/connection':
            return self.send_json(200, {'connected': True, 'server_url': 'https://cloud.example.com', 'username': 'Atlas owner', 'selected_calendars': 1})
        if path == '/calendar/calendars':
            return self.send_json(200, {'calendars': [dict(id='calendar-1', name='Planning', selected=True)]})
        if path == '/calendar/events':
            return self.send_json(200, {'events': [EVENT]})
        if path == '/calendar/events/event-1':
            if method == 'PUT':
                if body['etag'] != EVENT['etag']:
                    return self.send_json(412, {'error': 'Event changed. Reload it.'})
                EVENT.update(body)
                EVENT['etag'] = '"v2"'
            return self.send_json(200, EVENT)
        if path == '/oauth/clients':
            return self.send_json(200, {'clients': [dict(client_id='fixture-client', client_name='Atlas CLI', kind='device', redirect_uris=[], active_access_tokens=1, active_refresh_tokens=1)]})
        if path == '/oauth/revoke':
            return self.send_json(200, {'revoked': 2})
        return self.send_json(404, {'error': 'Fixture route not found'})

    do_GET = handle_api
    do_POST = handle_api
    do_PUT = handle_api
    do_DELETE = handle_api

if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--cert', required=True)
    parser.add_argument('--key', required=True)
    args = parser.parse_args()
    server = ThreadingHTTPServer(('127.0.0.1', 8443), Handler)
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.load_cert_chain(args.cert, args.key)
    server.socket = tls.wrap_socket(server.socket, server_side=True)
    server.serve_forever()
