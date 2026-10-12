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

# Override with a nearer mirror if PyPI is slow from the build host.
ARG PIP_INDEX_URL=https://pypi.org/simple
RUN --mount=type=cache,target=/root/.cache/pip \
    pip install \
    --index-url ${PIP_INDEX_URL} \
    --timeout 120 \
    --retries 5 \
    -r requirements.txt

# Bake the speech models into the image, so a production box starts without
# reaching Hugging Face and a cold start cannot fail on a slow network.
ARG STT_MODEL=small.en
RUN python -c "from faster_whisper import WhisperModel; WhisperModel('${STT_MODEL}', device='cpu', compute_type='int8'); from paradee import Paradee; Paradee()('Ready.')"

RUN useradd --uid 10001 --no-create-home media && chown -R media:media /models

# Copy application
COPY . .

USER media
EXPOSE 8200

CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8200"]
