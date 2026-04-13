FROM python:3.11-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends wget espeak-ng && \
    pip install --no-cache-dir flask piper-tts pathvalidate && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

RUN mkdir -p /models && \
    wget -q -O /models/en_US-lessac-medium.onnx \
      "https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/lessac/medium/en_US-lessac-medium.onnx" && \
    wget -q -O /models/en_US-lessac-medium.onnx.json \
      "https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/lessac/medium/en_US-lessac-medium.onnx.json"

COPY serve.py /serve.py

EXPOSE 5000
CMD ["python", "/serve.py"]
