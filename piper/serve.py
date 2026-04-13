import os
import subprocess
import tempfile
from flask import Flask, request, send_file, jsonify

app = Flask(__name__)
MODEL_PATH = os.environ.get("PIPER_MODEL", "/models/en_US-lessac-medium.onnx")


@app.route("/synthesize", methods=["POST"])
def synthesize():
    data = request.get_json()
    if not data or "text" not in data:
        return jsonify({"error": "missing text field"}), 400

    text = data["text"]
    with tempfile.NamedTemporaryFile(suffix=".wav", delete=False) as tmp:
        tmp_path = tmp.name

    try:
        proc = subprocess.run(
            ["piper", "--model", MODEL_PATH, "--output_file", tmp_path],
            input=text.encode("utf-8"),
            capture_output=True,
            timeout=60,
        )
        if proc.returncode != 0:
            return jsonify({"error": proc.stderr.decode()}), 500

        return send_file(tmp_path, mimetype="audio/wav")
    finally:
        try:
            os.unlink(tmp_path)
        except OSError:
            pass


@app.route("/health", methods=["GET"])
def health():
    return jsonify({"status": "ok", "model": MODEL_PATH})


if __name__ == "__main__":
    print(f"Piper TTS ready with model: {MODEL_PATH}", flush=True)
    app.run(host="0.0.0.0", port=5000)
