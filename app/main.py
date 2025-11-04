from __future__ import annotations
import os
from typing import Optional, List

import docker
from fastapi import FastAPI, Request, Depends, HTTPException, Response
from fastapi.responses import HTMLResponse, RedirectResponse, PlainTextResponse
from fastapi.templating import Jinja2Templates

from prometheus_fastapi_instrumentator import Instrumentator
from pydantic import BaseModel

app = FastAPI(title="DockTiles")
Instrumentator().instrument(app).expose(app)

TEMPLATES = Jinja2Templates(directory="app/templates")

# ---- Auth (optional, lightweight JWT check) ----
JWT_SECRET = os.getenv("DOCKTILES_JWT_SECRET")
ALLOW_ACTIONS = os.getenv("DOCKTILES_ALLOW_ACTIONS", "true").lower() == "true"

async def require_auth(request: Request):
    if not JWT_SECRET:
        return  # auth disabled
    auth = request.headers.get("authorization") or request.headers.get("Authorization")
    if not auth or not auth.lower().startswith("bearer "):
        raise HTTPException(status_code=401, detail="Missing bearer token")
    token = auth.split(" ", 1)[1]
    # In a real app, verify signature/claims. For now, token must equal secret.
    if token != JWT_SECRET:
        raise HTTPException(status_code=403, detail="Invalid token")

# ---- Docker client ----
# Uses local socket by default; honors DOCKER_HOST, DOCKER_TLS_VERIFY, DOCKER_CERT_PATH
_docker_client = docker.from_env()

# Ordered list of port hints (as strings) that likely host a web UI
PORT_HINTS = [p.strip() for p in os.getenv("DOCKTILES_HTTP_SCHEMES", "8080,3000,80,5000,8000,8888,9090").split(",") if p.strip()]

class PortInfo(BaseModel):
    host: str
    port: int

class ContainerDTO(BaseModel):
    id: str
    name: str
    image: str
    status: str
    state: str
    ports: List[PortInfo] = []

# ---- Helpers ----

def _extract_published_ports(c) -> List[PortInfo]:
    ports = []
    net = c.attrs.get("NetworkSettings", {})
    port_map = net.get("Ports", {}) or {}
    for container_port, bindings in port_map.items():  # e.g. '80/tcp': [{'HostIp': '0.0.0.0', 'HostPort': '8080'}]
        if not bindings:
            continue
        for b in bindings:
            host = b.get("HostIp", "127.0.0.1")
            try:
                port = int(b.get("HostPort"))
            except (TypeError, ValueError):
                continue
            ports.append(PortInfo(host=host, port=port))
    return ports


def _dto(c) -> ContainerDTO:
    c.reload()
    state = c.attrs.get("State", {}).get("Status", "unknown")
    image = c.attrs.get("Config", {}).get("Image", c.image.tags[0] if c.image.tags else c.image.short_id)
    return ContainerDTO(
        id=c.short_id,
        name=c.name,
        image=image,
        status=c.status,
        state=state,
        ports=_extract_published_ports(c),
    )


def _find_first_http_port(ports: List[PortInfo]) -> Optional[PortInfo]:
    if not ports:
        return None
    # Prefer hinted ports first, then fallback to first published
    hinted = [p for hint in PORT_HINTS for p in ports if str(p.port) == hint]
    return hinted[0] if hinted else ports[0]

# ---- FastAPI app ----
app = FastAPI(title="DockTiles")

@app.get("/", response_class=HTMLResponse)
async def home(request: Request, _=Depends(require_auth)):
    containers = _docker_client.containers.list(all=True)
    items = [_dto(c) for c in containers]
    return TEMPLATES.TemplateResponse("index.html", {
        "request": request,
        "containers": items,
        "allow_actions": ALLOW_ACTIONS,
    })

@app.post("/containers/{name}/start")
async def start_container(name: str, _=Depends(require_auth)):
    if not ALLOW_ACTIONS:
        raise HTTPException(status_code=403, detail="Actions disabled")
    c = _docker_client.containers.get(name)
    c.start()
    return {"ok": True}

@app.post("/containers/{name}/stop")
async def stop_container(name: str, _=Depends(require_auth)):
    if not ALLOW_ACTIONS:
        raise HTTPException(status_code=403, detail="Actions disabled")
    c = _docker_client.containers.get(name)
    c.stop()
    return {"ok": True}

@app.post("/containers/{name}/restart")
async def restart_container(name: str, _=Depends(require_auth)):
    if not ALLOW_ACTIONS:
        raise HTTPException(status_code=403, detail="Actions disabled")
    c = _docker_client.containers.get(name)
    c.restart()
    return {"ok": True}

@app.get("/containers/{name}", response_class=HTMLResponse)
async def container_detail(name: str, request: Request, _=Depends(require_auth)):
    c = _docker_client.containers.get(name)
    dto = _dto(c)
    # Compute a best-effort UI link
    ui_href: Optional[str] = None
    http_port = _find_first_http_port(dto.ports)
    if http_port:
        # Try to use the Host header's hostname for nicer links (works behind reverse proxy)
        host = request.headers.get("host", f"{http_port.host}:{http_port.port}").split(":")[0]
        ui_href = f"http://{host}:{http_port.port}"
    return TEMPLATES.TemplateResponse("container.html", {
        "request": request,
        "container": dto,
        "ui_href": ui_href,
        "allow_actions": ALLOW_ACTIONS,
    })

@app.get("/containers/{name}/logs", response_class=PlainTextResponse)
async def container_logs(name: str, tail: int = 200, _=Depends(require_auth)):
    c = _docker_client.containers.get(name)
    logs = c.logs(tail=tail).decode(errors="ignore")
    return logs

@app.get("/healthz")
async def healthz():
    # Simple health check
    try:
        _docker_client.ping()
        return {"ok": True}
    except Exception as e:
        return Response(status_code=500, content=str(e))