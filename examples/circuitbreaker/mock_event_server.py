#!/usr/bin/env python3
"""Mock pushgateway server: captures POST /polaris/client/events to a JSON-lines file.

Usage:
    python3 mock_event_server.py <events_file> <port>

Each POST body is expected to be {"batch": [{...}, {...}]}.
Events are appended one-per-line (JSON-lines format) to <events_file>.
"""
import http.server
import json
import sys


EVENTS_FILE = sys.argv[1] if len(sys.argv) > 1 else "/tmp/captured_events.json"
PORT = int(sys.argv[2]) if len(sys.argv) > 2 else 19090


class Handler(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        content_length = int(self.headers.get("Content-Length", 0))
        if content_length > 0 and "/polaris/client/events" in self.path:
            body = self.rfile.read(content_length)
            try:
                data = json.loads(body)
                events = data.get("batch", [])
                with open(EVENTS_FILE, "a") as f:
                    for evt in events:
                        f.write(json.dumps(evt, ensure_ascii=False) + "\n")
            except (json.JSONDecodeError, IOError):
                pass
        self.send_response(200)
        self.end_headers()

    def log_message(self, format, *args):
        pass  # suppress access logs to avoid polluting test output


if __name__ == "__main__":
    httpd = http.server.HTTPServer(("127.0.0.1", PORT), Handler)
    print(f"mock_event_server listening on :{PORT}, events -> {EVENTS_FILE}", flush=True)
    httpd.serve_forever()
