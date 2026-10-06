"""MegaMoot AI plane.

Stateless by design: it holds no business state, writes nothing to the
database, and decides nothing about an assessment's progress. It is given
bytes or text and returns a structured answer. The control plane validates and
persists whatever comes back.

For now it does one job, document extraction. Grading, judge agents and report
composition land here next and share the same contract style.
"""

from __future__ import annotations

import logging
import os
import secrets

from fastapi import Depends, FastAPI, File, Header, HTTPException, UploadFile

from .extract import MAX_BYTES, ExtractionFailed, UnsupportedFormat, extract

logging.basicConfig(
    level=os.getenv("LOG_LEVEL", "INFO").upper(),
    format='{"level":"%(levelname)s","logger":"%(name)s","msg":"%(message)s"}',
)
log = logging.getLogger("megamoot.ai")

SERVICE_TOKEN = os.getenv("AI_SERVICE_TOKEN", "")

app = FastAPI(title="MegaMoot AI plane", version="0.1.0", docs_url=None, redoc_url=None)


def require_token(authorization: str = Header(default="")) -> None:
    """Authenticate the control plane.

    The service also sits on a network with no route out, but a shared secret
    costs nothing and means a misconfigured network is not the only thing
    standing between an attacker and the parsers.
    """
    if not SERVICE_TOKEN:
        raise HTTPException(status_code=500, detail="service token is not configured")

    prefix = "Bearer "
    if not authorization.startswith(prefix):
        raise HTTPException(status_code=401, detail="missing bearer token")
    if not secrets.compare_digest(authorization[len(prefix) :], SERVICE_TOKEN):
        raise HTTPException(status_code=401, detail="invalid token")


@app.get("/healthz")
def healthz() -> dict:
    return {"status": "ok"}


@app.post("/v1/extract", dependencies=[Depends(require_token)])
async def extract_document(file: UploadFile = File(...)) -> dict:
    data = await file.read()

    if len(data) > MAX_BYTES:
        raise HTTPException(status_code=413, detail=f"document exceeds {MAX_BYTES} bytes")

    try:
        result = extract(data)
    except UnsupportedFormat as exc:
        raise HTTPException(status_code=415, detail=str(exc)) from exc
    except ExtractionFailed as exc:
        log.warning("extraction failed: %s", exc)
        raise HTTPException(status_code=422, detail=str(exc)) from exc

    log.info(
        "extracted %s, pages=%s chars=%d truncated=%s",
        result.format,
        result.pages,
        len(result.text),
        result.truncated,
    )
    return result.as_dict()
