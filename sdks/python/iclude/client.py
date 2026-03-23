"""IClude HTTP client for the memory API."""

from __future__ import annotations

import json
from typing import Any
from urllib import request as urllib_request
from urllib.error import HTTPError


class ICludeClient:
    """IClude API client wrapping v1 REST endpoints.

    Args:
        base_url: Server base URL, e.g. "http://localhost:8080".
        team_id: Optional default team_id for all requests.
    """

    def __init__(self, base_url: str = "http://localhost:8080", team_id: str = ""):
        self.base_url = base_url.rstrip("/")
        self.team_id = team_id

    # -- CRUD --

    def create(
        self,
        content: str,
        metadata: dict[str, Any] | None = None,
        team_id: str | None = None,
        embedding: list[float] | None = None,
    ) -> dict[str, Any]:
        """Create a memory."""
        body: dict[str, Any] = {"content": content}
        if metadata:
            body["metadata"] = metadata
        body["team_id"] = team_id if team_id is not None else self.team_id
        if embedding:
            body["embedding"] = embedding
        return self._post("/v1/memories", body)

    def get(self, memory_id: str) -> dict[str, Any]:
        """Get a memory by ID."""
        return self._get(f"/v1/memories/{memory_id}")

    def update(
        self,
        memory_id: str,
        content: str | None = None,
        metadata: dict[str, Any] | None = None,
        embedding: list[float] | None = None,
    ) -> dict[str, Any]:
        """Update a memory."""
        body: dict[str, Any] = {}
        if content is not None:
            body["content"] = content
        if metadata is not None:
            body["metadata"] = metadata
        if embedding is not None:
            body["embedding"] = embedding
        return self._put(f"/v1/memories/{memory_id}", body)

    def delete(self, memory_id: str) -> dict[str, Any]:
        """Delete a memory by ID."""
        return self._delete(f"/v1/memories/{memory_id}")

    def list(
        self,
        team_id: str | None = None,
        offset: int = 0,
        limit: int = 20,
    ) -> dict[str, Any]:
        """List memories with pagination."""
        tid = team_id if team_id is not None else self.team_id
        params = f"?team_id={tid}&offset={offset}&limit={limit}"
        return self._get(f"/v1/memories{params}")

    # -- Search --

    def retrieve(
        self,
        query: str = "",
        embedding: list[float] | None = None,
        team_id: str | None = None,
        limit: int = 10,
    ) -> dict[str, Any]:
        """Search memories via text and/or vector."""
        body: dict[str, Any] = {"limit": limit}
        if query:
            body["query"] = query
        if embedding:
            body["embedding"] = embedding
        body["team_id"] = team_id if team_id is not None else self.team_id
        return self._post("/v1/retrieve", body)

    # -- Health --

    def health(self) -> dict[str, Any]:
        """Check server health."""
        return self._get("/health")

    # -- Internal HTTP helpers (stdlib only, no requests dependency) --

    def _get(self, path: str) -> dict[str, Any]:
        return self._request("GET", path)

    def _post(self, path: str, body: dict[str, Any]) -> dict[str, Any]:
        return self._request("POST", path, body)

    def _put(self, path: str, body: dict[str, Any]) -> dict[str, Any]:
        return self._request("PUT", path, body)

    def _delete(self, path: str) -> dict[str, Any]:
        return self._request("DELETE", path)

    def _request(
        self, method: str, path: str, body: dict[str, Any] | None = None
    ) -> dict[str, Any]:
        url = f"{self.base_url}{path}"
        data = json.dumps(body).encode("utf-8") if body else None
        req = urllib_request.Request(url, data=data, method=method)
        req.add_header("Content-Type", "application/json")

        try:
            with urllib_request.urlopen(req) as resp:
                return json.loads(resp.read().decode("utf-8"))
        except HTTPError as e:
            error_body = e.read().decode("utf-8")
            try:
                return json.loads(error_body)
            except json.JSONDecodeError:
                raise RuntimeError(
                    f"IClude API error {e.code}: {error_body}"
                ) from e
