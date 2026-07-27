# Phonara pronunciation-engine — alignment-free CTC GOP
#
# Mọi phiên bản ở đây đều bị pin. espeak-ng đặc biệt quan trọng: nó không nằm trong code
# cũng không nằm trong artifact, nên một lần build lại có thể đổi chuỗi phoneme và kéo
# theo mọi thứ phía sau mà không để lại dấu vết (R10, §8 g2p_version).
FROM python:3.11-slim

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    HF_HOME=/models \
    TOKENIZERS_PARALLELISM=false

RUN apt-get update \
 && apt-get install -y --no-install-recommends espeak-ng curl \
 && rm -rf /var/lib/apt/lists/* \
 && espeak-ng --version

# torch CPU-only: bản mặc định kéo theo CUDA ~2 GB không dùng đến
RUN pip install --no-cache-dir \
      --extra-index-url https://download.pytorch.org/whl/cpu \
      "torch==2.5.1" \
 && pip install --no-cache-dir \
      "transformers==4.46.3" \
      "phonemizer==3.3.0" \
      "fastapi==0.115.5" \
      "uvicorn==0.32.1" \
      "pydantic==2.9.2" \
      "pydantic-settings==2.6.1" \
      "python-multipart==0.0.19" \
      "numpy==2.1.3"

WORKDIR /srv
COPY artifacts/ ./artifacts/
COPY app/ ./app/

# Tải sẵn weights vào image thay vì tải lúc khởi động: container khởi động nhanh, và
# quan trọng hơn — không phụ thuộc mạng HuggingFace lúc deploy.
ARG MODEL_ID=facebook/wav2vec2-xlsr-53-espeak-cv-ft
ARG MODEL_REVISION=2c733782da5604684829819a5eb744c193fe9398
RUN python -c "\
from transformers import Wav2Vec2ForCTC, Wav2Vec2PhonemeCTCTokenizer; \
Wav2Vec2PhonemeCTCTokenizer.from_pretrained('${MODEL_ID}', revision='${MODEL_REVISION}'); \
Wav2Vec2ForCTC.from_pretrained('${MODEL_ID}', revision='${MODEL_REVISION}')" \
 && du -sh /models

EXPOSE 8000

# MỘT worker. Nhiều worker = nhiều bản sao model ~1.2 GB trong RAM (§3.4).
# Đồng thời được kiểm soát bằng semaphore + threadpool trong app, không bằng process.
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000", "--workers", "1"]
