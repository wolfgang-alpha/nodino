import os

import httpx
from mcp.server.fastmcp import FastMCP

NODINO_URL = os.environ.get("NODINO_URL", "http://host.docker.internal:8085")
HOST = os.environ.get("MCP_HOST", "0.0.0.0")
PORT = int(os.environ.get("MCP_PORT", "8003"))

mcp = FastMCP("nodino", host=HOST, port=PORT)


def _api(method: str, path: str, **kwargs):
    with httpx.Client(base_url=NODINO_URL, timeout=30) as client:
        r = getattr(client, method)(path, **kwargs)
        r.raise_for_status()
        return r.json()


@mcp.tool()
def list_tasks(limit: int = 50) -> list:
    """List all tasks from the kanban board."""
    return _api("get", "/api/knots", params={"type": "task", "limit": limit})


@mcp.tool()
def create_task(content: str, importance: int = 3) -> dict:
    """Create a new task. Importance is 1-5 (1=trivial, 5=urgent). Starts as todo."""
    return _api("post", "/api/knots", json={
        "content": content, "type": "task", "importance": importance,
    })


@mcp.tool()
def update_status(task_id: str, status: str) -> dict:
    """Change a task's status. Valid statuses: todo, in_progress, waiting, done."""
    return _api("put", "/api/knots", json={"id": task_id, "status": status})


@mcp.tool()
def delete_task(task_id: str) -> dict:
    """Delete a task by its drawer ID."""
    return _api("delete", f"/api/knots", params={"id": task_id})


if __name__ == "__main__":
    mcp.run(transport="streamable-http")
