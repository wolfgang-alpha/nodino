FROM python:3.11-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
        ffmpeg \
    && rm -rf /var/lib/apt/lists/*

RUN pip install --no-cache-dir flask faster-whisper

RUN python -c "from faster_whisper import WhisperModel; WhisperModel('medium.en', compute_type='int8')"

EXPOSE 5001

COPY serve.py /app/serve.py
WORKDIR /app

CMD ["python", "serve.py"]
