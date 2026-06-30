"""Tests for the Python SDK using a local HTTP server (no network/deps)."""
import json
import threading
import unittest
from http.server import BaseHTTPRequestHandler, HTTPServer

from openfireblocks import OpenFireblocksClient, OpenFireblocksError

CAPTURED = {}


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):  # silence
        pass

    def _respond(self):
        CAPTURED["path"] = self.path
        CAPTURED["auth"] = self.headers.get("Authorization")
        if self.path == "/sign":
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(
                json.dumps(
                    {"requestId": "r1", "txHash": "0xhash", "status": "signed"}
                ).encode()
            )
        elif self.path.startswith("/deny"):
            self.send_response(403)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({"error": "policy denied"}).encode())
        else:
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b"{}")

    def do_GET(self):
        self._respond()

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        self.rfile.read(length)
        self._respond()


class SDKTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = HTTPServer(("127.0.0.1", 0), Handler)
        cls.port = cls.server.server_address[1]
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()

    def client(self):
        return OpenFireblocksClient(f"http://127.0.0.1:{self.port}", "k1")

    def test_sign_sends_auth_and_parses(self):
        res = self.client().sign(
            {"chainId": 11155111, "to": "0xabc", "gasLimit": 21000,
             "gasPrice": "1", "nonce": 0}
        )
        self.assertEqual(res["requestId"], "r1")
        self.assertEqual(res["status"], "signed")
        self.assertEqual(CAPTURED["auth"], "Bearer k1")

    def test_error_raises(self):
        c = OpenFireblocksClient(f"http://127.0.0.1:{self.port}", "k1")
        with self.assertRaises(OpenFireblocksError) as ctx:
            # the /deny path triggers a 403 from the test server
            c._request("GET", "/deny")
        self.assertEqual(ctx.exception.status, 403)
        self.assertEqual(ctx.exception.body, {"error": "policy denied"})

    def test_get_transaction_quotes_id(self):
        self.client().get_transaction("a b")
        self.assertEqual(CAPTURED["path"], "/transactions/a%20b")


if __name__ == "__main__":
    unittest.main()
