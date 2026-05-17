import os, tempfile
from flask import Flask, request, jsonify
from faster_whisper import WhisperModel

app = Flask(__name__)
model = WhisperModel("medium.en", compute_type="int8")

@app.route("/transcribe", methods=["POST"])
def transcribe():
    if "file" not in request.files:
        return jsonify({"error": "no file"}), 400
    f = request.files["file"]
    with tempfile.NamedTemporaryFile(suffix=".webm", delete=True) as tmp:
        f.save(tmp.name)
        segments, _ = model.transcribe(tmp.name, language="en")
        text = " ".join(s.text.strip() for s in segments)
    return jsonify({"text": text})

@app.route("/health")
def health():
    return jsonify({"status": "ok"})

if __name__ == "__main__":
    app.run(host="0.0.0.0", port=int(os.environ.get("PORT", "5001")))
