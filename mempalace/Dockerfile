FROM python:3.12-slim

ARG MEMPALACE_REPO=https://github.com/wolfgang-alpha/mempalace.git
ARG MEMPALACE_REF=docker-rest

ENV PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    HOME=/data

RUN apt-get update && apt-get install -y --no-install-recommends \
        git build-essential \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
RUN git clone --depth 1 --branch "${MEMPALACE_REF}" "${MEMPALACE_REPO}" /app

RUN pip install -e ".[api]"

EXPOSE 8000

CMD ["sh", "-c", "\
    mkdir -p /data/.mempalace && \
    python -c 'from mempalace.config import MempalaceConfig; MempalaceConfig().init()' && \
    exec uvicorn mempalace.rest_server:app --host 0.0.0.0 --port 8000 \
"]
