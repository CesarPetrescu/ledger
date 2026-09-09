"""CI-only external speech/Nextcloud contracts and a transparent MCP fault proxy.

Ledger itself is never mocked. Normal MCP requests are forwarded verbatim to
production nginx. One armed append response may be dropped AFTER the real
backend completes it, reproducing a lost acknowledgement without faking a write.
"""
import base64
import email.parser
import email.policy
import html
import http.client
import io
import json
import os
import socket
import ssl
import threading
import wave
import xml.etree.ElementTree as ET
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlsplit

ORIGIN = "https://glass-provider:9444"
TOKEN = os.environ["GLASS_PROVIDER_TOKEN"]
TRANSCRIPT = "Amber telescope voice capture for Atlas."
LOCK = threading.Lock()
STATE = {"drop_next_append": False, "dropped": 0, "speech_requests": [], "caldav_queries": [], "errors": []}
ROOT = "/remote.php/dav/calendars/fixture/"


def dav_row(path, props):
    return f'<d:response><d:href>{html.escape(path)}</d:href><d:propstat><d:prop>{props}</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>'


def multistatus(rows):
    return '<?xml version="1.0"?><d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">' + ''.join(rows) + '</d:multistatus>'


def event(uid, title, start, end, date=False, tz=None):
    suffix = ';VALUE=DATE' if date else f';TZID={tz}' if tz else ''
    return '\r\n'.join(['BEGIN:VCALENDAR', 'VERSION:2.0', 'PRODID:-//Ledger CI external boundary//EN', 'BEGIN:VEVENT', f'UID:{uid}', 'DTSTAMP:20260901T000000Z', f'DTSTART{suffix}:{start}', f'DTEND{suffix}:{end}', f'SUMMARY:{title}', 'LOCATION:Fixture room', 'END:VEVENT', 'END:VCALENDAR', ''])


def fixtures():
    tomorrow = datetime.now(timezone.utc).date() + timedelta(days=1)
    next_day = tomorrow + timedelta(days=1)
    return [
        ('utc.ics', event('utc@fixture', 'UTC midnight boundary', tomorrow.strftime('%Y%m%d')+'T233000Z', next_day.strftime('%Y%m%d')+'T003000Z')),
        ('bucharest.ics', event('bucharest@fixture', 'Bucharest same instant', next_day.strftime('%Y%m%d')+'T023000', next_day.strftime('%Y%m%d')+'T033000', tz='Europe/Bucharest')),
        ('all-day.ics', event('day@fixture', 'All-day planning', tomorrow.strftime('%Y%m%d'), next_day.strftime('%Y%m%d'), date=True)),
    ]


