FROM python:3.11-slim

WORKDIR /srv

ENV DEBIAN_FRONTEND=noninteractive
ENV HF_HOME=/models

# Install system dependencies and Git
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
       git \
       ca-certificates \
       libsndfile1 \
       espeak-ng \
    && rm -rf /var/lib/apt/lists/*

# Copy dependencies
COPY requirements.txt .

# Install Python packages
RUN --mount=type=cache,target=/root/.cache/pip \
    pip install \
    --index-url https://pypi.tuna.tsinghua.edu.cn/simple \
    --timeout 120 \
    --retries 5 \
    -r requirements.txt

# Copy application
COPY . .

EXPOSE 8200

CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8200"]