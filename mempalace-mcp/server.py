import os

import httpx
from mcp.server.fastmcp import FastMCP

MEMPALACE_URL = os.environ.get("MEMPALACE_URL", "http://mempalace-api:8000")
HOST = os.environ.get("MCP_HOST", "0.0.0.0")
PORT = int(os.environ.get("MCP_PORT", "8001"))

mcp = FastMCP("mempalace", host=HOST, port=PORT)


def _api(method: str, path: str, **kwargs) -> dict | list:
    with httpx.Client(base_url=MEMPALACE_URL, timeout=30) as client:
        r = getattr(client, method)(path, **kwargs)
        r.raise_for_status()
        return r.json()


@mcp.tool()
def store_drawer(wing: str, room: str, content: str) -> dict:
    """Store verbatim content in a drawer. Returns the new drawer ID."""
    return _api("post", "/drawers", json={"wing": wing, "room": room, "content": content})


@mcp.tool()
def search(query: str, wing: str | None = None, room: str | None = None, limit: int = 5) -> list:
    """Semantic search across stored drawers. Returns matching results ranked by relevance."""
    payload: dict = {"query": query, "limit": limit}
    if wing:
        payload["wing"] = wing
    if room:
        payload["room"] = room
    return _api("post", "/search", json=payload)


@mcp.tool()
def delete_drawer(drawer_id: str) -> dict:
    """Delete a drawer by ID."""
    return _api("delete", f"/drawers/{drawer_id}")


@mcp.tool()
def add_fact(subject: str, predicate: str, object: str, valid_from: str | None = None) -> dict:
    """Add a fact (triple) to the knowledge graph."""
    payload: dict = {"subject": subject, "predicate": predicate, "object": object}
    if valid_from:
        payload["valid_from"] = valid_from
    return _api("post", "/kg/facts", json=payload)


@mcp.tool()
def invalidate_fact(subject: str, predicate: str, object: str) -> dict:
    """Mark a knowledge graph fact as no longer true."""
    return _api("post", "/kg/facts/invalidate", json={
        "subject": subject, "predicate": predicate, "object": object,
    })


@mcp.tool()
def query_entity(entity: str, as_of: str | None = None, direction: str = "both") -> dict:
    """Look up all facts about an entity in the knowledge graph."""
    params: dict = {"entity": entity, "direction": direction}
    if as_of:
        params["as_of"] = as_of
    return _api("get", "/kg/query", params=params)


if __name__ == "__main__":
    mcp.run(transport="streamable-http")
