from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
import threading
import time


BACKEND = os.environ.get("BACKEND_NAME", "backend")
STATUS = int(os.environ.get("STATUS_CODE", "200"))
STATUS_SEQUENCE = [
    int(status.strip())
    for status in os.environ.get("STATUS_SEQUENCE", "").split(",")
    if status.strip()
]
DELAY_STATUS = os.environ.get("DELAY_STATUS_CODE", "")
DELAY_SECONDS = float(os.environ.get("DELAY_SECONDS", "0") or "0")
QUERY_ID = os.environ.get("QUERY_ID", "20260508_000000_00001_e2e01")
REQUEST_COUNT = 0
REQUEST_COUNT_LOCK = threading.Lock()


def next_status():
    global REQUEST_COUNT

    with REQUEST_COUNT_LOCK:
        REQUEST_COUNT += 1
        request_count = REQUEST_COUNT

    if not STATUS_SEQUENCE:
        return STATUS, request_count

    index = request_count - 1
    if index < len(STATUS_SEQUENCE):
        return STATUS_SEQUENCE[index], request_count
    return STATUS_SEQUENCE[-1], request_count


def status_for_request(headers):
    # Gateway active health checks call /v1/info directly without Trino headers.
    # Keep them healthy and outside scripted failure sequences so tests control
    # circuit-breaker state only through proxied client traffic.
    if STATUS_SEQUENCE and not headers.get("X-Trino-User"):
        return 200, REQUEST_COUNT
    return next_status()


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        return

    def send_json(self, status, payload):
        body = json.dumps(payload, separators=(",", ":")).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        status, request_count = status_for_request(self.headers)
        if DELAY_SECONDS > 0 and str(status) == DELAY_STATUS:
            time.sleep(DELAY_SECONDS)
        self.send_json(
            status,
            {
                "id": QUERY_ID,
                "stats": {"state": "FINISHED"},
                "backend": BACKEND,
                "path": self.path,
                "xff": self.headers.get("X-Forwarded-For", ""),
                "requestCount": request_count,
            },
        )

    def do_POST(self):
        status, request_count = status_for_request(self.headers)
        if DELAY_SECONDS > 0 and str(status) == DELAY_STATUS:
            time.sleep(DELAY_SECONDS)
        length = int(self.headers.get("Content-Length", "0") or "0")
        if length:
            self.rfile.read(length)
        self.send_json(
            status,
            {
                "id": QUERY_ID,
                "nextUri": "http://fake.invalid/v1/statement/queued/%s/1" % QUERY_ID,
                "stats": {"state": "RUNNING"},
                "backend": BACKEND,
                "path": self.path,
                "requestCount": request_count,
            },
        )


ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