class Handler(BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'

    def log_message(self, *_):
        pass  # Never log request headers, tokens, audio, or private bodies.

    def send(self, status, data=b'', content_type='application/json', headers=None):
        if isinstance(data, str):
            data = data.encode()
        self.send_response(status)
        for key, value in (headers or {}).items():
            if key.lower() not in ('connection', 'content-length', 'transfer-encoding', 'content-encoding', 'content-type'):
                self.send_header(key, value)
        self.send_header('Content-Type', content_type)
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def read_body(self):
        length = int(self.headers.get('Content-Length', '0'))
        if length < 0 or length > 2_000_000:
            raise ValueError('Body outside test contract bounds')
        return self.rfile.read(length)

    def json(self, status, data):
        self.send(status, json.dumps(data).encode())

    def proxy(self, body):
        connection = http.client.HTTPConnection('nginx', 8080, timeout=35)
        try:
            headers = {k: v for k, v in self.headers.items() if k.lower() not in ('host', 'connection', 'transfer-encoding', 'content-length')}
            headers['Host'] = 'localhost:8443'
            connection.request(self.command, '/mcp', body=body, headers=headers)
            response = connection.getresponse()
            payload = response.read(4_000_001)
            if len(payload) > 4_000_000:
                raise ValueError('Oversized MCP response')
            is_append = False
            try:
                request = json.loads(body)
                is_append = request.get('method') == 'tools/call' and request.get('params', {}).get('name') == 'append_entry'
            except (ValueError, AttributeError):
                pass
            with LOCK:
                drop = STATE['drop_next_append'] and is_append and response.status == 200
                if drop:
                    STATE['drop_next_append'] = False
                    STATE['dropped'] += 1
            if drop:
                self.close_connection = True
                self.connection.shutdown(socket.SHUT_RDWR)
                self.connection.close()
                return
            self.send(response.status, payload, response.getheader('Content-Type', 'application/json'), dict(response.getheaders()))
        except (OSError, http.client.HTTPException) as exc:
            self.json(502, {'error': 'real upstream unavailable'})
        finally:
            connection.close()

    def handle_request(self):
        path = urlsplit(self.path).path
        body = self.read_body()
        if path == '/mcp':
            return self.proxy(body)
        if path == '/control':
            if self.headers.get('Authorization') != 'Bearer '+TOKEN:
                return self.json(401, {'error': 'unauthorized'})
            with LOCK:
                if self.command == 'POST':
                    change = json.loads(body)
                    if set(change) != {'drop_next_append'} or change['drop_next_append'] is not True:
                        return self.json(400, {'error': 'unknown fault'})
                    STATE['drop_next_append'] = True
                receipt = dict(STATE)
            return self.json(200, receipt)
        if path == '/v1/audio/transcriptions':
            if self.headers.get('Authorization') != 'Bearer '+TOKEN:
                return self.json(401, {'error': 'unauthorized'})
            message = email.parser.BytesParser(policy=email.policy.default).parsebytes(b'Content-Type: '+self.headers.get('Content-Type', '').encode()+b'\r\nMIME-Version: 1.0\r\n\r\n'+body)
            fields = {part.get_param('name', header='content-disposition'): part.get_payload(decode=True) for part in message.iter_parts()}
            with wave.open(io.BytesIO(fields['file'])) as wav:
                assert wav.getnchannels() == 1 and wav.getsampwidth() == 2 and wav.getframerate() == 16000
                assert 1600 <= wav.getnframes() <= 480000
                samples = wav.readframes(wav.getnframes())
            assert fields.get('model') == b'ci-boundary'
            with LOCK:
                STATE['speech_requests'].append({'pcm_bytes': len(samples), 'has_signal': any(samples), 'language': fields.get('language', b'').decode()})
            return self.json(200, {'text': TRANSCRIPT})
        if path == '/index.php/login/v2':
            return self.json(200, {'poll': {'token': TOKEN, 'endpoint': ORIGIN+'/login/v2/poll'}, 'login': ORIGIN+'/login/approve'})
        if path == '/login/v2/poll':
            if parse_qs(body.decode()).get('token') != [TOKEN]:
                return self.json(403, {'error': 'wrong login token'})
            return self.json(200, {'server': ORIGIN, 'loginName': 'fixture', 'appPassword': TOKEN})
        expected = 'Basic '+base64.b64encode(('fixture:'+TOKEN).encode()).decode()
        if self.headers.get('Authorization') != expected:
            return self.json(401, {'error': 'missing CalDAV credentials'})
        if self.command == 'PROPFIND':
            ET.fromstring(body)  # A malformed client request must not look successful.
            if path.rstrip('/') == '/remote.php/dav':
                rows = [dav_row(path, '<d:current-user-principal><d:href>/remote.php/dav/principals/users/fixture/</d:href></d:current-user-principal>')]
            elif path == '/remote.php/dav/principals/users/fixture/':
                rows = [dav_row(path, f'<c:calendar-home-set><d:href>{ROOT}</d:href></c:calendar-home-set>')]
            elif path == ROOT:
                rows = [dav_row(ROOT+name+'/', f'<d:resourcetype><d:collection/><c:calendar/></d:resourcetype><d:displayname>{name.title()}</d:displayname><c:supported-calendar-component-set><c:comp name="VEVENT"/></c:supported-calendar-component-set>') for name in ('work', 'hidden')]
            else:
                return self.json(404, {'error': 'unknown DAV path'})
            return self.send(207, multistatus(rows), 'application/xml')
        if self.command == 'REPORT' and path in (ROOT+'work/', ROOT+'hidden/'):
            request = ET.fromstring(body)
            ranges = request.findall('.//{urn:ietf:params:xml:ns:caldav}time-range')
            assert ranges and ranges[0].get('start') and ranges[0].get('end')
            with LOCK:
                STATE['caldav_queries'].append(path)
            items = fixtures() if path.endswith('/work/') else [('hidden.ics', event('hidden@fixture', 'Must not be shown', '20260910T120000Z', '20260910T130000Z'))]
            rows = [dav_row(path+name, '<d:getetag>"fixture-v1"</d:getetag><c:calendar-data>'+html.escape(ical)+'</c:calendar-data>') for name, ical in items]
            return self.send(207, multistatus(rows), 'application/xml')
        return self.json(404, {'error': 'provider fixture route not found'})

    def dispatch(self):
        try:
            self.handle_request()
        except Exception as exc:
            with LOCK:
                STATE['errors'].append(type(exc).__name__)
            try:
                self.json(400, {'error': 'external provider contract rejected request'})
            except OSError:
                pass

    do_GET = do_POST = do_DELETE = do_OPTIONS = do_PROPFIND = do_REPORT = dispatch


if __name__ == '__main__':
    server = ThreadingHTTPServer(('0.0.0.0', 9444), Handler)
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.load_cert_chain('/certs/localhost.crt', '/certs/localhost.key')
    server.socket = tls.wrap_socket(server.socket, server_side=True)
    server.serve_forever()
