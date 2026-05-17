import os
import time
from urllib.parse import urlparse

from locust import HttpUser, between, task


def _float_env(name: str, default: float) -> float:
    value = os.getenv(name)
    if not value:
        return default
    return float(value)


class TrinoGatewayUser(HttpUser):
    wait_time = between(
        _float_env("XTRINODE_LOAD_WAIT_MIN", 0.5),
        _float_env("XTRINODE_LOAD_WAIT_MAX", 1.0),
    )

    def on_start(self) -> None:
        route = os.getenv("XTRINODE_ROUTE_HEADER", "local-trino-keda")
        user = os.getenv("XTRINODE_USER", "local-loadtest")
        self.headers = {
            "X-Trino-User": user,
            "X-Trino-XTrinode": route,
        }
        self.query = os.getenv("XTRINODE_QUERY", "SELECT count(*) FROM postgres.public.orders")
        self.query_timeout_seconds = int(os.getenv("XTRINODE_QUERY_TIMEOUT_SECONDS", "60"))

    @task
    def run_statement(self) -> None:
        payload = self._post_statement()
        if payload is None:
            return
        self._drain_statement(payload)

    def _post_statement(self) -> dict | None:
        with self.client.post(
            "/v1/statement",
            data=self.query,
            headers=self.headers,
            name="POST /v1/statement",
            catch_response=True,
        ) as response:
            if response.status_code != 200:
                response.failure(f"expected 200 from Trino statement, got {response.status_code}: {response.text}")
                return None
            try:
                payload = response.json()
            except ValueError as exc:
                response.failure(f"statement response was not JSON: {exc}")
                return None
            if payload.get("error"):
                response.failure(f"statement failed before drain: {payload['error']}")
                return None
            response.success()
            return payload

    def _drain_statement(self, payload: dict) -> None:
        deadline = time.monotonic() + self.query_timeout_seconds
        while True:
            state = _query_state(payload)
            if state == "FINISHED":
                return
            if state in {"FAILED", "CANCELED", "CANCELLED"}:
                raise RuntimeError(f"query ended with {state}: {payload.get('error')}")

            next_uri = payload.get("nextUri")
            if not next_uri:
                if state == "FINISHED":
                    return
                raise RuntimeError(f"query stopped without nextUri; state={state or 'unknown'}")
            if time.monotonic() > deadline:
                raise TimeoutError(f"query did not finish within {self.query_timeout_seconds}s")

            payload = self._get_next(next_uri)

    def _get_next(self, next_uri: str) -> dict:
        path = _path_from_next_uri(next_uri)
        with self.client.get(
            path,
            headers=self.headers,
            name="GET /v1/statement/[next]",
            catch_response=True,
        ) as response:
            if response.status_code != 200:
                response.failure(f"expected 200 from Trino nextUri, got {response.status_code}: {response.text}")
                raise RuntimeError("nextUri request failed")
            try:
                payload = response.json()
            except ValueError as exc:
                response.failure(f"nextUri response was not JSON: {exc}")
                raise
            if payload.get("error"):
                response.failure(f"query failed while draining: {payload['error']}")
                raise RuntimeError("query failed while draining")
            response.success()
            return payload


def _query_state(payload: dict) -> str:
    stats = payload.get("stats")
    if isinstance(stats, dict) and stats.get("state"):
        return str(stats["state"])
    if payload.get("state"):
        return str(payload["state"])
    return ""


def _path_from_next_uri(next_uri: str) -> str:
    parsed = urlparse(next_uri)
    path = parsed.path or next_uri
    if parsed.query:
        path = f"{path}?{parsed.query}"
    return path
