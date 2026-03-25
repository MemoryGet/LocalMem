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
        scope: str | None = None,
        kind: str | None = None,
        context_id: str | None = None,
        retention_tier: str | None = None,
        tags: list[str] | None = None,
        auto_extract: bool = False,
        visibility: str | None = None,
    ) -> dict[str, Any]:
        """Create a memory.

        Args:
            content: Memory content (required).
            metadata: Arbitrary key-value metadata.
            team_id: Override default team_id.
            embedding: Pre-computed embedding vector.
            scope: Logical scope / namespace for the memory.
            kind: Memory kind — "fact" | "observation" | "mental_model" | etc.
            context_id: Attach to an existing context node.
            retention_tier: "permanent" | "long_term" | "standard" | "short_term" | "ephemeral".
            tags: Tag names to attach (created automatically if not existing).
            auto_extract: Trigger async entity extraction after creation.
            visibility: "private" (default) | "team" | "public".
        """
        body: dict[str, Any] = {"content": content}
        if metadata:
            body["metadata"] = metadata
        body["team_id"] = team_id if team_id is not None else self.team_id
        if embedding:
            body["embedding"] = embedding
        if scope is not None:
            body["scope"] = scope
        if kind is not None:
            body["kind"] = kind
        if context_id is not None:
            body["context_id"] = context_id
        if retention_tier is not None:
            body["retention_tier"] = retention_tier
        if tags:
            body["tags"] = tags
        if auto_extract:
            body["auto_extract"] = True
        if visibility is not None:
            body["visibility"] = visibility
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
        filters: dict[str, Any] | None = None,
        detail_level: str | None = None,
        max_tokens: int | None = None,
        graph_enabled: bool | None = None,
        mmr_enabled: bool | None = None,
        mmr_lambda: float | None = None,
    ) -> dict[str, Any]:
        """Search memories via text and/or vector.

        Args:
            query: Text query for FTS5 + semantic search.
            embedding: Pre-computed query embedding (skips embed call).
            team_id: Override default team_id.
            limit: Max results to return (1-100).
            filters: Structured filter dict. Supported keys:
                scope (str), context_id (str), kind (str), tags (list[str]),
                retention_tier (str), min_strength (float), source_type (str),
                happened_after (str ISO8601), happened_before (str ISO8601),
                include_expired (bool).
            detail_level: Field verbosity — "abstract_only" | "summary" | "full" (default).
            max_tokens: Trim results to fit within token budget.
            graph_enabled: Override config graph retrieval toggle.
            mmr_enabled: Enable MMR diversity re-ranking (requires Qdrant).
            mmr_lambda: MMR relevance/diversity balance; 1.0=relevance, 0.0=diversity.
        """
        body: dict[str, Any] = {"limit": limit}
        if query:
            body["query"] = query
        if embedding:
            body["embedding"] = embedding
        body["team_id"] = team_id if team_id is not None else self.team_id
        if filters is not None:
            body["filters"] = filters
        if detail_level is not None:
            body["detail_level"] = detail_level
        if max_tokens is not None:
            body["max_tokens"] = max_tokens
        if graph_enabled is not None:
            body["graph_enabled"] = graph_enabled
        if mmr_enabled is not None:
            body["mmr_enabled"] = mmr_enabled
        if mmr_lambda is not None:
            body["mmr_lambda"] = mmr_lambda
        return self._post("/v1/retrieve", body)

    # -- Memory lifecycle --

    def reinforce(self, memory_id: str) -> dict[str, Any]:
        """Reinforce a memory (increase strength)."""
        return self._post(f"/v1/memories/{memory_id}/reinforce", {})

    def restore(self, memory_id: str) -> dict[str, Any]:
        """Restore a soft-deleted memory."""
        return self._post(f"/v1/memories/{memory_id}/restore", {})

    def extract(self, memory_id: str) -> dict[str, Any]:
        """Trigger explicit entity extraction for a memory."""
        return self._post(f"/v1/memories/{memory_id}/extract", {})

    # -- Reflect --

    def reflect(
        self,
        question: str,
        scope: str = "",
        team_id: str | None = None,
        max_rounds: int = 3,
        token_budget: int = 4096,
        auto_save: bool | None = None,
    ) -> dict[str, Any]:
        """Multi-round LLM reasoning over retrieved memories.

        Args:
            question: The question to reason about (maps to API field "question").
            scope: Limit retrieval to a specific scope.
            team_id: Override default team_id.
            max_rounds: Max LLM reasoning rounds (default 3).
            token_budget: Max total tokens consumed across all rounds.
            auto_save: Save final conclusion as a mental_model memory.
        """
        body: dict[str, Any] = {
            "question": question,
            "max_rounds": max_rounds,
            "token_budget": token_budget,
        }
        body["team_id"] = team_id if team_id is not None else self.team_id
        if scope:
            body["scope"] = scope
        if auto_save is not None:
            body["auto_save"] = auto_save
        return self._post("/v1/reflect", body)

    # -- Conversations --

    def ingest_conversation(
        self,
        messages: list[dict[str, Any]],
        context_path: str = "",
        scope: str = "",
        team_id: str | None = None,
    ) -> dict[str, Any]:
        """Batch-ingest a conversation as memories."""
        body: dict[str, Any] = {
            "messages": messages,
            "team_id": team_id if team_id is not None else self.team_id,
        }
        if context_path:
            body["context_path"] = context_path
        if scope:
            body["scope"] = scope
        return self._post("/v1/conversations", body)

    def get_conversation(self, context_id: str, limit: int = 50) -> dict[str, Any]:
        """Get conversation memories by context ID."""
        return self._get(f"/v1/conversations/{context_id}?limit={limit}")

    # -- Contexts --

    def create_context(
        self,
        path: str,
        name: str,
        context_type: str = "topic",
        scope: str = "",
        parent_id: str = "",
    ) -> dict[str, Any]:
        """Create a hierarchical context node."""
        body: dict[str, Any] = {"path": path, "name": name, "type": context_type}
        if scope:
            body["scope"] = scope
        if parent_id:
            body["parent_id"] = parent_id
        return self._post("/v1/contexts", body)

    def get_context(self, context_id: str) -> dict[str, Any]:
        """Get a context by ID."""
        return self._get(f"/v1/contexts/{context_id}")

    def list_contexts(self, parent_id: str = "", scope: str = "") -> dict[str, Any]:
        """List context children."""
        params = []
        if parent_id:
            params.append(f"parent_id={parent_id}")
        if scope:
            params.append(f"scope={scope}")
        qs = ("?" + "&".join(params)) if params else ""
        return self._get(f"/v1/contexts{qs}")

    def delete_context(self, context_id: str) -> dict[str, Any]:
        """Delete a context."""
        return self._delete(f"/v1/contexts/{context_id}")

    # -- Tags --

    def create_tag(self, name: str, scope: str = "") -> dict[str, Any]:
        """Create a tag."""
        body: dict[str, Any] = {"name": name}
        if scope:
            body["scope"] = scope
        return self._post("/v1/tags", body)

    def list_tags(self, scope: str = "") -> dict[str, Any]:
        """List tags."""
        qs = f"?scope={scope}" if scope else ""
        return self._get(f"/v1/tags{qs}")

    def add_memory_tag(self, memory_id: str, tag_id: str) -> dict[str, Any]:
        """Associate a tag with a memory."""
        return self._post(f"/v1/memories/{memory_id}/tags/{tag_id}", {})

    def remove_memory_tag(self, memory_id: str, tag_id: str) -> dict[str, Any]:
        """Remove a tag from a memory."""
        return self._delete(f"/v1/memories/{memory_id}/tags/{tag_id}")

    # -- Entities & Knowledge Graph --

    def create_entity(
        self, name: str, entity_type: str, scope: str = "", description: str = ""
    ) -> dict[str, Any]:
        """Create a knowledge graph entity."""
        body: dict[str, Any] = {"name": name, "entity_type": entity_type}
        if scope:
            body["scope"] = scope
        if description:
            body["description"] = description
        return self._post("/v1/entities", body)

    def list_entities(self, scope: str = "", entity_type: str = "") -> dict[str, Any]:
        """List entities, optionally filtered."""
        params = []
        if scope:
            params.append(f"scope={scope}")
        if entity_type:
            params.append(f"entity_type={entity_type}")
        qs = ("?" + "&".join(params)) if params else ""
        return self._get(f"/v1/entities{qs}")

    def create_relation(
        self, source_id: str, target_id: str, relation_type: str, scope: str = ""
    ) -> dict[str, Any]:
        """Create a relation between two entities."""
        body: dict[str, Any] = {
            "source_id": source_id,
            "target_id": target_id,
            "relation_type": relation_type,
        }
        if scope:
            body["scope"] = scope
        return self._post("/v1/entity-relations", body)

    def list_relations(self, entity_id: str = "", scope: str = "") -> dict[str, Any]:
        """List entity relations."""
        params = []
        if entity_id:
            params.append(f"entity_id={entity_id}")
        if scope:
            params.append(f"scope={scope}")
        qs = ("?" + "&".join(params)) if params else ""
        return self._get(f"/v1/entity-relations{qs}")

    def link_memory_entity(self, memory_id: str, entity_id: str) -> dict[str, Any]:
        """Associate an entity with a memory."""
        return self._post(f"/v1/memory-entities", {"memory_id": memory_id, "entity_id": entity_id})

    # -- Documents --

    def upload_document(
        self,
        filename: str,
        content: str,
        scope: str = "",
        team_id: str | None = None,
    ) -> dict[str, Any]:
        """Upload and process a document (chunked into memories)."""
        body: dict[str, Any] = {
            "filename": filename,
            "content": content,
            "team_id": team_id if team_id is not None else self.team_id,
        }
        if scope:
            body["scope"] = scope
        return self._post("/v1/documents", body)

    def list_documents(self, scope: str = "", team_id: str | None = None) -> dict[str, Any]:
        """List uploaded documents."""
        tid = team_id if team_id is not None else self.team_id
        params = [f"team_id={tid}"]
        if scope:
            params.append(f"scope={scope}")
        return self._get(f"/v1/documents?{'&'.join(params)}")

    # -- Maintenance --

    def cleanup(self) -> dict[str, Any]:
        """Trigger maintenance: expire soft-deleted memories."""
        return self._post("/v1/maintenance/cleanup", {})

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
