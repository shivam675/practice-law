FROM python:3.12-slim AS base

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_RETRIES=10 \
    PIP_DEFAULT_TIMEOUT=300

WORKDIR /srv

COPY requirements.txt ./
RUN --mount=type=cache,target=/root/.cache/pip \
    pip install \
    --index-url https://pypi.org/simple \
    --timeout 60 \
    --retries 3 \
    -r requirements.txt
COPY app ./app

# Runs unprivileged: the parsers in here read attacker-supplied binary formats.
RUN useradd --no-create-home --shell /usr/sbin/nologin aiplane
USER aiplane

EXPOSE 8100
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8100", "--no-server-header"]
